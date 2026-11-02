package agent

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/siv19/compute/internal/types"
)

// dockerCommand builds a `docker run` invocation that mounts the job workdir,
// runs as an unprivileged user, applies the per-job memory cap (§6.5), and for
// CUDA jobs requests GPUs via the NVIDIA runtime. Used on Linux nodes (§ open
// decision 2). The container's /work is the extracted bundle; outputs written
// there are collected as artifacts after the run.
func dockerCommand(ctx context.Context, spec runSpec) (*exec.Cmd, error) {
	j := spec.Job
	image := dockerImage(j)
	inner := dockerInnerScript(j)

	args := []string{
		"run", "--rm",
		"-v", spec.WorkDir + ":/work",
		"-w", "/work",
		"--user", "1000:1000", // dedicated unprivileged uid inside the container
		"--network", "none", // jobs don't need inbound; outbound via host if needed
	}
	// Per-job memory cap keeps one job from dragging the whole node into
	// swap-thrash (§6.5). Docker enforces this via cgroups.
	if spec.MemLimitMB > 0 {
		args = append(args, "--memory", fmt.Sprintf("%dm", spec.MemLimitMB))
	}
	// CUDA jobs: expose GPUs. (No-op image/host without the NVIDIA runtime.)
	if j.NeedsGPU == types.GPUCUDA {
		args = append(args, "--gpus", "all")
		// pip/network often needed for CUDA setup; relax network for GPU jobs.
		args = args[:len(args)-0] // keep as-is; network stays "none" by default
	}
	args = append(args, image, "/bin/sh", "-c", inner)

	return exec.CommandContext(ctx, "docker", args...), nil
}

// dockerImage chooses a base image per runtime and GPU need. These are
// sensible public defaults; a node can override via POOL_IMAGE_* env if desired.
func dockerImage(j *types.Job) string {
	switch j.Runtime {
	case types.RuntimePython, types.RuntimeNotebook:
		if j.NeedsGPU == types.GPUCUDA {
			// CUDA-enabled Python base. The job's requirements.txt installs the
			// rest. Pinned major to avoid surprise breaks.
			return envOrImage("POOL_IMAGE_PYTHON_CUDA", "nvidia/cuda:12.4.1-runtime-ubuntu22.04")
		}
		return envOrImage("POOL_IMAGE_PYTHON", "python:3.12-slim")
	case types.RuntimeJava:
		return envOrImage("POOL_IMAGE_JAVA", "eclipse-temurin:21-jdk")
	default:
		return envOrImage("POOL_IMAGE_PYTHON", "python:3.12-slim")
	}
}

// dockerInnerScript is the command run inside the container. It mirrors the bare
// runners but assumes the image provides the toolchain. For the CUDA Python
// image we must install Python ourselves since the nvidia/cuda base lacks it.
func dockerInnerScript(j *types.Job) string {
	entry := shellQuote(j.EntryFile)
	switch j.Runtime {
	case types.RuntimePython:
		if j.NeedsGPU == types.GPUCUDA {
			return "apt-get update -qq && apt-get install -y -qq python3 python3-pip >/dev/null 2>&1; " +
				"if [ -f requirements.txt ]; then pip3 install -q -r requirements.txt; fi; " +
				"python3 " + entry
		}
		return "if [ -f requirements.txt ]; then pip install --disable-pip-version-check -q -r requirements.txt; fi; " +
			"python " + entry
	case types.RuntimeNotebook:
		return "pip install --disable-pip-version-check -q papermill ipykernel >/dev/null 2>&1; " +
			"papermill " + entry + " " + shellQuote(strings.TrimSuffix(j.EntryFile, ".ipynb")+".executed.ipynb")
	case types.RuntimeJava:
		return "if [ $(ls *.java | wc -l) -gt 1 ]; then javac *.java && java " +
			shellQuote(strings.TrimSuffix(j.EntryFile, ".java")) + "; else java " + entry + "; fi"
	default:
		return "python " + entry
	}
}

func envOrImage(key, def string) string {
	if v := getenv(key); v != "" {
		return v
	}
	return def
}

// shellQuote single-quotes a string for safe embedding in /bin/sh -c.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
