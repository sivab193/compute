// Package hostinfo collects a node's live capacity: CPU count + load, GPU
// (model/VRAM/type), RAM total/free, disk total/free, swap total/free, and the
// labels the scheduler filters on (arch, os, gpu-type, preemptible, location).
//
// It is intentionally dependency-light: it reads /proc and /sys on Linux and
// shells out to sysctl / vm_stat / nvidia-smi where that is the only portable
// source. Every probe degrades gracefully to zero/none rather than failing the
// whole heartbeat.
package hostinfo

import (
	"bufio"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"

	"github.com/siv19/compute/internal/types"
)

// Collect gathers a full heartbeat snapshot. nodeName and extra labels (e.g.
// preemptible, location) supplied by the operator are merged in.
func Collect(nodeName string, extraLabels map[string]string, agentVersion string) types.Heartbeat {
	hb := types.Heartbeat{
		NodeName:     nodeName,
		CPUCount:     runtime.NumCPU(),
		AgentVersion: agentVersion,
		Labels:       map[string]string{},
	}

	hb.LoadAvg1 = loadAvg1()
	hb.RAMTotalMB, hb.RAMFreeMB = memInfo()
	hb.SwapTotalMB, hb.SwapFreeMB = swapInfo()
	hb.DiskTotalMB, hb.DiskFreeMB = diskInfo(workDirRoot())
	hb.GPUs = detectGPUs()
	hb.DockerAvailable = dockerAvailable()
	hb.Toolchains = detectToolchains()

	// Base labels the scheduler relies on.
	hb.Labels["arch"] = runtime.GOARCH
	hb.Labels["os"] = runtime.GOOS
	hb.Labels["gpu-type"] = string(primaryGPUType(hb.GPUs))
	if _, ok := extraLabels["preemptible"]; !ok {
		hb.Labels["preemptible"] = "false"
	}
	for k, v := range extraLabels {
		hb.Labels[k] = v
	}
	return hb
}

func workDirRoot() string {
	if d := os.Getenv("POOL_WORKDIR"); d != "" {
		return d
	}
	return os.TempDir()
}

// primaryGPUType reduces a node's GPUs to a single scheduler label.
func primaryGPUType(gpus []types.GPUInfo) types.GPUType {
	for _, g := range gpus {
		if g.Type == types.GPUCUDA {
			return types.GPUCUDA
		}
	}
	for _, g := range gpus {
		if g.Type == types.GPUMetal {
			return types.GPUMetal
		}
	}
	return types.GPUNone
}

// ---- Load average ---------------------------------------------------------

func loadAvg1() float64 {
	switch runtime.GOOS {
	case "linux":
		b, err := os.ReadFile("/proc/loadavg")
		if err != nil {
			return 0
		}
		fields := strings.Fields(string(b))
		if len(fields) > 0 {
			v, _ := strconv.ParseFloat(fields[0], 64)
			return v
		}
	case "darwin":
		out, err := exec.Command("sysctl", "-n", "vm.loadavg").Output()
		if err != nil {
			return 0
		}
		// format: { 1.23 4.56 7.89 }
		s := strings.Trim(strings.TrimSpace(string(out)), "{} ")
		fields := strings.Fields(s)
		if len(fields) > 0 {
			v, _ := strconv.ParseFloat(fields[0], 64)
			return v
		}
	}
	return 0
}

// ---- Memory ---------------------------------------------------------------

func memInfo() (totalMB, freeMB uint64) {
	switch runtime.GOOS {
	case "linux":
		return linuxMemInfo()
	case "darwin":
		return darwinMemInfo()
	}
	return 0, 0
}

func linuxMemInfo() (totalMB, freeMB uint64) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, 0
	}
	defer f.Close()
	var totalKB, availKB uint64
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "MemTotal:"):
			totalKB = parseMeminfoKB(line)
		case strings.HasPrefix(line, "MemAvailable:"):
			availKB = parseMeminfoKB(line)
		}
	}
	return totalKB / 1024, availKB / 1024
}

func parseMeminfoKB(line string) uint64 {
	fields := strings.Fields(line)
	if len(fields) >= 2 {
		v, _ := strconv.ParseUint(fields[1], 10, 64)
		return v
	}
	return 0
}

func darwinMemInfo() (totalMB, freeMB uint64) {
	out, err := exec.Command("sysctl", "-n", "hw.memsize").Output()
	if err == nil {
		b, _ := strconv.ParseUint(strings.TrimSpace(string(out)), 10, 64)
		totalMB = b / 1024 / 1024
	}
	// Free memory on macOS: (free + inactive + speculative) pages * pagesize.
	pageSize := uint64(16384) // Apple Silicon default; corrected below if possible
	if o, err := exec.Command("sysctl", "-n", "hw.pagesize").Output(); err == nil {
		if ps, e := strconv.ParseUint(strings.TrimSpace(string(o)), 10, 64); e == nil && ps > 0 {
			pageSize = ps
		}
	}
	if o, err := exec.Command("vm_stat").Output(); err == nil {
		var freePages, inactivePages, specPages uint64
		sc := bufio.NewScanner(strings.NewReader(string(o)))
		for sc.Scan() {
			line := sc.Text()
			switch {
			case strings.HasPrefix(line, "Pages free:"):
				freePages = parseVMStatPages(line)
			case strings.HasPrefix(line, "Pages inactive:"):
				inactivePages = parseVMStatPages(line)
			case strings.HasPrefix(line, "Pages speculative:"):
				specPages = parseVMStatPages(line)
			}
		}
		freeMB = (freePages + inactivePages + specPages) * pageSize / 1024 / 1024
	}
	return totalMB, freeMB
}

func parseVMStatPages(line string) uint64 {
	idx := strings.LastIndex(line, ":")
	if idx < 0 {
		return 0
	}
	v := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(line[idx+1:]), "."))
	n, _ := strconv.ParseUint(v, 10, 64)
	return n
}

// ---- Swap -----------------------------------------------------------------

func swapInfo() (totalMB, freeMB uint64) {
	switch runtime.GOOS {
	case "linux":
		f, err := os.Open("/proc/meminfo")
		if err != nil {
			return 0, 0
		}
		defer f.Close()
		var totalKB, freeKB uint64
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			line := sc.Text()
			switch {
			case strings.HasPrefix(line, "SwapTotal:"):
				totalKB = parseMeminfoKB(line)
			case strings.HasPrefix(line, "SwapFree:"):
				freeKB = parseMeminfoKB(line)
			}
		}
		return totalKB / 1024, freeKB / 1024
	case "darwin":
		// sysctl vm.swapusage -> "total = 2048.00M  used = 123.00M  free = 1925.00M"
		out, err := exec.Command("sysctl", "-n", "vm.swapusage").Output()
		if err != nil {
			return 0, 0
		}
		fields := strings.Fields(string(out))
		for i, f := range fields {
			if f == "total" && i+2 < len(fields) {
				totalMB = parseMacSwapMB(fields[i+2])
			}
			if f == "free" && i+2 < len(fields) {
				freeMB = parseMacSwapMB(fields[i+2])
			}
		}
		return totalMB, freeMB
	}
	return 0, 0
}

func parseMacSwapMB(s string) uint64 {
	s = strings.TrimSpace(s)
	mult := 1.0
	switch {
	case strings.HasSuffix(s, "G"):
		mult, s = 1024, strings.TrimSuffix(s, "G")
	case strings.HasSuffix(s, "M"):
		mult, s = 1, strings.TrimSuffix(s, "M")
	case strings.HasSuffix(s, "K"):
		mult, s = 1.0/1024, strings.TrimSuffix(s, "K")
	}
	v, _ := strconv.ParseFloat(s, 64)
	return uint64(v * mult)
}

// ---- Disk -----------------------------------------------------------------
// diskInfo is implemented per-platform (disk_unix.go / disk_windows.go).

// ---- GPU ------------------------------------------------------------------

func detectGPUs() []types.GPUInfo {
	// CUDA: nvidia-smi, on Linux and Windows (and WSL2).
	if gpus := detectNvidia(); len(gpus) > 0 {
		return gpus
	}
	// Metal: Apple Silicon. We report a single Metal device; VRAM is unified
	// memory, so we do not claim a separate VRAM figure (left 0 to avoid
	// double-counting against RAM in the dashboard).
	if runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" {
		model := "Apple Silicon GPU"
		if o, err := exec.Command("sysctl", "-n", "machdep.cpu.brand_string").Output(); err == nil {
			model = strings.TrimSpace(string(o)) + " (Metal)"
		}
		return []types.GPUInfo{{Model: model, VRAMMB: 0, Type: types.GPUMetal}}
	}
	return nil
}

func detectNvidia() []types.GPUInfo {
	path, err := exec.LookPath("nvidia-smi")
	if err != nil {
		return nil
	}
	out, err := exec.Command(path, "--query-gpu=name,memory.total",
		"--format=csv,noheader,nounits").Output()
	if err != nil {
		return nil
	}
	var gpus []types.GPUInfo
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		parts := strings.Split(line, ",")
		if len(parts) < 2 {
			continue
		}
		vram, _ := strconv.ParseUint(strings.TrimSpace(parts[1]), 10, 64)
		gpus = append(gpus, types.GPUInfo{
			Model:  strings.TrimSpace(parts[0]),
			VRAMMB: vram,
			Type:   types.GPUCUDA,
		})
	}
	return gpus
}

// ---- Docker ---------------------------------------------------------------

func dockerAvailable() bool {
	path, err := exec.LookPath("docker")
	if err != nil {
		return false
	}
	// `docker info` fails fast if the daemon is not reachable.
	return exec.Command(path, "info", "--format", "{{.ServerVersion}}").Run() == nil
}

// ---- Toolchains -----------------------------------------------------------

// detectToolchains probes which job runtimes this machine can actually execute.
// The control plane unions these across online nodes to build the submit form,
// so a claim here must mean the job will really run.
func detectToolchains() map[string]bool {
	t := map[string]bool{}

	// Python: either python3 or python on PATH.
	t[types.ToolPython] = lookPathAny("python3", "python")

	// Notebook: papermill (preferred) or jupyter for nbconvert.
	t[types.ToolNotebook] = lookPathAny("papermill", "jupyter")

	// Java: a working compiler is the universal path. On macOS /usr/bin/javac
	// is a stub that exists even with no JDK installed, so presence on PATH is
	// not enough — probe that it actually runs.
	t[types.ToolJava] = javaUsable()

	// Docker is tracked separately too, but include it so one map describes the
	// node's execution surface.
	t[types.ToolDocker] = dockerAvailable()

	return t
}

func lookPathAny(names ...string) bool {
	for _, n := range names {
		if _, err := exec.LookPath(n); err == nil {
			return true
		}
	}
	return false
}

// javaUsable reports whether this node can compile-and-run or source-launch
// Java. It runs the tool rather than trusting PATH, because macOS ships stubs
// that fail at exec time.
func javaUsable() bool {
	if _, err := exec.LookPath("javac"); err == nil {
		if exec.Command("javac", "-version").Run() == nil {
			return true
		}
	}
	// No compiler: a JDK 11+ runtime can still source-launch a single file.
	if _, err := exec.LookPath("java"); err == nil {
		out, err := exec.Command("java", "-version").CombinedOutput()
		if err != nil {
			return false
		}
		// Reject 1.8-era runtimes, which cannot run `java Foo.java`.
		return !strings.Contains(string(out), `"1.8`) &&
			!strings.Contains(string(out), `"1.7`)
	}
	return false
}
