package crontab

import (
	"context"
	"errors"
	"strings"
	"testing"

	"slices"

	"github.com/rednafi/eon/cron"
)

// fakeCrontab returns a CrontabRunner that pretends a fixed crontab exists,
// records the args of each call, and captures any stdin written via `crontab -`.
type fakeCrontab struct {
	content string
	calls   [][]string
	stdin   []string
}

func (f *fakeCrontab) run(_ context.Context, args []string, stdin string) ([]byte, error) {
	f.calls = append(f.calls, slices.Clone(args))
	f.stdin = append(f.stdin, stdin)
	switch {
	case len(args) == 1 && args[0] == "-l":
		if f.content == "" {
			return []byte("no crontab for tester"), nil
		}
		return []byte(f.content), nil
	case len(args) == 1 && args[0] == "-r":
		f.content = ""
		return nil, nil
	case len(args) == 1 && args[0] == "-":
		f.content = stdin
		return nil, nil
	}
	return nil, nil
}

func TestCrontabListSkipsCommentsAndBlank(t *testing.T) {
	f := &fakeCrontab{content: `
# top comment
*/5 * * * * /usr/bin/echo hi

@daily /usr/local/bin/backup.sh
0 9 * * 1 /opt/foo/run --quiet
`}
	c := New()
	c.Runner = f.run
	jobs, err := c.List(t.Context())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(jobs) != 3 {
		t.Fatalf("want 3 jobs, got %d: %v", len(jobs), jobs)
	}
	if jobs[0].Schedule != "*/5 * * * *" {
		t.Errorf("bad schedule: %q", jobs[0].Schedule)
	}
	if jobs[1].Schedule != "@daily" {
		t.Errorf("bad descriptor schedule: %q", jobs[1].Schedule)
	}
	if jobs[1].Name != "backup.sh" {
		t.Errorf("name should strip path: %q", jobs[1].Name)
	}
	if jobs[0].NextRun == nil {
		t.Errorf("expected NextRun for %q", jobs[0].Schedule)
	}
}

func TestCrontabDeleteRemovesOnlyMatch(t *testing.T) {
	f := &fakeCrontab{content: "*/5 * * * * /usr/bin/foo\n@daily /usr/bin/bar\n"}
	c := New()
	c.Runner = f.run

	jobs, err := c.List(t.Context())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	target := jobs[0]
	if err := c.Delete(t.Context(), target.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if !strings.Contains(f.content, "@daily /usr/bin/bar") {
		t.Errorf("non-target line was removed: %q", f.content)
	}
	if strings.Contains(f.content, "/usr/bin/foo") {
		t.Errorf("target line not removed: %q", f.content)
	}
}

func TestCrontabDeleteUnknownIDReturnsNotFound(t *testing.T) {
	f := &fakeCrontab{content: "*/5 * * * * /usr/bin/foo\n"}
	c := New()
	c.Runner = f.run
	if err := c.Delete(t.Context(), "crontab:deadbeef"); !errors.Is(err, cron.ErrNotFound) {
		t.Errorf("want cron.ErrNotFound, got %v", err)
	}
}

func TestCrontabDeleteLastEntryRemovesCrontab(t *testing.T) {
	f := &fakeCrontab{content: "*/5 * * * * /usr/bin/foo\n"}
	c := New()
	c.Runner = f.run

	jobs, _ := c.List(t.Context())
	if err := c.Delete(t.Context(), jobs[0].ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	// We expect a `crontab -r` (full removal) rather than a no-op replace.
	var sawR bool
	for _, c := range f.calls {
		if len(c) == 1 && c[0] == "-r" {
			sawR = true
		}
	}
	if !sawR {
		t.Errorf("removing last entry should call crontab -r; calls=%v", f.calls)
	}
}

func TestSplitCrontabLine(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		in       string
		schedule string
		command  string
		ok       bool
	}{
		{"five-field", "*/5 * * * * /bin/foo", "*/5 * * * *", "/bin/foo", true},
		{"runs of whitespace", "  0  9 *  *  1   /bin/foo --x", "0 9 * * 1", "/bin/foo --x", true},
		{"descriptor", "@daily /bin/foo", "@daily", "/bin/foo", true},
		{"descriptor without command", "@reboot", "", "", false},
		{"too few fields", "too few", "", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s, c, ok := splitCrontabLine(tc.in)
			if ok != tc.ok || s != tc.schedule || c != tc.command {
				t.Errorf("split(%q) = (%q,%q,%v), want (%q,%q,%v)", tc.in, s, c, ok, tc.schedule, tc.command, tc.ok)
			}
		})
	}
}

// crontab files written by editors that default to UTF-8-with-BOM (Notepad,
// some Windows tools) shouldn't break parsing. The BOM survives TrimSpace,
// so we strip it explicitly in splitCrontabLine.
func TestCrontabNameAndScope(t *testing.T) {
	t.Parallel()
	c := New()
	if c.Name() != "crontab" {
		t.Errorf("Name() = %q, want %q", c.Name(), "crontab")
	}
	if c.Scope() != cron.ScopeUser {
		t.Errorf("Scope() = %v, want %v", c.Scope(), cron.ScopeUser)
	}
}

func TestCrontabListPropagatesRunnerError(t *testing.T) {
	t.Parallel()
	want := errors.New("crontab disk on fire")
	c := New()
	c.Runner = func(_ context.Context, _ []string, _ string) ([]byte, error) {
		return nil, want
	}
	_, err := c.List(t.Context())
	if !errors.Is(err, want) {
		t.Errorf("want %v, got %v", want, err)
	}
}

func TestCrontabListIgnoresUTF8BOM(t *testing.T) {
	t.Parallel()
	f := &fakeCrontab{content: "\uFEFF*/5 * * * * /usr/bin/echo hi\n@daily /bin/backup\n"}
	c := New()
	c.Runner = f.run
	jobs, err := c.List(t.Context())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(jobs) != 2 {
		t.Fatalf("BOM swallowed a job: got %d", len(jobs))
	}
	if jobs[0].Schedule != "*/5 * * * *" {
		t.Errorf("BOM bled into schedule: %q", jobs[0].Schedule)
	}
}

// CRLF line endings (CRLF crontabs come from Windows-edited files copied to
// macOS). bufio.ScanLines strips the trailing CR, so we should still see
// clean fields and round-tripped Raw.
func TestCrontabListHandlesCRLF(t *testing.T) {
	t.Parallel()
	f := &fakeCrontab{content: "*/5 * * * * /bin/echo hi\r\n@daily /bin/backup\r\n"}
	c := New()
	c.Runner = f.run
	jobs, err := c.List(t.Context())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(jobs) != 2 {
		t.Fatalf("CRLF broke parsing: got %d jobs", len(jobs))
	}
	if strings.HasSuffix(jobs[0].Command, "\r") {
		t.Errorf("CR leaked into command: %q", jobs[0].Command)
	}
}

// Tabs are legitimate field separators in crontabs (man 5 crontab). We must
// recognise them in both 5-field and descriptor lines.
func TestSplitCrontabLineHandlesTabs(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, in, sched, cmd string
	}{
		{"5-field tabs", "*/5\t*\t*\t*\t*\t/bin/echo hi", "*/5 * * * *", "/bin/echo hi"},
		{"descriptor tab", "@daily\t/bin/backup", "@daily", "/bin/backup"},
		{"mixed space/tab", "0 \t9 *\t*\t* /bin/foo", "0 9 * * *", "/bin/foo"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s, c, ok := splitCrontabLine(tc.in)
			if !ok {
				t.Fatalf("split(%q) failed unexpectedly", tc.in)
			}
			if s != tc.sched || c != tc.cmd {
				t.Errorf("split(%q) = (%q,%q), want (%q,%q)", tc.in, s, c, tc.sched, tc.cmd)
			}
		})
	}
}

// Schedules that the cron parser library can't parse should still surface as
// jobs (with no NextRun) rather than being silently dropped — the user wants
// to know the line exists even if the spec is broken.
func TestCrontabListKeepsLinesWithBadSchedule(t *testing.T) {
	t.Parallel()
	f := &fakeCrontab{content: "*/notaschedule * * * * /bin/maybe\n"}
	c := New()
	c.Runner = f.run
	jobs, err := c.List(t.Context())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("bad schedule line should still appear, got %d jobs", len(jobs))
	}
	if jobs[0].NextRun != nil {
		t.Errorf("NextRun should be nil for unparseable schedule, got %v", jobs[0].NextRun)
	}
}

// Env-var assignments at the top of a crontab are valid syntax but aren't
// scheduled jobs. The user crontab parser today just falls into "fewer than
// 5 fields", which silently drops them — verify that path stays stable.
func TestCrontabListSkipsEnvVarLines(t *testing.T) {
	t.Parallel()
	f := &fakeCrontab{content: `SHELL=/bin/bash
PATH=/usr/local/bin:/usr/bin:/bin
*/5 * * * * /bin/echo hi
`}
	c := New()
	c.Runner = f.run
	jobs, err := c.List(t.Context())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("env-vars should not become jobs: got %d", len(jobs))
	}
}

// Delete with an ID that doesn't carry the "crontab:" prefix is a programmer
// error — not a missing entry. We return ErrNotFound rather than mutating
// anything so the cron.Manager fan-out can fall through cleanly.
func TestCrontabDeleteIDWithoutPrefix(t *testing.T) {
	t.Parallel()
	f := &fakeCrontab{content: "*/5 * * * * /bin/echo hi\n"}
	c := New()
	c.Runner = f.run
	if err := c.Delete(t.Context(), "launchd:something"); !errors.Is(err, cron.ErrNotFound) {
		t.Errorf("non-prefix ID: want ErrNotFound, got %v", err)
	}
	if !strings.Contains(f.content, "/bin/echo hi") {
		t.Errorf("crontab mutated by foreign ID: %q", f.content)
	}
}

// Comments must round-trip on Delete: when we rewrite the crontab, the
// surrounding comments and blank lines should be preserved exactly. A naive
// implementation that scanner.Trim()s lines into a slice would lose them.
func TestCrontabDeletePreservesCommentsAndBlankLines(t *testing.T) {
	t.Parallel()
	f := &fakeCrontab{content: `# header
SHELL=/bin/bash

# group A
*/5 * * * * /bin/foo
*/10 * * * * /bin/bar

# tail
`}
	c := New()
	c.Runner = f.run
	jobs, _ := c.List(t.Context())
	// jobs[0] is whichever comes first alphabetically — pick the foo one by command.
	var target cron.Job
	for _, j := range jobs {
		if strings.Contains(j.Command, "/bin/foo") {
			target = j
		}
	}
	if target.ID == "" {
		t.Fatalf("setup failure: foo job not found")
	}
	if err := c.Delete(t.Context(), target.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	for _, want := range []string{"# header", "SHELL=/bin/bash", "# group A", "/bin/bar", "# tail"} {
		if !strings.Contains(f.content, want) {
			t.Errorf("missing context line %q after delete; got:\n%s", want, f.content)
		}
	}
	if strings.Contains(f.content, "/bin/foo") {
		t.Errorf("target line not deleted: %q", f.content)
	}
}

func TestCommandShortName(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"absolute path", "/usr/bin/echo hi", "echo"},
		{"env-prefixed path", "PATH=/x:/y /usr/local/bin/run", "run"},
		{"bare command", "foo bar", "foo"},
		{"empty", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := cron.CommandShortName(tc.in); got != tc.want {
				t.Errorf("cron.CommandShortName(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
