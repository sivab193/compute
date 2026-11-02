package agent

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/siv19/compute/internal/types"
)

// logSink receives log lines as a job runs. The agent implementation batches
// and ships these to the control plane.
type logSink func(stream, text string)

// runResult is the outcome of executing a job locally.
type runResult struct {
	ExitCode int
	Err      error // non-nil for agent-side failures (couldn't start, timeout)
}

// runSpec is everything a runner needs to execute one job.
type runSpec struct {
	Job        *types.Job
	WorkDir    string // bundle extracted here; also the artifact collection root
	UseDocker  bool
	MemLimitMB uint64 // per-job memory cap (§6.5), 0 = none
	Log        logSink
}

// execute dispatches to the right runner based on the job's runtime, honoring
// the timeout. Bare execution on macOS, Docker on Linux where available (§ open
// decision 2).
func execute(ctx context.Context, spec runSpec) runResult {
	timeout := time.Duration(spec.Job.TimeoutSec) * time.Second
	if timeout <= 0 {
		timeout = time.Hour
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var cmd *exec.Cmd
	var err error
	if spec.UseDocker {
		cmd, err = dockerCommand(runCtx, spec)
	} else {
		cmd, err = bareCommand(runCtx, spec)
	}
	if err != nil {
		spec.Log("system", "failed to build command: "+err.Error())
		return runResult{ExitCode: -1, Err: err}
	}

	spec.Log("system", "exec: "+strings.Join(cmd.Args, " "))
	return streamRun(runCtx, cmd, spec.Log)
}

// bareCommand builds the host-process command for a runtime (macOS / no-Docker).
func bareCommand(ctx context.Context, spec runSpec) (*exec.Cmd, error) {
	j := spec.Job
	entry := j.EntryFile
	switch j.Runtime {
	case types.RuntimePython:
		return pythonBareCommand(ctx, spec, entry)
	case types.RuntimeNotebook:
		return notebookCommand(ctx, spec, entry, false)
	case types.RuntimeJava:
		return javaCommand(ctx, spec, entry, false)
	default:
		return nil, fmt.Errorf("unknown runtime %q", j.Runtime)
	}
}

// pythonBareCommand runs `python file.py`. If a requirements.txt is present in
// the bundle, it first creates a venv and installs into it (§4 table).
func pythonBareCommand(ctx context.Context, spec runSpec, entry string) (*exec.Cmd, error) {
	py := pythonExe()
	reqs := filepath.Join(spec.WorkDir, "requirements.txt")
	if _, err := os.Stat(reqs); err == nil {
		// Build a venv + install synchronously via a shell wrapper so logs stream.
		venv := filepath.Join(spec.WorkDir, ".venv")
		venvPy := filepath.Join(venv, "bin", "python")
		if runtime.GOOS == "windows" {
			venvPy = filepath.Join(venv, "Scripts", "python.exe")
		}
		script := fmt.Sprintf(
			`%s -m venv %q && %q -m pip install --disable-pip-version-check -q -r %q && %q %q`,
			py, venv, venvPy, reqs, venvPy, entry)
		c := exec.CommandContext(ctx, shell(), shellArg(), script)
		c.Dir = spec.WorkDir
		return c, nil
	}
	c := exec.CommandContext(ctx, py, entry)
	c.Dir = spec.WorkDir
	return c, nil
}

// notebookCommand executes an .ipynb headlessly. Prefers papermill (writes an
// executed notebook, parameterizable); falls back to jupyter nbconvert (§4).
func notebookCommand(ctx context.Context, spec runSpec, entry string, inDocker bool) (*exec.Cmd, error) {
	base := strings.TrimSuffix(entry, filepath.Ext(entry))
	outNb := base + ".executed.ipynb"
	var script string
	if hasExecutable("papermill") {
		script = fmt.Sprintf(`papermill %q %q`, entry, outNb)
	} else {
		script = fmt.Sprintf(
			`jupyter nbconvert --to notebook --execute --output %q %q`,
			filepath.Base(outNb), entry)
	}
	c := exec.CommandContext(ctx, shell(), shellArg(), script)
	c.Dir = spec.WorkDir
	return c, nil
}

// javaCommand runs Java source. On JDK 11+ a single file can use source-launch
// (`java Foo.java`), but compile-then-run (`javac` + `java`) works on every JDK
// from 8 up and for multi-file jobs, so we use it as the universal path and
// only fall back to source-launch when javac is unavailable (e.g. a JRE-only
// node).
func javaCommand(ctx context.Context, spec runSpec, entry string, inDocker bool) (*exec.Cmd, error) {
	class := strings.TrimSuffix(filepath.Base(entry), ".java")
	var script string
	if hasExecutable("javac") {
		script = fmt.Sprintf(`javac *.java && java %s`, shellQuoteRunner(class))
	} else {
		// No compiler: single-file source-launch (requires JDK 11+ runtime).
		script = fmt.Sprintf(`java %s`, shellQuoteRunner(entry))
	}
	c := exec.CommandContext(ctx, shell(), shellArg(), script)
	c.Dir = spec.WorkDir
	return c, nil
}

// shellQuoteRunner single-quotes for /bin/sh; on Windows cmd we leave as-is
// since class/file names here are simple identifiers.
func shellQuoteRunner(s string) string {
	if runtime.GOOS == "windows" {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// streamRun starts cmd, pumps stdout/stderr to the sink line by line, and
// returns the exit code. A timeout (ctx deadline) surfaces as a non-zero exit.
func streamRun(ctx context.Context, cmd *exec.Cmd, log logSink) runResult {
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return runResult{ExitCode: -1, Err: err}
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return runResult{ExitCode: -1, Err: err}
	}
	if err := cmd.Start(); err != nil {
		log("system", "start failed: "+err.Error())
		return runResult{ExitCode: -1, Err: err}
	}

	done := make(chan struct{}, 2)
	pump := func(r *bufio.Scanner, stream string) {
		r.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for r.Scan() {
			log(stream, r.Text())
		}
		done <- struct{}{}
	}
	go pump(bufio.NewScanner(stdout), "stdout")
	go pump(bufio.NewScanner(stderr), "stderr")
	<-done
	<-done

	err = cmd.Wait()
	if ctx.Err() == context.DeadlineExceeded {
		log("system", "job timed out and was terminated")
		return runResult{ExitCode: 124, Err: ctx.Err()}
	}
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return runResult{ExitCode: ee.ExitCode()}
		}
		return runResult{ExitCode: -1, Err: err}
	}
	return runResult{ExitCode: 0}
}

// ---- environment helpers --------------------------------------------------

func pythonExe() string {
	for _, c := range []string{"python3", "python"} {
		if hasExecutable(c) {
			return c
		}
	}
	return "python3"
}

func hasExecutable(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func shell() string {
	if runtime.GOOS == "windows" {
		return "cmd"
	}
	return "/bin/sh"
}

func shellArg() string {
	if runtime.GOOS == "windows" {
		return "/c"
	}
	return "-c"
}
