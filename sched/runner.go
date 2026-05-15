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

	"github.com/rednafi/eon"
)

// Runner executes a single job. Decoupling exec from the scheduler
// lets tests substitute a fake that records invocations without
// forking processes.
type Runner interface {
	Run(ctx context.Context, job eon.Job, out io.Writer) (exitCode int, err error)
}

// ExecRunner is the production [Runner]: shells out via os/exec and
// streams merged stdout+stderr into out.
type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, job eon.Job, out io.Writer) (int, error) {
	if len(job.Command) == 0 {
		return -1, errors.New("scheduler: empty command")
	}
	cmd := exec.CommandContext(ctx, job.Command[0], job.Command[1:]...)
	cmd.Stdout = out
	cmd.Stderr = out
	if len(job.Env) > 0 {
		// Use the snapshot from `eon add` time so the child sees the
		// user's PATH (and the rest of their shell env), bypassing the
		// minimal env that launchd/systemd hands the daemon.
		cmd.Env = job.Env
		// CommandContext already resolved cmd.Path against the
		// daemon's PATH at construction time; redo the lookup against
		// the snapshot's PATH so we exec the binary the user expects.
		if resolved, err := lookPathIn(job.Command[0], envPath(job.Env)); err == nil {
			cmd.Path = resolved
			cmd.Err = nil
		}
	}
	if err := cmd.Run(); err != nil {
		if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
			// Non-zero exits are job outcomes, not scheduler errors.
			return exitErr.ExitCode(), nil
		}
		// Failure to *start* (binary missing, permission denied, …).
		// The user needs to be able to find out why from `eon logs`,
		// so persist the reason alongside any prior output.
		_, _ = fmt.Fprintf(out, "eon: failed to start: %v\n", err)
		return -1, err
	}
	return 0, nil
}

// envPath returns the PATH value from a `KEY=VALUE` slice, or "" if
// PATH isn't present. The first match wins, which matches POSIX
// "earlier definitions take precedence" behaviour for environments
// passed to exec.
func envPath(env []string) string {
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, "PATH="); ok {
			return v
		}
	}
	return ""
}

// lookPathIn is exec.LookPath with the search path supplied
// explicitly instead of read from os.Getenv("PATH"). Needed because
// the daemon's own PATH is the launchd/systemd minimal one, but jobs
// run with whatever PATH was snapshotted at `eon add` time.
func lookPathIn(name, path string) (string, error) {
	if strings.ContainsRune(name, os.PathSeparator) {
		// Already a path; let the OS resolve it at exec time.
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
