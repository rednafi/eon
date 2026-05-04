// Package tests holds end-to-end integration tests that exercise the cron,
// source, and cli packages through their public APIs only. They are kept out
// of the production packages so unit tests stay fast and focused, and so
// real-cron mutations (which need EON_RUN_REAL_CRON=1) sit in one obvious
// place.
package tests

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/rednafi/eon/cli"
	"github.com/rednafi/eon/cron"
)

// runCLI drives the cobra root with captured stdin/stdout. It exists in the
// shared helpers file so every integration test takes the same code path
// users hit when they invoke the binary.
func runCLI(t *testing.T, mgr *cron.Manager, argv []string, stdin io.Reader, stdout, stderr io.Writer) error {
	t.Helper()
	root := cli.BuildRoot(mgr)
	root.SetArgs(argv)
	if stdin != nil {
		root.SetIn(stdin)
	}
	root.SetOut(stdout)
	root.SetErr(stderr)
	return root.ExecuteContext(context.Background())
}

func mustOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
}

// captureCLI is a convenience wrapper that allocates a single bytes.Buffer
// for stdout+stderr and returns its contents alongside any error.
func captureCLI(t *testing.T, mgr *cron.Manager, argv ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	err := runCLI(t, mgr, argv, nil, &out, &out)
	return out.String(), err
}
