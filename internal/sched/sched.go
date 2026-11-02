// Package sched implements the v1 scheduler described in §4 of the plan:
// a deliberately simple, FIFO, one-job-per-node matcher. It filters nodes by
// labels (GPU type, arch, OS, preemptible opt-in), then does a fit check
// against RAM (fast lane) and RAM+swap (slow lane, §6).
package sched

import (
	"fmt"

	"github.com/siv19/compute/internal/types"
)

// Decision is the outcome of trying to place one job.
type Decision struct {
	Node   *types.Node    // chosen node, nil if none fits
	Lane   types.SwapLane // fast or slow when Node != nil
	Reason string         // human-readable explanation (always set)
}

// Place chooses a node for job j out of the candidate nodes. Only ONLINE nodes
// with no job currently running are considered busy-free; the control plane is
// responsible for passing the live node list. This never spans nodes — each
// job runs on exactly one machine (§4, "What v1 is NOT").
func Place(j *types.Job, nodes []*types.Node) Decision {
	var eligible []*types.Node
	var skipped []string

	for _, n := range nodes {
		if n.Status != types.NodeOnline {
			continue // offline nodes never count toward available capacity (§3)
		}
		if reason, ok := labelsMatch(j, n); !ok {
			skipped = append(skipped, fmt.Sprintf("%s (%s)", n.Name, reason))
			continue
		}
		eligible = append(eligible, n)
	}

	if len(eligible) == 0 {
		r := "no node matches the job's requirements"
		if len(skipped) > 0 {
			r = fmt.Sprintf("no matching node; filtered: %v", skipped)
		}
		return Decision{Reason: r}
	}

	// Among eligible nodes, prefer: (1) idle over busy, (2) fast lane over slow
	// lane, (3) more free RAM. FIFO is enforced by the caller processing the
	// queue oldest-first and only placing one job per idle node per pass.
	var best *types.Node
	var bestLane types.SwapLane
	bestScore := -1

	for _, n := range eligible {
		lane, fits := fitCheck(j, n)
		if !fits {
			continue
		}
		score := 0
		if n.RunningJobs == 0 {
			score += 1000 // strongly prefer idle machines
		}
		if lane == types.LaneFast {
			score += 500 // prefer fast lane over swap
		}
		// tie-break on free RAM (MB), capped so it can't dominate the flags above
		score += int(min64(n.RAMFreeMB, 400))
		if score > bestScore {
			bestScore = score
			best = n
			bestLane = lane
		}
	}

	if best == nil {
		// Eligible by labels, but none fits even in RAM+swap.
		need := j.MinRAMMB
		return Decision{Reason: fmt.Sprintf(
			"no node fits: job needs ~%d MB RAM, no eligible node has that much RAM+swap free",
			need)}
	}

	if best.RunningJobs > 0 {
		// All fitting nodes are busy -> stay queued (FIFO). Caller leaves it queued.
		return Decision{Reason: "all fitting nodes are busy; queued (FIFO)"}
	}

	reason := fmt.Sprintf("placed on %s in %s lane", best.Name, bestLane)
	if bestLane == types.LaneSlow {
		reason = fmt.Sprintf(
			"placed on %s in SLOW lane: job will run in swap, expect a large slowdown (§6)",
			best.Name)
	}
	return Decision{Node: best, Lane: bestLane, Reason: reason}
}

// labelsMatch applies the hard filters from §4 step 1.
func labelsMatch(j *types.Job, n *types.Node) (string, bool) {
	// GPU type: a CUDA job never lands on a Metal-only node, and vice versa.
	if j.NeedsGPU != "" && j.NeedsGPU != types.GPUNone {
		if n.GPUTypeLabel() != j.NeedsGPU {
			return fmt.Sprintf("gpu-type=%s, needs %s", n.GPUTypeLabel(), j.NeedsGPU), false
		}
	}
	// Architecture.
	if j.NeedsArch != "" && n.Labels["arch"] != j.NeedsArch {
		return fmt.Sprintf("arch=%s, needs %s", n.Labels["arch"], j.NeedsArch), false
	}
	// OS.
	if j.NeedsOS != "" && n.Labels["os"] != j.NeedsOS {
		return fmt.Sprintf("os=%s, needs %s", n.Labels["os"], j.NeedsOS), false
	}
	// Toolchain: the node must be able to execute this runtime at all.
	if j.Runtime != "" && !n.CanRun(j.Runtime) {
		return fmt.Sprintf("cannot run %s jobs", j.Runtime), false
	}
	// Preemptible nodes (Colab) are excluded from default scheduling unless the
	// job opts in (§3).
	if n.Preemptible() && !j.AllowPreempt {
		return "preemptible; job did not opt in", false
	}
	return "", true
}

// fitCheck implements §6.2 scheduler tiers.
//   - disk-backed jobs (§6.4) do NOT demand full RAM — access is sparse by
//     design — so they always "fit" on an eligible node in the fast lane.
//   - a job that fits in free RAM -> fast lane.
//   - a job that fits only in free RAM + free swap -> slow lane (allowed, warned).
//   - otherwise -> does not fit.
func fitCheck(j *types.Job, n *types.Node) (types.SwapLane, bool) {
	if j.DiskBacked || j.MinRAMMB == 0 {
		return types.LaneFast, true
	}
	if j.MinRAMMB <= n.RAMFreeMB {
		return types.LaneFast, true
	}
	if j.MinRAMMB <= n.RAMFreeMB+n.SwapFreeMB {
		return types.LaneSlow, true
	}
	return types.LaneNone, false
}

func min64(a, b uint64) uint64 {
	if a < b {
		return a
	}
	return b
}
