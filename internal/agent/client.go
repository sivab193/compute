// Package agent is the node daemon ("pool-agent"). One small process per
// machine: it registers with the control plane, heartbeats capacity every ~15s,
// leases queued jobs that fit this node, runs them in an isolated workdir
// (Docker where available, bare process otherwise), streams stdout/stderr live,
// uploads artifacts, and reports the exit code.
package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/siv19/compute/internal/types"
)

// client talks to the control plane HTTP API using the join token.
type client struct {
	base      string
	joinToken string
	http      *http.Client
}

func newClient(base, joinToken string) *client {
	return &client{
		base:      base,
		joinToken: joinToken,
		http:      &http.Client{Timeout: 60 * time.Second},
	}
}

func (c *client) do(ctx context.Context, method, path string, body io.Reader, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return err
	}
	if c.joinToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.joinToken)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("%s %s: %s: %s", method, path, resp.Status, bytes.TrimSpace(b))
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

func (c *client) register(ctx context.Context, req types.RegisterRequest) (types.RegisterResponse, error) {
	var out types.RegisterResponse
	b, _ := json.Marshal(req)
	err := c.do(ctx, http.MethodPost, "/api/register", bytes.NewReader(b), &out)
	return out, err
}

func (c *client) heartbeat(ctx context.Context, hb types.Heartbeat) error {
	b, _ := json.Marshal(hb)
	return c.do(ctx, http.MethodPost, "/api/heartbeat", bytes.NewReader(b), nil)
}

func (c *client) lease(ctx context.Context, nodeName string) (types.LeaseResponse, error) {
	var out types.LeaseResponse
	b, _ := json.Marshal(map[string]string{"node_name": nodeName})
	err := c.do(ctx, http.MethodPost, "/api/lease", bytes.NewReader(b), &out)
	return out, err
}

// downloadBundle fetches a job's source bundle (.tar.gz) and writes it to w.
func (c *client) downloadBundle(ctx context.Context, jobID string, w io.Writer) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.base+"/api/jobs/"+jobID+"/bundle", nil)
	if err != nil {
		return err
	}
	if c.joinToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.joinToken)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("bundle download: %s", resp.Status)
	}
	_, err = io.Copy(w, resp.Body)
	return err
}

// postLogs sends a batch of log lines (newline-delimited JSON) for a job.
func (c *client) postLogs(ctx context.Context, jobID string, lines []types.LogLine) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	for _, l := range lines {
		if err := enc.Encode(l); err != nil {
			return err
		}
	}
	return c.do(ctx, http.MethodPost, "/api/jobs/"+jobID+"/logs", &buf, nil)
}

// uploadArtifacts streams a .tar.gz of outputs for a job.
func (c *client) uploadArtifacts(ctx context.Context, jobID string, r io.Reader) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.base+"/api/jobs/"+jobID+"/artifacts", r)
	if err != nil {
		return err
	}
	if c.joinToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.joinToken)
	}
	req.Header.Set("Content-Type", "application/gzip")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("artifact upload: %s: %s", resp.Status, b)
	}
	return nil
}

func (c *client) reportResult(ctx context.Context, jobID string, res types.JobResult) error {
	b, _ := json.Marshal(res)
	return c.do(ctx, http.MethodPost, "/api/jobs/"+jobID+"/result", bytes.NewReader(b), nil)
}
