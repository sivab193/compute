// Command compute-server runs the shared compute pool control plane: node
// registry, job queue, FIFO scheduler, log store, artifact store, and the web
// dashboard. It is meant to live on the always-on Mac mini and be reachable
// only over the tailnet (see docs/HOSTING.md).
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/siv19/compute/internal/server"
)

func main() {
	var (
		addr    = flag.String("addr", envOr("POOL_ADDR", "127.0.0.1:8080"), "listen address")
		dataDir = flag.String("data", envOr("POOL_DATA", defaultDataDir()), "data directory (SQLite DB + job bundles)")
		joinTok = flag.String("join-token", os.Getenv("POOL_JOIN_TOKEN"), "shared join token agents must present")
		dashTok = flag.String("dash-token", os.Getenv("POOL_DASH_TOKEN"), "optional bearer token for dashboard/CLI access")
		timeout = flag.Int("default-timeout", 3600, "default per-job timeout in seconds")
		distDir = flag.String("dist", os.Getenv("POOL_DIST"), "optional directory of agent binaries served at /dist for node bootstrap")
	)
	flag.Parse()

	if err := os.MkdirAll(*dataDir, 0o755); err != nil {
		log.Fatalf("create data dir: %v", err)
	}
	if *joinTok == "" {
		log.Printf("WARNING: no --join-token set; any agent on the tailnet can join")
	}

	srv, err := server.New(server.Config{
		Addr:              *addr,
		DataDir:           *dataDir,
		JoinToken:         *joinTok,
		DashToken:         *dashTok,
		DefaultTimeoutSec: *timeout,
		DistDir:           *distDir,
	})
	if err != nil {
		log.Fatalf("init server: %v", err)
	}
	defer srv.Close()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := srv.Run(ctx); err != nil {
		log.Fatalf("server: %v", err)
	}
	log.Printf("shut down cleanly")
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func defaultDataDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "./pool-data"
	}
	return filepath.Join(home, ".compute-pool")
}
