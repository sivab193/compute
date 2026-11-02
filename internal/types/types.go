// Package types defines the shared data model used by the control plane,
// the node agent, and the CLI. Keeping these in one package means the wire
// format (JSON over the tailnet) is defined exactly once.
package types

import "time"

// NodeStatus is the lifecycle state of a node as seen by the control plane.
type NodeStatus string

const (
	NodeOnline  NodeStatus = "online"
	NodeOffline NodeStatus = "offline"
)

// GPUType classifies a node's accelerator so the scheduler never lands a CUDA
// job on a Metal-only box (or vice versa).
type GPUType string

const (
	GPUNone  GPUType = "none"
	GPUCUDA  GPUType = "cuda"
	GPUMetal GPUType = "metal"
)

// GPUInfo describes a single accelerator on a node.
type GPUInfo struct {
	Model  string  `json:"model"`
	VRAMMB uint64  `json:"vram_mb"`
	Type   GPUType `json:"type"`
}

// Heartbeat is the periodic health + capacity report an agent sends to the
// control plane. The agent sends it on register and then every ~15s.
type Heartbeat struct {
	NodeName    string    `json:"node_name"`
	CPUCount    int       `json:"cpu_count"`
	LoadAvg1    float64   `json:"load_avg_1"`
	GPUs        []GPUInfo `json:"gpus"`
	RAMTotalMB  uint64    `json:"ram_total_mb"`
	RAMFreeMB   uint64    `json:"ram_free_mb"`
	DiskTotalMB uint64    `json:"disk_total_mb"`
	DiskFreeMB  uint64    `json:"disk_free_mb"`
	SwapTotalMB uint64    `json:"swap_total_mb"`
	SwapFreeMB  uint64    `json:"swap_free_mb"`
	// Labels: arch, os, gpu-type, preemptible, location.
	Labels map[string]string `json:"labels"`
	// RunningJobs is the count of jobs the agent currently has executing.
	RunningJobs int `json:"running_jobs"`
	// DockerAvailable signals whether the node can isolate jobs in containers.
	DockerAvailable bool   `json:"docker_available"`
	AgentVersion    string `json:"agent_version"`
}

// Node is the control plane's view of a registered machine.
type Node struct {
	Name            string            `json:"name"`
	Token           string            `json:"-"` // per-node join token, never serialized out
	Status          NodeStatus        `json:"status"`
	CPUCount        int               `json:"cpu_count"`
	LoadAvg1        float64           `json:"load_avg_1"`
	GPUs            []GPUInfo         `json:"gpus"`
	RAMTotalMB      uint64            `json:"ram_total_mb"`
	RAMFreeMB       uint64            `json:"ram_free_mb"`
	DiskTotalMB     uint64            `json:"disk_total_mb"`
	DiskFreeMB      uint64            `json:"disk_free_mb"`
	SwapTotalMB     uint64            `json:"swap_total_mb"`
	SwapFreeMB      uint64            `json:"swap_free_mb"`
	Labels          map[string]string `json:"labels"`
	RunningJobs     int               `json:"running_jobs"`
	DockerAvailable bool              `json:"docker_available"`
	AgentVersion    string            `json:"agent_version"`
	LastHeartbeat   time.Time         `json:"last_heartbeat"`
	RegisteredAt    time.Time         `json:"registered_at"`
}

// Preemptible reports whether this node may vanish mid-job (e.g. Colab).
func (n *Node) Preemptible() bool {
	return n.Labels["preemptible"] == "true"
}

// GPUTypeLabel returns the node's declared gpu-type label (cuda/metal/none).
func (n *Node) GPUTypeLabel() GPUType {
	switch n.Labels["gpu-type"] {
	case "cuda":
		return GPUCUDA
	case "metal":
		return GPUMetal
	default:
		return GPUNone
	}
}

// JobState is the lifecycle of a submitted job.
type JobState string

const (
	JobQueued  JobState = "queued"
	JobRunning JobState = "running"
	JobDone    JobState = "done"
	JobFailed  JobState = "failed"
)

// RuntimeKind is the inferred file type that selects a runner on the node.
type RuntimeKind string

const (
	RuntimePython   RuntimeKind = "python"   // .py
	RuntimeNotebook RuntimeKind = "notebook" // .ipynb
	RuntimeJava     RuntimeKind = "java"     // .java
)

// SwapLane records which scheduler tier a running job landed in (§6).
type SwapLane string

const (
	LaneFast SwapLane = "fast" // fits in RAM
	LaneSlow SwapLane = "slow" // fits only in RAM+swap, expect slowdown
	LaneNone SwapLane = ""     // not yet scheduled
)

// Job is the full control-plane record for a submitted job.
type Job struct {
	ID        string      `json:"id"`
	Name      string      `json:"name"`
	Runtime   RuntimeKind `json:"runtime"`
	EntryFile string      `json:"entry_file"` // name of the file to run within the bundle
	State     JobState    `json:"state"`

	// Requirements (the scheduler filters/fits against these).
	NeedsGPU     GPUType `json:"needs_gpu"`     // "" means no GPU required
	MinRAMMB     uint64  `json:"min_ram_mb"`    // 0 means unspecified
	NeedsArch    string  `json:"needs_arch"`    // "" means any
	NeedsOS      string  `json:"needs_os"`      // "" means any
	AllowPreempt bool    `json:"allow_preempt"` // opt-in to preemptible nodes
	DiskBacked   bool    `json:"disk_backed"`   // §6.4: sparse mmap weights, don't demand full RAM
	TimeoutSec   int     `json:"timeout_sec"`   // 0 means server default

	// Assignment + results.
	AssignedNode string   `json:"assigned_node"`
	Lane         SwapLane `json:"lane"`
	ExitCode     *int     `json:"exit_code"`
	Reason       string   `json:"reason"` // e.g. "no node fits" explanation

	SubmittedAt time.Time  `json:"submitted_at"`
	StartedAt   *time.Time `json:"started_at"`
	FinishedAt  *time.Time `json:"finished_at"`

	// ArtifactCount is how many output files the job produced.
	ArtifactCount int `json:"artifact_count"`
}

// RegisterRequest is sent by an agent to join the pool.
type RegisterRequest struct {
	NodeName  string    `json:"node_name"`
	Token     string    `json:"token"`
	Heartbeat Heartbeat `json:"heartbeat"`
}

// RegisterResponse confirms a successful join.
type RegisterResponse struct {
	OK            bool   `json:"ok"`
	HeartbeatSec  int    `json:"heartbeat_sec"`
	ServerVersion string `json:"server_version"`
}

// LeaseResponse is what the control plane hands an agent that asks for work.
// If Job is nil, there is nothing to run right now.
type LeaseResponse struct {
	Job  *Job     `json:"job"`
	Lane SwapLane `json:"lane"`
}

// LogLine is one streamed line of job output.
type LogLine struct {
	JobID  string    `json:"job_id"`
	Stream string    `json:"stream"` // "stdout" | "stderr" | "system"
	Text   string    `json:"text"`
	At     time.Time `json:"at"`
}

// JobResult is posted by the agent when a job finishes.
type JobResult struct {
	ExitCode int      `json:"exit_code"`
	State    JobState `json:"state"`
	Reason   string   `json:"reason"`
}

// Aggregate is the dashboard header: totals across ONLINE nodes only (§3).
type Aggregate struct {
	OnlineNodes  int    `json:"online_nodes"`
	OfflineNodes int    `json:"offline_nodes"`
	VCPUs        int    `json:"vcpus"`
	GPUCount     int    `json:"gpu_count"`
	VRAMTotalMB  uint64 `json:"vram_total_mb"`
	CUDAVRAMMB   uint64 `json:"cuda_vram_mb"`
	MetalVRAMMB  uint64 `json:"metal_vram_mb"`
	RAMTotalMB   uint64 `json:"ram_total_mb"`
	SwapTotalMB  uint64 `json:"swap_total_mb"`
	DiskTotalMB  uint64 `json:"disk_total_mb"`
	RunningJobs  int    `json:"running_jobs"`
	QueuedJobs   int    `json:"queued_jobs"`
}

// Usage is the Phase 3 usage view: lifetime job accounting the dashboard shows
// so the operator can see where compute went across the fleet.
type Usage struct {
	TotalJobs    int            `json:"total_jobs"`
	ByState      map[string]int `json:"by_state"`       // queued/running/done/failed counts
	ByRuntime    map[string]int `json:"by_runtime"`     // python/notebook/java counts
	ByNode       map[string]int `json:"by_node"`        // jobs run per node
	SlowLaneJobs int            `json:"slow_lane_jobs"` // how many ran in swap (§6)
}
