// Package server is the control plane ("the place"). It owns the node
// registry, the job queue, the FIFO scheduler, the log store, and the artifact
// store, and serves the web dashboard. It never runs user code itself — it
// dispatches jobs to node agents over the tailnet.
package server

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/siv19/compute/internal/idgen"
	"github.com/siv19/compute/internal/store"
	"github.com/siv19/compute/internal/types"
)

// Version is the server/protocol version reported to agents and the dashboard.
const Version = "0.1.0"

// offlineAfter is how long without a heartbeat before a node is marked offline.
// With a 15s heartbeat, ~3 missed beats is a reasonable threshold.
const offlineAfter = 50 * time.Second

//go:embed web/*
var webFS embed.FS

// Config configures the control plane.
type Config struct {
	Addr              string // listen address, e.g. "127.0.0.1:8080"
	DataDir           string // where the SQLite DB + job bundles live
	JoinToken         string // shared secret an agent must present to register
	DashToken         string // optional bearer token to view/submit via the dashboard/API
	DefaultTimeoutSec int    // default job timeout when a job specifies none
	DistDir           string // optional: directory of agent binaries served at /dist for bootstrap
	PublicURL         string // optional: externally reachable base URL used in generated onboarding commands
}

// Server is the running control plane.
type Server struct {
	cfg     Config
	st      *store.Store
	bundles *bundleStore
	mux     *http.ServeMux

	// mu guards the lightweight in-memory scheduling pass so we don't double
	// assign a node within a single tick.
	mu sync.Mutex
}

// New constructs a Server, opening the database and artifact store.
func New(cfg Config) (*Server, error) {
	if cfg.DefaultTimeoutSec == 0 {
		cfg.DefaultTimeoutSec = 3600
	}
	st, err := store.Open(filepath.Join(cfg.DataDir, "compute.db"))
	if err != nil {
		return nil, fmt.Errorf("open store: %w", err)
	}
	bundles, err := newBundleStore(cfg.DataDir)
	if err != nil {
		return nil, fmt.Errorf("bundle store: %w", err)
	}
	s := &Server{cfg: cfg, st: st, bundles: bundles, mux: http.NewServeMux()}
	s.routes()
	return s, nil
}

// Close releases resources.
func (s *Server) Close() error { return s.st.Close() }

// Run starts the HTTP server and background loops until ctx is cancelled.
func (s *Server) Run(ctx context.Context) error {
	go s.offlineSweeper(ctx)

	srv := &http.Server{
		Addr:    s.cfg.Addr,
		Handler: s.logRequests(s.mux),
	}
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()

	log.Printf("control plane %s listening on %s (data=%s)", Version, s.cfg.Addr, s.cfg.DataDir)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

func (s *Server) routes() {
	// Agent-facing API (authenticated by join token).
	s.mux.HandleFunc("POST /api/register", s.handleRegister)
	s.mux.HandleFunc("POST /api/heartbeat", s.handleHeartbeat)
	s.mux.HandleFunc("POST /api/lease", s.handleLease)
	s.mux.HandleFunc("GET /api/jobs/{id}/bundle", s.handleBundleDownload)
	s.mux.HandleFunc("POST /api/jobs/{id}/logs", s.handleLogAppend)
	s.mux.HandleFunc("POST /api/jobs/{id}/artifacts", s.handleArtifactUpload)
	s.mux.HandleFunc("POST /api/jobs/{id}/result", s.handleJobResult)

	// Dashboard/CLI-facing API (authenticated by dash token when configured).
	s.mux.HandleFunc("GET /api/aggregate", s.handleAggregate)
	s.mux.HandleFunc("GET /api/capabilities", s.handleCapabilities)
	s.mux.HandleFunc("POST /api/feasibility", s.handleFeasibility)
	s.mux.HandleFunc("GET /api/colab", s.handleColab)
	s.mux.HandleFunc("GET /api/enroll", s.handleEnroll)
	s.mux.HandleFunc("GET /api/usage", s.handleUsage)
	s.mux.HandleFunc("GET /api/nodes", s.handleListNodes)
	s.mux.HandleFunc("DELETE /api/nodes/{name}", s.handleDeleteNode)
	s.mux.HandleFunc("GET /api/jobs", s.handleListJobs)
	s.mux.HandleFunc("POST /api/jobs", s.handleSubmitJob)
	s.mux.HandleFunc("GET /api/jobs/{id}", s.handleGetJob)
	s.mux.HandleFunc("GET /api/jobs/{id}/logs/stream", s.handleLogStream)
	s.mux.HandleFunc("GET /api/jobs/{id}/artifacts", s.handleListArtifacts)
	s.mux.HandleFunc("GET /api/jobs/{id}/artifacts/{name...}", s.handleArtifactDownload)

	// Agent binary distribution for node bootstrap (Phase 3). Optional.
	if s.cfg.DistDir != "" {
		s.mux.Handle("GET /dist/", http.StripPrefix("/dist/",
			http.FileServer(http.Dir(s.cfg.DistDir))))
	}

	// Dashboard static assets.
	sub, _ := fs.Sub(webFS, "web")
	s.mux.Handle("GET /", http.FileServer(http.FS(sub)))
}

// ---- middleware -----------------------------------------------------------

func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		if !strings.HasPrefix(r.URL.Path, "/api/heartbeat") &&
			!strings.HasPrefix(r.URL.Path, "/api/lease") {
			log.Printf("%s %s %s", r.Method, r.URL.Path, time.Since(start).Round(time.Millisecond))
		}
	})
}

// requireJoinToken authenticates an agent request.
func (s *Server) requireJoinToken(w http.ResponseWriter, r *http.Request) bool {
	if s.cfg.JoinToken == "" {
		return true
	}
	if bearer(r) == s.cfg.JoinToken {
		return true
	}
	writeErr(w, http.StatusUnauthorized, "invalid or missing join token")
	return false
}

// requireDashToken authenticates a dashboard/CLI request. When no dash token is
// configured, the API is open to anyone who can reach the tailnet address —
// which, per the plan, is the real security boundary.
func (s *Server) requireDashToken(w http.ResponseWriter, r *http.Request) bool {
	if s.cfg.DashToken == "" {
		return true
	}
	if bearer(r) == s.cfg.DashToken {
		return true
	}
	writeErr(w, http.StatusUnauthorized, "invalid or missing dashboard token")
	return false
}

func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if strings.HasPrefix(h, "Bearer ") {
		return strings.TrimPrefix(h, "Bearer ")
	}
	// Fall back to a query param for convenience in the dashboard/SSE.
	return r.URL.Query().Get("token")
}

// ---- background loops -----------------------------------------------------

// offlineSweeper marks nodes offline when their heartbeat goes stale.
func (s *Server) offlineSweeper(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			nodes, err := s.st.ListNodes()
			if err != nil {
				continue
			}
			now := time.Now()
			for _, n := range nodes {
				if n.Status == types.NodeOnline && now.Sub(n.LastHeartbeat) > offlineAfter {
					_ = s.st.SetNodeStatus(n.Name, types.NodeOffline)
					log.Printf("node %s marked offline (no heartbeat for %s)",
						n.Name, now.Sub(n.LastHeartbeat).Round(time.Second))
				}
			}
		}
	}
}

// ---- small response helpers ----------------------------------------------

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func (s *Server) defaultTimeout(sec int) int {
	if sec <= 0 {
		return s.cfg.DefaultTimeoutSec
	}
	return sec
}

// RuntimeFromFilename infers the runtime kind from a filename extension.
func RuntimeFromFilename(name string) (types.RuntimeKind, bool) {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".py":
		return types.RuntimePython, true
	case ".ipynb":
		return types.RuntimeNotebook, true
	case ".java":
		return types.RuntimeJava, true
	}
	return "", false
}

// GenerateNodeToken is exposed so the CLI/admin can mint per-node tokens.
func GenerateNodeToken() string { return idgen.Token() }
