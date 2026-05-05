package crontab

import (
	"context"
	"errors"
	"strings"
	"testing"

	"slices"

	"github.com/rednafi/eon/cron"
	"github.com/rednafi/eon/cron/crontest"
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

// A line longer than the scanner's 1MB buffer used to be silently
// dropped. parse() now propagates the bufio error so the caller can show
// "your crontab was truncated" instead of returning a partial list.
func TestCrontabParseSurfacesScannerError(t *testing.T) {
	t.Parallel()
	c := New()
	jobs, err := c.parse(strings.Repeat("a", 2*1024*1024) + "\n")
	if err == nil {
		t.Errorf("expected scanner error on a >1MB line, got nil")
	}
	// Any jobs that did parse before the failure should still be returned.
	_ = jobs
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

// Add appends a line and reports the new Job. The runner should have seen
// a "crontab -" call with the previous content + new line.
func TestCrontabAddAppendsLine(t *testing.T) {
	t.Parallel()
	f := &fakeCrontab{content: "*/5 * * * * /bin/old\n"}
	c := New()
	c.Runner = f.run
	j, err := c.Add(t.Context(), cron.JobSpec{Schedule: "@daily", Command: "/bin/new"})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if !strings.Contains(j.ID, "crontab:") {
		t.Errorf("returned job ID lacks prefix: %q", j.ID)
	}
	if j.Schedule != "@daily" || j.Command != "/bin/new" {
		t.Errorf("returned job fields wrong: %+v", j)
	}
	if !strings.Contains(f.content, "/bin/old") || !strings.Contains(f.content, "/bin/new") {
		t.Errorf("crontab missing both lines: %q", f.content)
	}
}

// Add against an empty crontab must not produce a leading blank line.
// `crontab -l` returns "no crontab for $user" on empty, which the fake
// translates to empty content.
func TestCrontabAddIntoEmpty(t *testing.T) {
	t.Parallel()
	f := &fakeCrontab{content: ""}
	c := New()
	c.Runner = f.run
	if _, err := c.Add(t.Context(), cron.JobSpec{Schedule: "*/15 * * * *", Command: "/bin/echo first"}); err != nil {
		t.Fatalf("add: %v", err)
	}
	if strings.HasPrefix(f.content, "\n") {
		t.Errorf("empty-crontab add produced leading blank: %q", f.content)
	}
	if !strings.HasSuffix(f.content, "\n") {
		t.Errorf("crontab body must end in newline: %q", f.content)
	}
}

// Add must reject empty schedule, empty command, command containing a
// newline, and schedules that the cron lib can't parse — none of those
// should ever land in the spool.
func TestCrontabAddRejectsInvalidSpec(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		spec cron.JobSpec
	}{
		{"empty schedule", cron.JobSpec{Schedule: "", Command: "/bin/foo"}},
		{"whitespace schedule", cron.JobSpec{Schedule: "   ", Command: "/bin/foo"}},
		{"empty command", cron.JobSpec{Schedule: "@daily", Command: ""}},
		{"newline in command", cron.JobSpec{Schedule: "@daily", Command: "/bin/foo\nrm -rf /"}},
		{"unparseable schedule", cron.JobSpec{Schedule: "every blue moon", Command: "/bin/foo"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := &fakeCrontab{content: "*/5 * * * * /bin/old\n"}
			c := New()
			c.Runner = f.run
			if _, err := c.Add(t.Context(), tc.spec); err == nil {
				t.Errorf("expected validation error for %+v", tc.spec)
			}
			if !strings.HasSuffix(f.content, "/bin/old\n") {
				t.Errorf("crontab mutated despite validation failure: %q", f.content)
			}
		})
	}
}

// Edit replaces the targeted line in place — surrounding lines and
// comments stay where they were. ID after edit changes (different hash).
func TestCrontabEditReplacesLineInPlace(t *testing.T) {
	t.Parallel()
	f := &fakeCrontab{content: "# header\n*/5 * * * * /bin/old\n@reboot /bin/sticky\n"}
	c := New()
	c.Runner = f.run
	jobs, _ := c.List(t.Context())
	var target cron.Job
	for _, j := range jobs {
		if strings.Contains(j.Command, "/bin/old") {
			target = j
		}
	}
	if target.ID == "" {
		t.Fatalf("setup: target job not found")
	}
	newJob, err := c.Edit(t.Context(), target.ID, cron.JobSpec{Schedule: "@hourly", Command: "/bin/new"})
	if err != nil {
		t.Fatalf("edit: %v", err)
	}
	if newJob.Schedule != "@hourly" || newJob.Command != "/bin/new" {
		t.Errorf("edit fields wrong: %+v", newJob)
	}
	for _, want := range []string{"# header", "/bin/new", "/bin/sticky"} {
		if !strings.Contains(f.content, want) {
			t.Errorf("crontab missing %q after edit:\n%s", want, f.content)
		}
	}
	if strings.Contains(f.content, "/bin/old") {
		t.Errorf("old command not removed:\n%s", f.content)
	}
	// The new ID should differ from the old (different hash).
	if newJob.ID == target.ID {
		t.Errorf("edited line should have a new ID hash; got the same: %q", newJob.ID)
	}
}

// Edit with an unrecognised ID returns ErrNotFound and does not touch
// the spool — Manager.Edit fan-out depends on this.
func TestCrontabEditUnknownIDIsNotFound(t *testing.T) {
	t.Parallel()
	f := &fakeCrontab{content: "*/5 * * * * /bin/foo\n"}
	c := New()
	c.Runner = f.run
	_, err := c.Edit(t.Context(), "crontab:deadbeef", cron.JobSpec{Schedule: "@daily", Command: "/bin/new"})
	if !errors.Is(err, cron.ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
	if !strings.Contains(f.content, "/bin/foo") {
		t.Errorf("crontab mutated despite ErrNotFound: %q", f.content)
	}
}

// Edit ID without "crontab:" prefix is foreign — must be ErrNotFound, not
// a fall-through that touches the spool.
func TestCrontabEditForeignIDIsNotFound(t *testing.T) {
	t.Parallel()
	f := &fakeCrontab{content: "*/5 * * * * /bin/foo\n"}
	c := New()
	c.Runner = f.run
	_, err := c.Edit(t.Context(), "launchd:com.example.foo", cron.JobSpec{Schedule: "@daily", Command: "/bin/new"})
	if !errors.Is(err, cron.ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

// FuzzSplitCrontabLine asserts the parser is total (never panics) and
// honours its postcondition: when ok=true, both schedule and command are
// non-empty trimmed strings \u2014 Add otherwise emits a malformed line that
// the cron daemon silently ignores. Seeded with adversarial fixtures
// (BOM, tabs, CRLF, NULs, very long whitespace runs).
func FuzzSplitCrontabLine(f *testing.F) {
	for _, seed := range []string{
		"",
		" ",
		"*/5 * * * * /bin/foo",
		"@daily /bin/backup",
		"@reboot",
		"\uFEFF*/5 * * * * /bin/x",
		"*/5\t*\t*\t*\t*\t/bin/x",
		"\r\n",
		"\x00\x00\x00",
		strings.Repeat(" ", 1024) + "/bin/x",
		strings.Repeat("*/5 * * * * /bin/foo\n", 100),
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, line string) {
		schedule, command, ok := splitCrontabLine(line)
		if !ok {
			return
		}
		if strings.TrimSpace(schedule) == "" {
			t.Errorf("ok=true but schedule blank for input %q", line)
		}
		if strings.TrimSpace(command) == "" {
			t.Errorf("ok=true but command blank for input %q", line)
		}
		// command must already be trimmed \u2014 leading whitespace would
		// produce a Name with surprising prefix.
		if command != strings.TrimSpace(command) {
			t.Errorf("command not trimmed: %q for input %q", command, line)
		}
	})
}

// FuzzCrontabParse stresses the full file-level parser. Invariants:
//  1. every Job's ID has the "crontab:" prefix and an 8-hex-digit hash;
//  2. Job.Name equals CommandShortName(Job.Command) \u2014 keeps the list view
//     consistent with the underlying command;
//  3. the parser never produces a Job with a blank schedule or command.
func FuzzCrontabParse(f *testing.F) {
	for _, seed := range []string{
		"",
		"# only comment\n",
		"*/5 * * * * /bin/echo\n",
		"*/5 * * * * /bin/echo\n@daily /bin/y\n",
		"\uFEFF*/5 * * * * /bin/echo\n",
		"\n\n\n",
	} {
		f.Add(seed)
	}
	c := New()
	f.Fuzz(func(t *testing.T, content string) {
		jobs, _ := c.parse(content)
		for _, j := range jobs {
			if !strings.HasPrefix(j.ID, "crontab:") || len(j.ID) != len("crontab:")+8 {
				t.Errorf("malformed ID: %q (input %q)", j.ID, content)
			}
			if want := cron.CommandShortName(j.Command); j.Name != want {
				t.Errorf("Name=%q want %q (Command=%q)", j.Name, want, j.Command)
			}
			if strings.TrimSpace(j.Schedule) == "" || strings.TrimSpace(j.Command) == "" {
				t.Errorf("blank schedule or command in Job: %+v", j)
			}
		}
	})
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

// TestCrontabContract runs the package-agnostic Source / Mutator contract
// against the crontab backend. Adding similar wrappers in every other
// backend means the interface contract is enforced uniformly: a regression
// in any single Source surfaces here, not just in backend-specific tests.
func TestCrontabContract(t *testing.T) {
	t.Parallel()
	newSource := func(*testing.T) cron.Source {
		c := New()
		c.Runner = (&fakeCrontab{content: "*/5 * * * * /bin/contract\n"}).run
		return c
	}
	crontest.Contract(t, "crontab", newSource)
	crontest.MutatorContract(t, "crontab", newSource,
		cron.JobSpec{Schedule: "@daily", Command: "/bin/contract-add"},
		cron.JobSpec{Schedule: "@hourly", Command: "/bin/contract-edit"},
	)
}
