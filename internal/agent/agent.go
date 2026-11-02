package agent

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/siv19/compute/internal/hostinfo"
	"github.com/siv19/compute/internal/types"
)

// Version is the agent version reported in heartbeats.
const Version = "0.1.0"

// Config configures a node agent.
type Config struct {
	ServerURL  string            // control plane base URL, e.g. https://mini.tailnet.ts.net
	NodeName   string            // unique node name
	JoinToken  string            // shared join token
	Labels     map[string]string // operator-supplied labels (preemptible, location, …)
	WorkRoot   string            // where job workdirs are created
	ForceBare  bool              // never use Docker even if available (macOS default)
	MemCapFrac float64           // per-job memory cap as a fraction of node RAM (0 = none)
}

// Agent is a running node daemon.
type Agent struct {
	cfg         Config
	cli         *client
	runningJobs int32 // atomic; mirrored into heartbeats
}

// New constructs an Agent.
func New(cfg Config) *Agent {
	if cfg.WorkRoot == "" {
		cfg.WorkRoot = filepath.Join(os.TempDir(), "pool-agent")
	}
	// Make workdir discoverable to hostinfo so disk stats reflect the job disk.
	_ = os.Setenv("POOL_WORKDIR", cfg.WorkRoot)
	return &Agent{cfg: cfg, cli: newClient(cfg.ServerURL, cfg.JoinToken)}
}

// Run registers the node and then loops on heartbeat + lease until ctx ends.
func (a *Agent) Run(ctx context.Context) error {
	if err := os.MkdirAll(a.cfg.WorkRoot, 0o755); err != nil {
		return fmt.Errorf("create work root: %w", err)
	}

	hb := a.snapshot()
	resp, err := a.cli.register(ctx, types.RegisterRequest{
		NodeName:  a.cfg.NodeName,
		Token:     a.cfg.JoinToken,
		Heartbeat: hb,
	})
	if err != nil {
		return fmt.Errorf("register: %w", err)
	}
	hbEvery := time.Duration(resp.HeartbeatSec) * time.Second
	if hbEvery <= 0 {
		hbEvery = 15 * time.Second
	}
	log.Printf("registered node %q with control plane %s (server %s)",
		a.cfg.NodeName, a.cfg.ServerURL, resp.ServerVersion)
	log.Printf("docker=%v force-bare=%v work=%s", hb.DockerAvailable, a.cfg.ForceBare, a.cfg.WorkRoot)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); a.heartbeatLoop(ctx, hbEvery) }()
	go func() { defer wg.Done(); a.leaseLoop(ctx) }()
	wg.Wait()
	return nil
}

// snapshot builds the current heartbeat, injecting the live running-job count.
func (a *Agent) snapshot() types.Heartbeat {
	hb := hostinfo.Collect(a.cfg.NodeName, a.cfg.Labels, Version)
	hb.RunningJobs = int(atomic.LoadInt32(&a.runningJobs))
	if a.cfg.ForceBare {
		hb.DockerAvailable = false
	}
	return hb
}

func (a *Agent) heartbeatLoop(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := a.cli.heartbeat(ctx, a.snapshot()); err != nil {
				log.Printf("heartbeat error: %v", err)
			}
		}
	}
}

func (a *Agent) leaseLoop(ctx context.Context) {
	t := time.NewTicker(3 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			// One job at a time in v1.
			if atomic.LoadInt32(&a.runningJobs) > 0 {
				continue
			}
			lease, err := a.cli.lease(ctx, a.cfg.NodeName)
			if err != nil {
				log.Printf("lease error: %v", err)
				continue
			}
			if lease.Job == nil {
				continue
			}
			atomic.AddInt32(&a.runningJobs, 1)
			go func(j *types.Job) {
				defer atomic.AddInt32(&a.runningJobs, -1)
				a.runJob(ctx, j)
			}(lease.Job)
		}
	}
}

// runJob is the full per-job pipeline: fetch bundle -> extract -> execute with
// live log streaming -> collect artifacts -> report result.
func (a *Agent) runJob(ctx context.Context, j *types.Job) {
	log.Printf("running job %s (%s, entry=%s)", j.ID, j.Runtime, j.EntryFile)
	workDir := filepath.Join(a.cfg.WorkRoot, j.ID)
	_ = os.RemoveAll(workDir)
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		a.failJob(ctx, j, fmt.Sprintf("mkdir workdir: %v", err))
		return
	}
	defer os.RemoveAll(workDir)

	// Batched log shipper so we don't POST per line.
	stream := newLogShipper(ctx, a.cli, j.ID)
	defer stream.flush()

	// 1. Download + extract bundle.
	if err := a.fetchBundle(ctx, j.ID, workDir); err != nil {
		stream.add("system", "fetch bundle failed: "+err.Error())
		stream.flush()
		a.failJob(ctx, j, "fetch bundle: "+err.Error())
		return
	}

	// 2. Decide isolation + memory cap.
	useDocker := !a.cfg.ForceBare && dockerUsable(j)
	var memCap uint64
	if a.cfg.MemCapFrac > 0 {
		hb := hostinfo.Collect(a.cfg.NodeName, a.cfg.Labels, Version)
		memCap = uint64(float64(hb.RAMTotalMB) * a.cfg.MemCapFrac)
	}

	// 3. Execute.
	res := execute(ctx, runSpec{
		Job:        j,
		WorkDir:    workDir,
		UseDocker:  useDocker,
		MemLimitMB: memCap,
		Log:        stream.add,
	})
	stream.flush()

	// 4. Collect artifacts: anything in the workdir that isn't the original
	//    source bundle venv/cache. We ship the whole workdir minus .venv.
	if err := a.uploadArtifacts(ctx, j.ID, workDir); err != nil {
		stream.add("system", "artifact upload failed: "+err.Error())
		stream.flush()
	}

	// 5. Report result.
	state := types.JobDone
	reason := ""
	if res.ExitCode != 0 {
		state = types.JobFailed
	}
	if res.Err != nil {
		reason = res.Err.Error()
	}
	if err := a.cli.reportResult(ctx, j.ID, types.JobResult{
		ExitCode: res.ExitCode, State: state, Reason: reason,
	}); err != nil {
		log.Printf("report result error for %s: %v", j.ID, err)
	}
	log.Printf("job %s finished: exit=%d state=%s", j.ID, res.ExitCode, state)
}

func (a *Agent) failJob(ctx context.Context, j *types.Job, reason string) {
	log.Printf("job %s failed: %s", j.ID, reason)
	_ = a.cli.reportResult(ctx, j.ID, types.JobResult{
		ExitCode: -1, State: types.JobFailed, Reason: reason,
	})
}

// dockerUsable reports whether this node should run the job in Docker.
func dockerUsable(j *types.Job) bool {
	// Metal GPU jobs cannot use Docker on macOS (no GPU passthrough), but we
	// only reach here on non-forced-bare nodes; still guard the Metal case.
	if j.NeedsGPU == types.GPUMetal {
		return false
	}
	return dockerDaemonUp()
}
