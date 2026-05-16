// Package tests holds native end-to-end coverage for the eon binary
// on each supported OS. Platform-specific files build a fresh binary,
// run it against a temp data directory, and assert user-visible behavior.
package tests

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// buildBinary cross-compiles eon for the host OS into a temp file.
//
// Per-run builds keep the test hermetic.
// The Go build cache keeps this cheap.
func buildBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "eon")
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/eon")
	cmd.Dir = repoRoot(t)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build eon: %v\n%s", err, out)
	}
	return bin
}

// repoRoot walks up from the test file's cwd until it finds go.mod.
func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for d := wd; d != string(filepath.Separator); d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
			return d
		}
	}
	t.Fatalf("go.mod not found above %s", wd)
	return ""
}

// runCmd executes eon with argv against the given data dir.
//
// It returns combined stdout and stderr plus exit code.
// Non-zero exit is not a test failure here.
// Callers decide what to assert.
func runCmd(t *testing.T, bin, dataDir string, argv ...string) (out string, code int) {
	t.Helper()
	args := append([]string{"--data-dir", dataDir, "--quiet"}, argv...)
	cmd := exec.Command(bin, args...)
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	err := cmd.Run()
	if exitErr, ok := err.(*exec.ExitError); ok {
		return buf.String(), exitErr.ExitCode()
	}
	if err != nil {
		t.Fatalf("exec eon %v: %v\n%s", argv, err, buf.String())
	}
	return buf.String(), 0
}

// mustRun is runCmd with the "exit 0" assertion baked in.
func mustRun(t *testing.T, bin, dataDir string, argv ...string) string {
	t.Helper()
	out, code := runCmd(t, bin, dataDir, argv...)
	if code != 0 {
		t.Fatalf("eon %v: exit %d\n%s", argv, code, out)
	}
	return out
}

// runE2E exercises the user-visible CLI surface on the current host. Both
// platform-gated test files call it so the shared E2E shape stays in one
// place and each OS file can layer on platform-specific assertions.
func runE2E(t *testing.T) {
	t.Helper()
	bin := buildBinary(t)
	dir := t.TempDir()

	t.Run("status_on_empty", func(t *testing.T) {
		out := mustRun(t, bin, dir, "status")
		if !strings.Contains(out, "stopped") {
			t.Fatalf("status didn't report stopped: %s", out)
		}
	})

	t.Run("add_then_ls", func(t *testing.T) {
		mustRun(t, bin, dir, "add", "--cron", "@hourly", "--name", "smoke", "--", "echo", "hi")
		out := mustRun(t, bin, dir, "ls", "--json")
		var jobs []map[string]any
		if err := json.Unmarshal([]byte(out), &jobs); err != nil {
			t.Fatalf("ls --json: %v\n%s", err, out)
		}
		if len(jobs) != 1 || jobs[0]["name"] != "smoke" {
			t.Fatalf("ls --json = %+v", jobs)
		}
	})

	t.Run("exit_codes", func(t *testing.T) {
		cases := []struct {
			name string
			argv []string
			want int
		}{
			{name: "usage_no_flag", argv: []string{"add"}, want: 2},
			{name: "not_found", argv: []string{"show", "nope0"}, want: 3},
			{name: "invalid_spec", argv: []string{"add", "--cron", "garbage", "--", "echo", "x"}, want: 5},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				_, code := runCmd(t, bin, dir, tc.argv...)
				if code != tc.want {
					t.Errorf("eon %v: exit %d, want %d", tc.argv, code, tc.want)
				}
			})
		}
	})

	t.Run("concurrent_adds", func(t *testing.T) {
		var wg sync.WaitGroup
		const N = 8
		for i := 1; i <= N; i++ {
			wg.Go(func() {
				mustRun(t, bin, dir,
					"add", "--cron", "@hourly", "--name", fmt.Sprintf("p%d", i),
					"--", "echo", fmt.Sprintf("%d", i))
			})
		}
		wg.Wait()
		out := mustRun(t, bin, dir, "ls", "--all", "--json")
		var jobs []map[string]any
		if err := json.Unmarshal([]byte(out), &jobs); err != nil {
			t.Fatalf("ls: %v", err)
		}
		got := 0
		for _, j := range jobs {
			if name, _ := j["name"].(string); strings.HasPrefix(name, "p") && len(name) <= 3 {
				got++
			}
		}
		if got != N {
			t.Fatalf("concurrent adds: %d landed, want %d", got, N)
		}
	})

	t.Run("daemon_lifecycle", func(t *testing.T) {
		// Schedule a ticker before launching so we can check logs.
		mustRun(t, bin, dir, "add", "--cron", "@every 1s", "--name", "ticker", "--", "echo", "tick")

		daemon := exec.Command(bin, "--data-dir", dir, "--quiet", "daemon")
		var dbuf bytes.Buffer
		daemon.Stdout, daemon.Stderr = &dbuf, &dbuf
		if err := daemon.Start(); err != nil {
			t.Fatalf("start daemon: %v", err)
		}
		t.Cleanup(func() {
			_, _ = runCmd(t, bin, dir, "stop")
			_ = daemon.Wait()
		})

		// Give it time to claim the lock.
		time.Sleep(2 * time.Second)

		out := mustRun(t, bin, dir, "status")
		if !strings.Contains(out, "running") {
			t.Fatalf("status didn't show running after start: %s\ndaemon log:\n%s", out, dbuf.String())
		}

		// Second daemon must exit 4 (conflict).
		_, code := runCmd(t, bin, dir, "daemon")
		if code != 4 {
			t.Errorf("second daemon: exit %d, want 4", code)
		}

		// Let the ticker fire a few times.
		time.Sleep(3 * time.Second)

		logs := mustRun(t, bin, dir, "logs", "ticker")
		if !strings.Contains(logs, "tick") {
			t.Fatalf("logs missing tick: %q", logs)
		}
	})
}

// requireGOOS skips the test when run on the wrong platform. Belt and
// braces alongside the go:build tag protects callers who execute helpers
// outside a normal `go test` run.
func requireGOOS(t *testing.T, want string) {
	t.Helper()
	if runtime.GOOS != want {
		t.Skipf("test requires GOOS=%s, have %s", want, runtime.GOOS)
	}
}
