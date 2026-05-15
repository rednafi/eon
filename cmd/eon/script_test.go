package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/charmbracelet/fang"
	"github.com/rogpeppe/go-internal/testscript"
)

// TestMain wires the eon binary as a testscript-callable command so
// the .txtar scripts in testdata/script can drive it like a real
// shell session — same code path as a real shell invocation, but
// in-process so we avoid the cost of forking.
func TestMain(m *testing.M) {
	testscript.Main(m, map[string]func(){
		"eon":     func() { os.Exit(runEonMain()) },
		"timeout": func() { os.Exit(runTimeoutMain()) },
	})
}

// runEonMain is the entrypoint testscript sees when a script runs
// `exec eon ...`. It mirrors cmd/eon/main.go exactly: same context,
// same Fang execute, same exit-code mapping.
func runEonMain() int {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	root := newRoot()
	if err := fang.Execute(ctx, root, fangOptions()...); err != nil {
		return exitCode(err)
	}
	return 0
}

func runTimeoutMain() int {
	if len(os.Args) < 3 {
		return 2
	}
	d, err := parseTimeoutDuration(os.Args[1])
	if err != nil {
		return 2
	}
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()

	cmd := exec.CommandContext(ctx, os.Args[2], os.Args[3:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err = cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return 124
	}
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return 1
}

func parseTimeoutDuration(s string) (time.Duration, error) {
	if n, err := strconv.Atoi(s); err == nil {
		return time.Duration(n) * time.Second, nil
	}
	return time.ParseDuration(s)
}

func TestScripts(t *testing.T) {
	testscript.Run(t, testscript.Params{
		Dir: "testdata/script",
		Setup: func(env *testscript.Env) error {
			// Each script gets its own HOME / XDG so the data
			// directory lives entirely inside $WORK and never
			// touches the user's real eon state.
			env.Setenv("HOME", env.WorkDir)
			env.Setenv("XDG_DATA_HOME", env.WorkDir+"/xdg")
			env.Setenv("XDG_CONFIG_HOME", env.WorkDir+"/xdg-config")
			// FANG_DISABLE_STYLES isn't strictly necessary (Fang
			// detects non-TTY), but pinning it makes the test
			// output byte-stable across terminal types.
			env.Setenv("CLICOLOR", "0")
			env.Setenv("NO_COLOR", "1")
			return nil
		},
	})
}
