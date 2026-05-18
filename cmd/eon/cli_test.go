package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/charmbracelet/fang"
	"github.com/google/go-cmp/cmp"
	"github.com/rednafi/eon"
	"github.com/spf13/cobra"
)

// runCmd executes the root command with the given argv. It returns
// stdout, stderr, and the error so callers can map it through
// exitCode for the contract a real shell would observe. data-dir is
// injected as the first flag so every invocation is hermetic.
func runCmd(t *testing.T, dir string, argv ...string) (stdoutS, stderrS string, err error) {
	t.Helper()

	// Reset persistent flag state between invocations.
	// Cobra parses once, but globalFlags persists in-process.
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
		t.Errorf("add stdout = %q, want substring %q", out, "added job ")
	}

	out, _, err = runCmd(t, dir, "ls", "--json")
	if err != nil {
		t.Fatalf("ls: %v", err)
	}
	var jobs []eon.Job
	if err := json.Unmarshal([]byte(out), &jobs); err != nil {
		t.Fatalf("ls --json: %v\n%s", err, out)
	}
	wantRows := []cliJobRow{{
		Name:    "backup",
		Kind:    eon.KindCron,
		Command: []string{"echo", "hi"},
		Cron:    "@hourly",
		Status:  eon.StatusEnabled,
	}}
	if diff := cmp.Diff(wantRows, cliJobRows(jobs)); diff != "" {
		t.Errorf("ls --json rows mismatch (-want +got):\n%s", diff)
	}
	if len(jobs) == 0 {
		t.Fatalf("ls --json returned no jobs")
	}
	id := jobs[0].ID
	if len(id) != 5 {
		t.Fatalf("len(job ID) = %d, want 5; id=%q", len(id), id)
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
			t.Errorf("show %q id = %q, want %q", ref, got.ID, id)
		}
	}

	out, _, err = runCmd(t, dir, "rm", string(id))
	if err != nil {
		t.Fatalf("rm: %v", err)
	}
	if !strings.Contains(out, "deleted job "+string(id)) {
		t.Errorf("rm stdout = %q, want substring %q", out, "deleted job "+string(id))
	}
}

type cliJobRow struct {
	Name    string
	Kind    eon.JobKind
	Command []string
	Cron    string
	Status  eon.JobStatus
}

func cliJobRows(jobs []eon.Job) []cliJobRow {
	rows := make([]cliJobRow, 0, len(jobs))
	for _, job := range jobs {
		rows = append(rows, cliJobRow{
			Name:    job.Name,
			Kind:    job.Kind,
			Command: job.Command,
			Cron:    job.Cron,
			Status:  job.Status,
		})
	}
	return rows
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
		{name: "add_no_args", argv: []string{"add"}, want: 2},
		{name: "add_invalid_cron", argv: []string{"add", "--cron", "garbage", "--", "echo"}, want: 5},
		{name: "add_past_time", argv: []string{"add", "--at", "2020-01-01T00:00:00Z", "--", "echo"}, want: 5},
		{name: "add_both_schedules", argv: []string{"add", "--cron", "@hourly", "--at", "+1h", "echo"}, want: 2},
		{name: "add_no_command", argv: []string{"add", "--cron", "@hourly"}, want: 2},
		// "abcde" is syntactically a valid 5-char ID.
		// Lookup misses, so this should be not-found.
		// It should not be a usage error.
		// A non-5-char arg is treated as a name and also misses -> 3.
		{name: "show_unknown_id", argv: []string{"show", "abcde"}, want: 3},
		{name: "show_unknown_name", argv: []string{"show", "nope"}, want: 3},
		{name: "rm_unknown_id", argv: []string{"rm", "abcde"}, want: 3},
		{name: "enable_unknown", argv: []string{"enable", "abcde"}, want: 3},
		{name: "ls_bad_kind", argv: []string{"ls", "--kind", "weird"}, want: 2},

		// Cobra-level usage errors must all land on exit 2.
		// Without tagUsageErrors these fell through to 1.
		{name: "unknown_subcommand", argv: []string{"foo"}, want: 2},
		{name: "unknown_root_flag", argv: []string{"--badflag"}, want: 2},
		{name: "unknown_sub_flag", argv: []string{"ls", "--badflag"}, want: 2},
		{name: "show_no_arg", argv: []string{"show"}, want: 2},
		{name: "show_extra_arg", argv: []string{"show", "abcde", "extra"}, want: 2},
		{name: "show_shorthand_parse", argv: []string{"show", "-1"}, want: 2},
		{name: "rm_no_arg", argv: []string{"rm"}, want: 2},
		{name: "rm_extra_arg", argv: []string{"rm", "abcde", "extra"}, want: 2},
		{name: "enable_no_arg", argv: []string{"enable"}, want: 2},
		{name: "disable_no_arg", argv: []string{"disable"}, want: 2},
		{name: "logs_no_arg", argv: []string{"logs"}, want: 2},
		{name: "logs_bad_duration", argv: []string{"logs", "abcde", "--since", "garbage"}, want: 2},
		{name: "ls_extra_arg", argv: []string{"ls", "extra"}, want: 2},
		{name: "status_extra_arg", argv: []string{"status", "extra"}, want: 2},
		{name: "stop_extra_arg", argv: []string{"stop", "extra"}, want: 2},
		// edit command was removed.
		// Treat it as an unknown subcommand.
		{name: "edit_gone", argv: []string{"edit", "abcde", "foo"}, want: 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := runCmd(t, dir, tc.argv...)
			if got := exitCode(err); got != tc.want {
				t.Errorf("exit = %d, want %d (err=%v)", got, tc.want, err)
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
		t.Errorf("daemon running = true, want false")
	}
	if s.DBPath == "" {
		t.Errorf("DBPath = %q, want non-empty", s.DBPath)
	}
}

func TestCLIStopIdempotent(t *testing.T) {
	dir := t.TempDir()
	out, _, err := runCmd(t, dir, "stop")
	if err != nil {
		t.Fatalf("stop: %v", err)
	}
	if !strings.Contains(out, "no daemon running") {
		t.Errorf("stop stdout = %q, want substring %q", out, "no daemon running")
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
	out, _, err := runCmd(t, dir, "show", "toggle", "--json")
	if err != nil {
		t.Fatalf("show after disable: %v", err)
	}
	var got eon.Job
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode show after disable: %v", err)
	}
	if got.Status != eon.StatusDisabled {
		t.Errorf("after disable: status = %s, want %s", got.Status, eon.StatusDisabled)
	}
	if _, _, err := runCmd(t, dir, "enable", "toggle"); err != nil {
		t.Fatalf("enable: %v", err)
	}
	out, _, err = runCmd(t, dir, "show", "toggle", "--json")
	if err != nil {
		t.Fatalf("show after enable: %v", err)
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode show after enable: %v", err)
	}
	if got.Status != eon.StatusEnabled {
		t.Errorf("after enable: status = %s, want %s", got.Status, eon.StatusEnabled)
	}
}

var _ = cobra.NoFileCompletions // keep cobra import even if unused otherwise
