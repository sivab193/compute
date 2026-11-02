package agent

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/siv19/compute/internal/types"
)

// ---- log shipper ----------------------------------------------------------

// logShipper batches log lines and flushes them to the control plane on an
// interval or when the buffer grows, so a chatty job doesn't cause one HTTP
// POST per line.
type logShipper struct {
	ctx   context.Context
	cli   *client
	jobID string

	mu   sync.Mutex
	buf  []types.LogLine
	stop chan struct{}
	once sync.Once
}

func newLogShipper(ctx context.Context, cli *client, jobID string) *logShipper {
	s := &logShipper{ctx: ctx, cli: cli, jobID: jobID, stop: make(chan struct{})}
	go s.loop()
	return s
}

func (s *logShipper) add(stream, text string) {
	s.mu.Lock()
	s.buf = append(s.buf, types.LogLine{
		JobID: s.jobID, Stream: stream, Text: text, At: time.Now(),
	})
	n := len(s.buf)
	s.mu.Unlock()
	if n >= 50 {
		s.flush()
	}
}

func (s *logShipper) flush() {
	s.mu.Lock()
	if len(s.buf) == 0 {
		s.mu.Unlock()
		return
	}
	batch := s.buf
	s.buf = nil
	s.mu.Unlock()
	// Use a short independent timeout so flush on a cancelled ctx still ships.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = s.cli.postLogs(ctx, s.jobID, batch)
}

func (s *logShipper) loop() {
	t := time.NewTicker(700 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-s.ctx.Done():
			return
		case <-t.C:
			s.flush()
		}
	}
}

// ---- bundle fetch / extract ----------------------------------------------

func (a *Agent) fetchBundle(ctx context.Context, jobID, workDir string) error {
	pr, pw := io.Pipe()
	go func() {
		err := a.cli.downloadBundle(ctx, jobID, pw)
		pw.CloseWithError(err)
	}()
	return extractTarGz(workDir, pr)
}

func extractTarGz(dir string, r io.Reader) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		target := filepath.Join(dir, filepath.Clean("/"+hdr.Name))
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, os.FileMode(hdr.Mode))
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return err
			}
			f.Close()
		}
	}
	return nil
}

// ---- artifact upload ------------------------------------------------------

// uploadArtifacts tars the workdir (excluding the source bundle's known
// scaffolding like .venv and __pycache__) and streams it to the control plane.
func (a *Agent) uploadArtifacts(ctx context.Context, jobID, workDir string) error {
	pr, pw := io.Pipe()
	go func() {
		err := tarWorkdirArtifacts(workDir, pw)
		pw.CloseWithError(err)
	}()
	return a.cli.uploadArtifacts(ctx, jobID, pr)
}

// excludedArtifact filters transient files we never want to ship back.
func excludedArtifact(rel string) bool {
	parts := strings.Split(filepath.ToSlash(rel), "/")
	for _, p := range parts {
		switch p {
		case ".venv", "__pycache__", ".ipynb_checkpoints", ".git":
			return true
		}
		if strings.HasSuffix(p, ".pyc") {
			return true
		}
	}
	return false
}

func tarWorkdirArtifacts(dir string, w io.Writer) error {
	gz := gzip.NewWriter(w)
	defer gz.Close()
	tw := tar.NewWriter(gz)
	defer tw.Close()
	return filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		if excludedArtifact(rel) {
			return nil
		}
		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		hdr.Name = filepath.ToSlash(rel)
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(tw, f)
		return err
	})
}

// ---- env + docker probes --------------------------------------------------

func getenv(key string) string { return os.Getenv(key) }

func dockerDaemonUp() bool {
	path, err := exec.LookPath("docker")
	if err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, path, "info", "--format", "{{.ServerVersion}}").Run() == nil
}
