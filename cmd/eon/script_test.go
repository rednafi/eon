package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"testing"

	"github.com/charmbracelet/fang"
	"github.com/rogpeppe/go-internal/testscript"
)

// TestMain wires the eon binary as a testscript-callable command so
// the .txtar scripts in testdata/script can drive it like a real
// shell session — same code path as a real shell invocation, but
// in-process so we avoid the cost of forking.
func TestMain(m *testing.M) {
	testscript.Main(m, map[string]func(){
		"eon": func() { os.Exit(runEonMain()) },
	})
}

// runEonMain is the entrypoint testscript sees when a script runs
// `exec eon ...`. It mirrors cmd/eon/main.go exactly: same context,
// same Fang execute, same exit-code mapping.
func runEonMain() int {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	root := newRoot()
	if err := fang.Execute(ctx, root, fang.WithColorSchemeFunc(fang.AnsiColorScheme)); err != nil {
		return exitCode(err)
	}
	return 0
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
