package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/charmbracelet/fang"
	"github.com/rednafi/eon"
	"github.com/spf13/cobra"
)

// runCmd executes the root command with the given argv. It returns
// stdout, stderr, and the error so callers can map it through
// exitCode for the contract a real shell would observe. data-dir is
// injected as the first flag so every invocation is hermetic.
func runCmd(t *testing.T, dir string, argv ...string) (stdoutS, stderrS string, err error) {
	t.Helper()

	// Reset persistent flag state between invocations — cobra parses
	// once but globalFlags persists across calls in-process.
	prev := globalFlags
	t.Cleanup(func() { globalFlags = prev })

	root := newRoot()
	var outBuf, errBuf bytes.Buffer
	root.SetOut(&outBuf)
	root.SetErr(&errBuf)
	root.SetArgs(append([]string{"--data-dir", dir, "--quiet"}, argv...))

	prevStderr := stderr
	stderr = &errBuf
	t.Cleanup(func() { stderr = prevStderr })

	err = root.ExecuteContext(t.Context())
	return outBuf.String(), errBuf.String(), err
}

func TestCLIAddListShowRm(t *testing.T) {
	dir := t.TempDir()

	out, _, err := runCmd(t, dir, "add", "--cron", "@hourly", "--name", "backup", "--", "echo", "hi")
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if !strings.Contains(out, "added job ") {
		t.Fatalf("add stdout = %q", out)
	}

	out, _, err = runCmd(t, dir, "ls", "--json")
	if err != nil {
		t.Fatalf("ls: %v", err)
	}
	var jobs []eon.Job
	if err := json.Unmarshal([]byte(out), &jobs); err != nil {
		t.Fatalf("ls --json: %v\n%s", err, out)
	}
	if len(jobs) != 1 || jobs[0].Name != "backup" || jobs[0].Cron != "@hourly" {
		t.Fatalf("ls --json returned %+v", jobs)
	}
	id := jobs[0].ID
	if len(id) != 5 {
		t.Fatalf("expected 5-char ID, got %q", id)
	}

	// show resolves by ID and by name.
	for _, ref := range []string{string(id), "backup"} {
		out, _, err = runCmd(t, dir, "show", ref, "--json")
		if err != nil {
			t.Fatalf("show %q: %v", ref, err)
		}
		var got eon.Job
		if err := json.Unmarshal([]byte(out), &got); err != nil {
			t.Fatalf("show %q --json: %v\n%s", ref, err, out)
		}
		if got.ID != id {
			t.Fatalf("show %q id = %q, want %q", ref, got.ID, id)
		}
	}

	out, _, err = runCmd(t, dir, "rm", string(id))
	if err != nil {
		t.Fatalf("rm: %v", err)
	}
	if !strings.Contains(out, "deleted job "+string(id)) {
		t.Fatalf("rm stdout = %q", out)
	}
}

func TestInjectedVersionWinsOverFangBuildInfo(t *testing.T) {
	prevVersion, prevCommit := version, commit
	version = "1.2.3"
	commit = ""
	t.Cleanup(func() {
		version = prevVersion
		commit = prevCommit
	})

	root := newRoot()
	var outBuf, errBuf bytes.Buffer
	root.SetOut(&outBuf)
	root.SetErr(&errBuf)
	root.SetArgs([]string{"--version"})

	if err := fang.Execute(t.Context(), root, fangOptions()...); err != nil {
		t.Fatalf("version: %v\nstderr: %s", err, errBuf.String())
	}
	if got := outBuf.String(); !strings.Contains(got, "eon version 1.2.3") {
		t.Fatalf("--version output = %q", got)
	}
}

func TestCLIExitCodesForBadInput(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name string
		argv []string
		want int
	}{
		{"add no args", []string{"add"}, 2},
		{"add invalid cron", []string{"add", "--cron", "garbage", "--", "echo"}, 5},
		{"add past time", []string{"add", "--at", "2020-01-01T00:00:00Z", "--", "echo"}, 5},
		{"add both schedules", []string{"add", "--cron", "@hourly", "--at", "+1h", "echo"}, 2},
		{"add no command", []string{"add", "--cron", "@hourly"}, 2},
		// "abcde" is a syntactically valid 5-char ID — lookup misses,
		// so we expect not-found (exit 3) rather than a usage error.
		// A non-5-char arg is treated as a name and also misses → 3.
		{"show unknown 5-char", []string{"show", "abcde"}, 3},
		{"show unknown name", []string{"show", "nope"}, 3},
		{"rm unknown 5-char", []string{"rm", "abcde"}, 3},
		{"enable unknown", []string{"enable", "abcde"}, 3},
		{"ls bad kind", []string{"ls", "--kind", "weird"}, 2},

		// Cobra-level usage errors must all land on exit 2.
		// Without tagUsageErrors these fell through to 1.
		{"unknown subcommand", []string{"foo"}, 2},
		{"unknown root flag", []string{"--badflag"}, 2},
		{"unknown sub flag", []string{"ls", "--badflag"}, 2},
		{"show no arg", []string{"show"}, 2},
		{"show extra arg", []string{"show", "abcde", "extra"}, 2},
		{"show shorthand parse", []string{"show", "-1"}, 2},
		{"rm no arg", []string{"rm"}, 2},
		{"rm extra arg", []string{"rm", "abcde", "extra"}, 2},
		{"enable no arg", []string{"enable"}, 2},
		{"disable no arg", []string{"disable"}, 2},
		{"logs no arg", []string{"logs"}, 2},
		{"logs bad duration", []string{"logs", "abcde", "--since", "garbage"}, 2},
		{"ls extra arg", []string{"ls", "extra"}, 2},
		{"status extra arg", []string{"status", "extra"}, 2},
		{"stop extra arg", []string{"stop", "extra"}, 2},
		// edit command was removed; treat as unknown subcommand.
		{"edit gone", []string{"edit", "abcde", "foo"}, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := runCmd(t, dir, tc.argv...)
			if got := exitCode(err); got != tc.want {
				t.Fatalf("exit = %d, want %d (err=%v)", got, tc.want, err)
			}
		})
	}
}

func TestCLIStatusJSON(t *testing.T) {
	dir := t.TempDir()
	out, _, err := runCmd(t, dir, "status", "--json")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	var s eon.Status
	if err := json.Unmarshal([]byte(out), &s); err != nil {
		t.Fatalf("decode: %v\n%s", err, out)
	}
	if s.Daemon.Running {
		t.Fatalf("daemon should not be running in fresh tempdir")
	}
	if s.DBPath == "" {
		t.Fatalf("DBPath missing")
	}
}

func TestCLIStopIdempotent(t *testing.T) {
	dir := t.TempDir()
	out, _, err := runCmd(t, dir, "stop")
	if err != nil {
		t.Fatalf("stop: %v", err)
	}
	if !strings.Contains(out, "no daemon running") {
		t.Fatalf("stop stdout = %q", out)
	}
}

func TestCLIEnableDisableRoundtrip(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := runCmd(t, dir, "add", "--cron", "@hourly", "--name", "toggle", "--", "echo", "hi"); err != nil {
		t.Fatalf("add: %v", err)
	}
	if _, _, err := runCmd(t, dir, "disable", "toggle"); err != nil {
		t.Fatalf("disable: %v", err)
	}
	out, _, _ := runCmd(t, dir, "show", "toggle", "--json")
	var got eon.Job
	_ = json.Unmarshal([]byte(out), &got)
	if got.Status != eon.StatusDisabled {
		t.Fatalf("after disable: status=%s", got.Status)
	}
	if _, _, err := runCmd(t, dir, "enable", "toggle"); err != nil {
		t.Fatalf("enable: %v", err)
	}
	out, _, _ = runCmd(t, dir, "show", "toggle", "--json")
	_ = json.Unmarshal([]byte(out), &got)
	if got.Status != eon.StatusEnabled {
		t.Fatalf("after enable: status=%s", got.Status)
	}
}

var _ = cobra.NoFileCompletions // keep cobra import even if unused otherwise
