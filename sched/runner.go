package sched

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"

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
