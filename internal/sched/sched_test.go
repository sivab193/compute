package sched

import (
	"testing"

	"github.com/siv19/compute/internal/types"
)

func metalNode() *types.Node {
	return &types.Node{
		Name: "mini", Status: types.NodeOnline, CPUCount: 10,
		RAMTotalMB: 16000, RAMFreeMB: 8000, SwapTotalMB: 16000, SwapFreeMB: 16000,
		Labels: map[string]string{"arch": "arm64", "os": "darwin", "gpu-type": "metal", "preemptible": "false"},
	}
}

func cudaNode() *types.Node {
	return &types.Node{
		Name: "rog", Status: types.NodeOnline, CPUCount: 16,
		RAMTotalMB: 32000, RAMFreeMB: 24000, SwapTotalMB: 32000, SwapFreeMB: 32000,
		Labels: map[string]string{"arch": "amd64", "os": "linux", "gpu-type": "cuda", "preemptible": "false"},
	}
}

func colabNode() *types.Node {
	return &types.Node{
		Name: "colab", Status: types.NodeOnline, CPUCount: 2,
		RAMTotalMB: 12000, RAMFreeMB: 11000, SwapTotalMB: 0, SwapFreeMB: 0,
		Labels: map[string]string{"arch": "amd64", "os": "linux", "gpu-type": "cuda", "preemptible": "true"},
	}
}

// A CUDA job must never land on the Metal-only mini (§4 step 1).
func TestCUDANeverOnMetal(t *testing.T) {
	j := &types.Job{NeedsGPU: types.GPUCUDA, MinRAMMB: 4000}
	d := Place(j, []*types.Node{metalNode()})
	if d.Node != nil {
		t.Fatalf("CUDA job placed on metal node: %+v", d)
	}
}

// A CUDA job lands on the CUDA node.
func TestCUDAOnCUDA(t *testing.T) {
	j := &types.Job{NeedsGPU: types.GPUCUDA, MinRAMMB: 4000}
	d := Place(j, []*types.Node{metalNode(), cudaNode()})
	if d.Node == nil || d.Node.Name != "rog" {
		t.Fatalf("expected placement on rog, got %+v", d)
	}
	if d.Lane != types.LaneFast {
		t.Fatalf("expected fast lane, got %s", d.Lane)
	}
}

// A job fitting only in RAM+swap goes to the slow lane (§6.2).
func TestSlowLane(t *testing.T) {
	n := metalNode()
	n.RAMFreeMB = 4000
	n.SwapFreeMB = 16000
	j := &types.Job{MinRAMMB: 10000} // > free RAM, < RAM+swap
	d := Place(j, []*types.Node{n})
	if d.Node == nil {
		t.Fatalf("expected placement, got none: %s", d.Reason)
	}
	if d.Lane != types.LaneSlow {
		t.Fatalf("expected slow lane, got %s", d.Lane)
	}
}

// A job exceeding RAM+swap does not fit anywhere.
func TestNoFit(t *testing.T) {
	n := metalNode()
	n.RAMFreeMB = 4000
	n.SwapFreeMB = 4000
	j := &types.Job{MinRAMMB: 100000}
	d := Place(j, []*types.Node{n})
	if d.Node != nil {
		t.Fatalf("expected no fit, got placement on %s", d.Node.Name)
	}
}

// Preemptible (Colab) nodes are excluded unless the job opts in (§3).
func TestPreemptibleOptIn(t *testing.T) {
	j := &types.Job{NeedsGPU: types.GPUCUDA, MinRAMMB: 4000}
	// Without opt-in, only the colab node available -> no placement.
	d := Place(j, []*types.Node{colabNode()})
	if d.Node != nil {
		t.Fatalf("preemptible node used without opt-in: %+v", d)
	}
	// With opt-in, it places.
	j.AllowPreempt = true
	d = Place(j, []*types.Node{colabNode()})
	if d.Node == nil || d.Node.Name != "colab" {
		t.Fatalf("expected colab placement with opt-in, got %+v", d)
	}
}

// Disk-backed jobs (§6.4) do not demand full RAM.
func TestDiskBackedIgnoresRAMFit(t *testing.T) {
	n := metalNode()
	n.RAMFreeMB = 1000
	n.SwapFreeMB = 0
	j := &types.Job{MinRAMMB: 100000, DiskBacked: true}
	d := Place(j, []*types.Node{n})
	if d.Node == nil || d.Lane != types.LaneFast {
		t.Fatalf("disk-backed job should fit in fast lane regardless of RAM: %+v", d)
	}
}

// Offline nodes never count toward capacity (§3).
func TestOfflineExcluded(t *testing.T) {
	n := cudaNode()
	n.Status = types.NodeOffline
	j := &types.Job{NeedsGPU: types.GPUCUDA, MinRAMMB: 4000}
	d := Place(j, []*types.Node{n})
	if d.Node != nil {
		t.Fatalf("offline node was scheduled: %+v", d)
	}
}

// Idle node preferred over busy node.
func TestPrefersIdle(t *testing.T) {
	busy := cudaNode()
	busy.Name = "busy"
	busy.RunningJobs = 1
	idle := cudaNode()
	idle.Name = "idle"
	j := &types.Job{NeedsGPU: types.GPUCUDA, MinRAMMB: 4000}
	d := Place(j, []*types.Node{busy, idle})
	if d.Node == nil || d.Node.Name != "idle" {
		t.Fatalf("expected idle node, got %+v", d)
	}
}
