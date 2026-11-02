package sched

import (
	"fmt"
	"sort"

	"github.com/siv19/compute/internal/types"
)

// Capabilities derives what the pool can be asked for right now, from ONLINE
// nodes only. The submit form is built from this, so every offered option is
// one some live machine can satisfy.
func Capabilities(nodes []*types.Node, defaultTimeoutSec int) types.Capabilities {
	caps := types.Capabilities{DefaultTimeoutSec: defaultTimeoutSec}

	// Accumulators keyed by option value.
	gpu := map[string]*types.Option{}
	arch := map[string]*types.Option{}
	osys := map[string]*types.Option{}
	runtimeCount := map[types.RuntimeKind]*types.RuntimeOption{}
	for _, r := range []types.RuntimeKind{types.RuntimePython, types.RuntimeNotebook, types.RuntimeJava} {
		runtimeCount[r] = &types.RuntimeOption{
			Option: types.Option{Value: string(r), Label: runtimeLabel(r)},
		}
	}

	var online []*types.Node
	for _, n := range nodes {
		if n.Status != types.NodeOnline {
			caps.OfflineNodes++
			continue
		}
		caps.OnlineNodes++
		online = append(online, n)
		if n.RunningJobs == 0 {
			caps.IdleNodes++
		}
		if n.Preemptible() {
			caps.PreemptibleNodes++
		}
		if n.CPUCount > caps.MaxVCPUs {
			caps.MaxVCPUs = n.CPUCount
		}
		if n.RAMFreeMB > caps.MaxRAMFastMB {
			caps.MaxRAMFastMB = n.RAMFreeMB
		}
		if n.RAMFreeMB+n.SwapFreeMB > caps.MaxRAMSwapMB {
			caps.MaxRAMSwapMB = n.RAMFreeMB + n.SwapFreeMB
		}

		// GPU type, with the strongest device as the detail line.
		gt := n.GPUTypeLabel()
		if gt != types.GPUNone {
			o := gpu[string(gt)]
			if o == nil {
				o = &types.Option{Value: string(gt), Label: gpuLabel(gt)}
				gpu[string(gt)] = o
			}
			o.NodeCount++
			if d := bestGPUDetail(n); d != "" && len(d) > len(o.Detail) {
				o.Detail = d
			}
		}

		addOption(arch, n.Labels["arch"], n.Labels["arch"])
		addOption(osys, n.Labels["os"], osLabel(n.Labels["os"]))

		for r, ro := range runtimeCount {
			if !n.CanRun(r) {
				continue
			}
			ro.NodeCount++
			if n.RAMFreeMB > ro.MaxRAMFastMB {
				ro.MaxRAMFastMB = n.RAMFreeMB
			}
			if n.RAMFreeMB+n.SwapFreeMB > ro.MaxRAMSwapMB {
				ro.MaxRAMSwapMB = n.RAMFreeMB + n.SwapFreeMB
			}
		}
	}

	// Runtimes always appear, so the operator can see that a runtime exists but
	// nothing can run it, rather than wondering where the option went.
	for _, r := range []types.RuntimeKind{types.RuntimePython, types.RuntimeNotebook, types.RuntimeJava} {
		ro := *runtimeCount[r]
		ro.Available = ro.NodeCount > 0
		if !ro.Available {
			ro.Detail = "no online machine has the toolchain"
		} else {
			ro.Detail = pluralNodes(ro.NodeCount)
		}
		caps.Runtimes = append(caps.Runtimes, ro)
	}

	caps.GPUTypes = sortedOptions(gpu)
	caps.Arches = sortedOptions(arch)
	caps.OSes = sortedOptions(osys)
	return caps
}

// Evaluate answers "could a job like this be placed?" without scheduling it.
// It runs the same label filter and fit check the real scheduler uses, so the
// preview and the outcome cannot disagree.
func Evaluate(req types.FeasibilityRequest, nodes []*types.Node) types.FeasibilityResponse {
	job := &types.Job{
		Runtime:      req.Runtime,
		NeedsGPU:     req.NeedsGPU,
		MinRAMMB:     req.MinRAMMB,
		NeedsArch:    req.NeedsArch,
		NeedsOS:      req.NeedsOS,
		AllowPreempt: req.AllowPreempt,
		DiskBacked:   req.DiskBacked,
	}

	var resp types.FeasibilityResponse
	resp.Lane = types.LaneNone
	bestLane := types.LaneNone
	var filtered []string

	for _, n := range nodes {
		if n.Status != types.NodeOnline {
			continue
		}
		if reason, ok := labelsMatch(job, n); !ok {
			filtered = append(filtered, fmt.Sprintf("%s (%s)", n.Name, reason))
			continue
		}
		lane, fits := fitCheck(job, n)
		if !fits {
			filtered = append(filtered, fmt.Sprintf("%s (needs %d MB, has %d MB with swap)",
				n.Name, job.MinRAMMB, n.RAMFreeMB+n.SwapFreeMB))
			continue
		}
		resp.MatchCount++
		resp.Nodes = append(resp.Nodes, n.Name)
		if n.RunningJobs == 0 {
			resp.IdleMatches++
		}
		if lane == types.LaneFast {
			bestLane = types.LaneFast
		} else if bestLane != types.LaneFast {
			bestLane = types.LaneSlow
		}
	}

	resp.Lane = bestLane
	resp.Placeable = resp.MatchCount > 0
	resp.SlowLaneOnly = resp.Placeable && bestLane == types.LaneSlow

	switch {
	case !resp.Placeable && len(filtered) == 0:
		resp.Reason = "No machines are online. Start an agent to join the pool."
	case !resp.Placeable:
		resp.Reason = "No machine fits: " + joinMax(filtered, 3)
	case resp.SlowLaneOnly:
		resp.Reason = fmt.Sprintf(
			"%s can take this, but only by paging into swap. Expect a large slowdown.",
			pluralNodes(resp.MatchCount))
	case resp.IdleMatches == 0:
		resp.Reason = fmt.Sprintf("%s can run this; all are busy, so it will queue.",
			pluralNodes(resp.MatchCount))
	default:
		resp.Reason = fmt.Sprintf("%s can run this now.", pluralNodes(resp.IdleMatches))
	}
	return resp
}

// ---- helpers --------------------------------------------------------------

func addOption(m map[string]*types.Option, value, label string) {
	if value == "" {
		return
	}
	o := m[value]
	if o == nil {
		o = &types.Option{Value: value, Label: label}
		m[value] = o
	}
	o.NodeCount++
}

func sortedOptions(m map[string]*types.Option) []types.Option {
	out := make([]types.Option, 0, len(m))
	for _, o := range m {
		o.Available = o.NodeCount > 0
		if o.Detail == "" {
			o.Detail = pluralNodes(o.NodeCount)
		}
		out = append(out, *o)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Value < out[j].Value })
	return out
}

func bestGPUDetail(n *types.Node) string {
	for _, g := range n.GPUs {
		if g.VRAMMB > 0 {
			return fmt.Sprintf("%s · %d GB VRAM", g.Model, g.VRAMMB/1024)
		}
		if g.Model != "" {
			return g.Model
		}
	}
	return ""
}

func gpuLabel(t types.GPUType) string {
	switch t {
	case types.GPUCUDA:
		return "CUDA (NVIDIA)"
	case types.GPUMetal:
		return "Metal (Apple)"
	}
	return string(t)
}

func osLabel(s string) string {
	switch s {
	case "darwin":
		return "macOS"
	case "linux":
		return "Linux"
	case "windows":
		return "Windows"
	}
	return s
}

func runtimeLabel(r types.RuntimeKind) string {
	switch r {
	case types.RuntimePython:
		return "Python (.py)"
	case types.RuntimeNotebook:
		return "Notebook (.ipynb)"
	case types.RuntimeJava:
		return "Java (.java)"
	}
	return string(r)
}

func pluralNodes(n int) string {
	if n == 1 {
		return "1 machine"
	}
	return fmt.Sprintf("%d machines", n)
}

func joinMax(items []string, max int) string {
	if len(items) <= max {
		return joinComma(items)
	}
	return fmt.Sprintf("%s and %d more", joinComma(items[:max]), len(items)-max)
}

func joinComma(items []string) string {
	out := ""
	for i, s := range items {
		if i > 0 {
			out += ", "
		}
		out += s
	}
	return out
}
