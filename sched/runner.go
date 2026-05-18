package sched

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/rednafi/eon"
)

// Runner executes a single job. Decoupling exec from the scheduler
// lets tests substitute a fake that records invocations without
// forking processes.
type Runner interface {
	Run(ctx context.Context, job eon.Job, out io.Writer) (exitCode int, err error)
}

// ExecRunner is the production [Runner].
//
// Behavior:
//   - It shells out via os/exec.
//   - It streams merged stdout and stderr into out.
//   - GracePeriod > 0 sends SIGTERM before Go escalates to SIGKILL.
//   - GracePeriod == 0 uses Go's default immediate SIGKILL.
type ExecRunner struct {
	GracePeriod time.Duration
}

// Run executes job and writes merged stdout and stderr to out.
func (e ExecRunner) Run(ctx context.Context, job eon.Job, out io.Writer) (int, error) {
	if len(job.Command) == 0 {
		return -1, errors.New("scheduler: empty command")
	}
	cmd := exec.CommandContext(ctx, job.Command[0], job.Command[1:]...)
	cmd.Stdout = out
	cmd.Stderr = out
	if e.GracePeriod > 0 {
		// Send SIGTERM on cancellation so the child can run cleanup.
		//
		// WaitDelay is still required.
		// A child can trap SIGTERM and never exit.
		// In that case WaitDelay lets os/exec force-kill it.
		cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
		cmd.WaitDelay = e.GracePeriod
	}
	if len(job.Env) > 0 {
		cmd.Env = job.Env
		// Use the env snapshot from `eon add` time.
		//
		// This gives the child the user's PATH and shell variables.
		// It avoids the minimal env from launchd or systemd.
		//
		// CommandContext already resolved cmd.Path once.
		// Redo the lookup against the snapshot PATH.
		// That execs the binary the user expected at `eon add` time.
		if resolved, err := lookPathIn(job.Command[0], envPath(job.Env)); err == nil {
			cmd.Path = resolved
			cmd.Err = nil
		} else if !strings.ContainsRune(job.Command[0], os.PathSeparator) {
			cmd.Path = job.Command[0]
			cmd.Err = err
		}
	}
	err := cmd.Run()
	if cmd.ProcessState != nil {
		// The child actually ran.
		//
		// ProcessState has the useful exit code.
		// A handled SIGTERM can still surface as a context error.
		return cmd.ProcessState.ExitCode(), nil
	}
	// The process never started.
	//
	// Persist the reason so `eon logs` can show it.
	if _, writeErr := fmt.Fprintf(out, "eon: failed to start: %v\n", err); writeErr != nil {
		return -1, errors.Join(err, writeErr)
	}
	return -1, err
}

// envPath returns PATH from a `KEY=VALUE` slice.
//
// Behavior:
//   - It returns "" when PATH is absent.
//   - The first match wins.
//
// The first-match rule matches environments passed to exec.
func envPath(env []string) string {
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, "PATH="); ok {
			return v
		}
	}
	return ""
}

// lookPathIn is exec.LookPath with an explicit search path.
//
// Needed because:
//   - The daemon's PATH can come from launchd or systemd.
//   - Jobs should use the PATH snapshotted at `eon add` time.
func lookPathIn(name, path string) (string, error) {
	if strings.ContainsRune(name, os.PathSeparator) {
		// Already a path.
		// Let the OS resolve it at exec time.
		return name, nil
	}
	if path == "" {
		return "", exec.ErrNotFound
	}
	for dir := range strings.SplitSeq(path, string(os.PathListSeparator)) {
		if dir == "" {
			dir = "."
		}
		full := filepath.Join(dir, name)
		if info, err := os.Stat(full); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			return full, nil
		}
	}
	return "", exec.ErrNotFound
}
