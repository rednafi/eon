package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rednafi/eon/cron"
)

// fakeOrigin is a minimal Source used to assemble a Manager without touching
// real cron files. The CLI doesn't care which backend a job came from, so a
// single fake covers list/show/delete/add/edit behaviour.
type fakeOrigin struct {
	jobs    []cron.Job
	deleted []string
	added   []cron.JobSpec
	edited  map[string]cron.JobSpec
}

func (f *fakeOrigin) Name() string      { return "fake" }
func (f *fakeOrigin) Scope() cron.Scope { return cron.ScopeUser }
func (f *fakeOrigin) List(_ context.Context) ([]cron.Job, error) {
	return slices.Clone(f.jobs), nil
}
func (f *fakeOrigin) Delete(_ context.Context, id string) error {
	for i, j := range f.jobs {
		if j.ID == id {
			f.jobs = append(f.jobs[:i], f.jobs[i+1:]...)
			f.deleted = append(f.deleted, id)
			return nil
		}
	}
	return cron.ErrNotFound
}

func (f *fakeOrigin) Add(_ context.Context, spec cron.JobSpec) (cron.Job, error) {
	f.added = append(f.added, spec)
	j := cron.Job{
		ID: "fake:" + cron.ShortHash(spec.Schedule+"|"+spec.Command),
		Kind: "fake", Name: spec.Command,
		Schedule: spec.Schedule, Command: spec.Command,
	}
	f.jobs = append(f.jobs, j)
	return j, nil
}

func (f *fakeOrigin) Edit(_ context.Context, id string, spec cron.JobSpec) (cron.Job, error) {
	if f.edited == nil {
		f.edited = map[string]cron.JobSpec{}
	}
	for i, j := range f.jobs {
		if j.ID == id {
			f.jobs[i].Schedule = spec.Schedule
			f.jobs[i].Command = spec.Command
			f.edited[id] = spec
			return f.jobs[i], nil
		}
	}
	return cron.Job{}, cron.ErrNotFound
}

func newFakeManager(jobs ...cron.Job) (*cron.Manager, *fakeOrigin) {
	f := &fakeOrigin{jobs: jobs}
	return cron.NewManager(f), f
}

// runCmd builds the root cobra command, points its IO at the supplied
// buffers, sets argv, and runs it. Returns whatever error (or nil) cobra
// produced — analogous to the old `Run(...) int` but lets tests assert on
// the actual error value.
func runCmd(t *testing.T, mgr *cron.Manager, argv []string, stdin io.Reader, stdout, stderr io.Writer) error {
	t.Helper()
	root := BuildRoot(mgr)
	root.SetArgs(argv)
	if stdin != nil {
		root.SetIn(stdin)
	}
	root.SetOut(stdout)
	root.SetErr(stderr)
	return root.ExecuteContext(t.Context())
}

func mustOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
}

func TestListEmpty(t *testing.T) {
	t.Parallel()
	mgr, _ := newFakeManager()
	var out bytes.Buffer
	mustOK(t, runCmd(t, mgr, []string{"list"}, nil, &out, &out))
	if !strings.Contains(out.String(), "(no scheduled jobs)") {
		t.Errorf("missing empty-state message: %q", out.String())
	}
}

func TestListJSON(t *testing.T) {
	t.Parallel()
	mgr, _ := newFakeManager(cron.Job{ID: "fake:1", Kind: "fake", Name: "first", Schedule: "@daily"})
	var out bytes.Buffer
	mustOK(t, runCmd(t, mgr, []string{"list", "--json"}, nil, &out, &out))
	var got []cron.Job
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out.String())
	}
	if len(got) != 1 || got[0].ID != "fake:1" {
		t.Errorf("unexpected payload: %+v", got)
	}
}

func TestListHidesSystemByDefault(t *testing.T) {
	t.Parallel()
	mgr, _ := newFakeManager(
		cron.Job{ID: "fake:user", Name: "user-job"},
		cron.Job{ID: "fake:sys", Name: "sys-job", Scope: cron.ScopeSystem},
	)
	var out bytes.Buffer
	mustOK(t, runCmd(t, mgr, []string{"list"}, nil, &out, &out))
	if strings.Contains(out.String(), "sys-job") {
		t.Errorf("default list should hide system jobs:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "user-job") {
		t.Errorf("user job missing:\n%s", out.String())
	}
}

func TestListAllShowsSystem(t *testing.T) {
	t.Parallel()
	mgr, _ := newFakeManager(
		cron.Job{ID: "fake:user", Name: "user-job"},
		cron.Job{ID: "fake:sys", Name: "sys-job", Scope: cron.ScopeSystem},
	)
	var out bytes.Buffer
	mustOK(t, runCmd(t, mgr, []string{"list", "--all"}, nil, &out, &out))
	if !strings.Contains(out.String(), "sys-job") {
		t.Errorf("--all should surface system jobs:\n%s", out.String())
	}
}

func TestShowResolvesByPrefix(t *testing.T) {
	t.Parallel()
	mgr, _ := newFakeManager(
		cron.Job{ID: "fake:com.example.alpha", Name: "alpha"},
		cron.Job{ID: "fake:com.example.beta", Name: "beta"},
	)
	var out bytes.Buffer
	mustOK(t, runCmd(t, mgr, []string{"show", "alpha"}, nil, &out, &out))
	if !strings.Contains(out.String(), "com.example.alpha") {
		t.Errorf("unexpected output: %s", out.String())
	}
}

func TestDeleteWithYesFlag(t *testing.T) {
	t.Parallel()
	mgr, fake := newFakeManager(cron.Job{ID: "fake:to-go", Name: "to-go"})
	var out bytes.Buffer
	mustOK(t, runCmd(t, mgr, []string{"delete", "to-go", "--yes"}, nil, &out, &out))
	if len(fake.deleted) != 1 || fake.deleted[0] != "fake:to-go" {
		t.Errorf("delete not invoked: %v", fake.deleted)
	}
}

func TestDeletePromptDeniedKeepsJob(t *testing.T) {
	t.Parallel()
	mgr, fake := newFakeManager(cron.Job{ID: "fake:keep", Name: "keep"})
	var out bytes.Buffer
	stdin := strings.NewReader("n\n")
	err := runCmd(t, mgr, []string{"delete", "keep"}, stdin, &out, &out)
	if !errors.Is(err, errAborted) {
		t.Errorf("want errAborted, got %v", err)
	}
	if len(fake.deleted) != 0 {
		t.Errorf("delete should have been skipped: %v", fake.deleted)
	}
}

func TestDeleteSystemRefused(t *testing.T) {
	t.Parallel()
	mgr, fake := newFakeManager(cron.Job{ID: "fake:sys", Name: "sys", Scope: cron.ScopeSystem})
	var out bytes.Buffer
	err := runCmd(t, mgr, []string{"delete", "sys", "--yes"}, nil, &out, &out)
	if err == nil || !strings.Contains(err.Error(), "system-scope") {
		t.Errorf("system delete should be refused, got %v", err)
	}
	if len(fake.deleted) != 0 {
		t.Errorf("system job should not be deleted: %v", fake.deleted)
	}
}

func TestAddRequiresScheduleAndCommand(t *testing.T) {
	t.Parallel()
	mgr, _ := newFakeManager()
	var out, errOut bytes.Buffer
	if err := runCmd(t, mgr, []string{"add"}, nil, &out, &errOut); err == nil {
		t.Errorf("add without flags must fail")
	}
}

func TestAddHappyPath(t *testing.T) {
	t.Parallel()
	mgr, fake := newFakeManager()
	var out bytes.Buffer
	mustOK(t, runCmd(t, mgr, []string{"add", "--schedule", "@daily", "--command", "/bin/echo hi"}, nil, &out, &out))
	if len(fake.added) != 1 || fake.added[0].Schedule != "@daily" {
		t.Errorf("add not invoked correctly: %+v", fake.added)
	}
	if !strings.Contains(out.String(), "added fake:") {
		t.Errorf("expected 'added <id>' confirmation, got %q", out.String())
	}
}

// --source <unknown> must fail loudly. Without this, a typo in the source
// name silently picks the first writable Source — surprising and unsafe.
func TestAddWithUnknownSourceErrors(t *testing.T) {
	t.Parallel()
	mgr, _ := newFakeManager()
	var out bytes.Buffer
	err := runCmd(t, mgr, []string{"add", "--schedule", "@daily", "--command", "/bin/x", "--source", "ghost"}, nil, &out, &out)
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'source not found' error, got %v", err)
	}
}

func TestEditRequiresExistingJob(t *testing.T) {
	t.Parallel()
	mgr, _ := newFakeManager()
	var out bytes.Buffer
	err := runCmd(t, mgr, []string{"edit", "missing", "--schedule", "@daily"}, nil, &out, &out)
	if !errors.Is(err, cron.ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

func TestEditChangesScheduleOnly(t *testing.T) {
	t.Parallel()
	mgr, fake := newFakeManager(cron.Job{ID: "fake:1", Name: "alpha", Schedule: "@daily", Command: "/bin/keep"})
	var out bytes.Buffer
	mustOK(t, runCmd(t, mgr, []string{"edit", "alpha", "--schedule", "@hourly"}, nil, &out, &out))
	if got := fake.edited["fake:1"]; got.Schedule != "@hourly" || got.Command != "/bin/keep" {
		t.Errorf("partial edit: command should have been kept, got %+v", got)
	}
}

func TestEditChangesCommandOnly(t *testing.T) {
	t.Parallel()
	mgr, fake := newFakeManager(cron.Job{ID: "fake:1", Name: "alpha", Schedule: "@daily", Command: "/bin/old"})
	var out bytes.Buffer
	mustOK(t, runCmd(t, mgr, []string{"edit", "alpha", "--command", "/bin/new"}, nil, &out, &out))
	if got := fake.edited["fake:1"]; got.Schedule != "@daily" || got.Command != "/bin/new" {
		t.Errorf("partial edit: schedule should have been kept, got %+v", got)
	}
}

func TestEditRefusesSystemScopeJob(t *testing.T) {
	t.Parallel()
	mgr, fake := newFakeManager(cron.Job{ID: "fake:sys", Name: "sys", Scope: cron.ScopeSystem, Schedule: "@daily", Command: "/bin/foo"})
	var out bytes.Buffer
	err := runCmd(t, mgr, []string{"edit", "sys", "--command", "/bin/new"}, nil, &out, &out)
	if !errors.Is(err, errSystemReadOnly) {
		t.Errorf("want errSystemReadOnly, got %v", err)
	}
	if len(fake.edited) != 0 {
		t.Errorf("system job should not be edited: %v", fake.edited)
	}
}

func TestUnknownCommandIsError(t *testing.T) {
	t.Parallel()
	mgr, _ := newFakeManager()
	var out bytes.Buffer
	if err := runCmd(t, mgr, []string{"bogus"}, nil, &out, &out); err == nil {
		t.Errorf("unknown command should produce an error")
	}
}

func TestTruncateRunesHandlesMultibyte(t *testing.T) {
	t.Parallel()
	// "café" has 4 runes / 5 bytes — naive c[:4] would slice mid-codepoint.
	if got := truncateRunes("café-runner", 4); got != "caf…" {
		t.Errorf("truncateRunes(\"café-runner\", 4) = %q, want %q", got, "caf…")
	}
	if got := truncateRunes("short", 10); got != "short" {
		t.Errorf("under-width passthrough failed: %q", got)
	}
}

// CJK glyphs occupy two display cells. truncateRunes must not produce a
// result wider than the requested cap — the previous rune-counting
// implementation would output a string that visually overflowed the
// column on every line containing a 2-cell character.
func TestTruncateRunesRespectsDisplayWidth(t *testing.T) {
	t.Parallel()
	got := truncateRunes("日本語の文字列", 6)
	if w := runeWidth(got); w > 6 {
		t.Errorf("truncated width = %d cells, want ≤ 6: %q", w, got)
	}
}

// runeWidth must return 2 for a CJK glyph and 0 for a zero-width joiner.
// Without a real width function, table columns drift any time a job name
// contains either.
func TestRuneWidthCellAccounting(t *testing.T) {
	t.Parallel()
	if w := runeWidth("日"); w != 2 {
		t.Errorf("CJK width = %d, want 2", w)
	}
	if w := runeWidth("a"); w != 1 {
		t.Errorf("ASCII width = %d, want 1", w)
	}
}

// `eon show <id> --json` should produce parseable JSON for a single job —
// callers wire eon into pipelines and rely on this shape.
func TestShowJSONEmitsSingleJob(t *testing.T) {
	t.Parallel()
	mgr, _ := newFakeManager(cron.Job{ID: "fake:1", Kind: "fake", Name: "alpha", Schedule: "@daily"})
	var out bytes.Buffer
	mustOK(t, runCmd(t, mgr, []string{"show", "alpha", "--json"}, nil, &out, &out))
	var got cron.Job
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out.String())
	}
	if got.ID != "fake:1" {
		t.Errorf("unexpected payload: %+v", got)
	}
}

// Show against an unknown ID must surface ErrNotFound — cobra exits non-zero
// and a script wrapping `eon show` can branch on it.
func TestShowMissingIDReturnsError(t *testing.T) {
	t.Parallel()
	mgr, _ := newFakeManager(cron.Job{ID: "fake:foo", Name: "foo"})
	var out bytes.Buffer
	err := runCmd(t, mgr, []string{"show", "missing"}, nil, &out, &out)
	if !errors.Is(err, cron.ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

// Logs command with no configured paths should print a friendly message
// rather than nothing — confirms the user isn't running into an env-var or
// permissions problem.
func TestLogsWithNoPathsPrintsHint(t *testing.T) {
	t.Parallel()
	mgr, _ := newFakeManager(cron.Job{ID: "fake:nopaths", Name: "nopaths"})
	var out bytes.Buffer
	mustOK(t, runCmd(t, mgr, []string{"logs", "nopaths"}, nil, &out, &out))
	if !strings.Contains(out.String(), "no log paths configured") {
		t.Errorf("expected hint, got %q", out.String())
	}
}

// Logs command tails both stdout and stderr files and emits a section header
// per stream so the user knows which is which.
func TestLogsTailsBothStreams(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	stdout := filepath.Join(dir, "out.log")
	stderr := filepath.Join(dir, "err.log")
	if err := os.WriteFile(stdout, []byte("out-line-1\nout-line-2\n"), 0o600); err != nil {
		t.Fatalf("write stdout: %v", err)
	}
	if err := os.WriteFile(stderr, []byte("err-line-1\n"), 0o600); err != nil {
		t.Fatalf("write stderr: %v", err)
	}
	mgr, _ := newFakeManager(cron.Job{
		ID: "fake:withlogs", Name: "withlogs",
		StdoutPath: stdout, StderrPath: stderr,
	})
	var out bytes.Buffer
	mustOK(t, runCmd(t, mgr, []string{"logs", "withlogs"}, nil, &out, &out))
	got := out.String()
	for _, want := range []string{"── stdout", "── stderr", "out-line-2", "err-line-1"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in output:\n%s", want, got)
		}
	}
}

// Logs against a missing log file must report the read error per stream
// (so the user can tell which one is missing) but not abort.
func TestLogsMissingFileReportsErrorPerStream(t *testing.T) {
	t.Parallel()
	mgr, _ := newFakeManager(cron.Job{
		ID: "fake:missing", Name: "missing",
		StdoutPath: "/no/such/file.log",
	})
	var out, errOut bytes.Buffer
	mustOK(t, runCmd(t, mgr, []string{"logs", "missing"}, nil, &out, &errOut))
	combined := out.String() + errOut.String()
	if !strings.Contains(combined, "stdout") {
		t.Errorf("expected stream label, got %q", combined)
	}
	if !strings.Contains(combined, "no such file") && !strings.Contains(combined, "no such") {
		t.Errorf("expected ENOENT mention, got %q", combined)
	}
}

// `--lines 0` must fall back to the default 100, not produce zero output.
// A naive implementation that passes 0 to a "last n lines" loop would print
// nothing, which is unhelpful.
func TestLogsLinesZeroFallsBackToDefault(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	logPath := filepath.Join(dir, "out.log")
	var b strings.Builder
	for i := range 200 {
		b.WriteString("L")
		b.WriteRune(rune('0' + i%10))
		b.WriteByte('\n')
	}
	if err := os.WriteFile(logPath, []byte(b.String()), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	mgr, _ := newFakeManager(cron.Job{
		ID: "fake:big", Name: "big",
		StdoutPath: logPath,
	})
	var out bytes.Buffer
	mustOK(t, runCmd(t, mgr, []string{"logs", "big", "-n", "0"}, nil, &out, &out))
	// The default is 100. Allow some slack for the section header line(s).
	lines := strings.Count(out.String(), "\n")
	if lines < 100 || lines > 105 {
		t.Errorf("expected ~100 lines, got %d:\n%s", lines, out.String())
	}
}

// Delete against a substring that matches multiple jobs is ambiguous —
// cobra should surface that error rather than silently picking one.
func TestDeleteAmbiguousIDFailsLoudly(t *testing.T) {
	t.Parallel()
	mgr, fake := newFakeManager(
		cron.Job{ID: "fake:com.foo.alpha", Name: "alpha"},
		cron.Job{ID: "fake:com.foo.beta", Name: "beta"},
	)
	var out bytes.Buffer
	err := runCmd(t, mgr, []string{"delete", "foo", "--yes"}, nil, &out, &out)
	if err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Errorf("expected ambiguous error, got %v", err)
	}
	if len(fake.deleted) != 0 {
		t.Errorf("nothing should have been deleted: %v", fake.deleted)
	}
}

// Delete confirmed with a Y carriage return prompt (Windows pasted input)
// should still register as a yes. confirm() runs through bufio.Scanner
// which strips CR/LF, but a regression to ReadString could re-introduce it.
func TestDeletePromptAcceptsCRLFYes(t *testing.T) {
	t.Parallel()
	mgr, fake := newFakeManager(cron.Job{ID: "fake:cr", Name: "cr"})
	var out bytes.Buffer
	stdin := strings.NewReader("yes\r\n")
	if err := runCmd(t, mgr, []string{"delete", "cr"}, stdin, &out, &out); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if len(fake.deleted) != 1 {
		t.Errorf("CRLF 'yes' not recognised: %v", fake.deleted)
	}
}

// Empty stdin (EOF before any newline) must NOT delete — the prompt is
// "y/N" with N as the safe default. Treating EOF as "yes" would lose
// data on a script that pipes /dev/null in.
func TestDeletePromptEOFIsNo(t *testing.T) {
	t.Parallel()
	mgr, fake := newFakeManager(cron.Job{ID: "fake:safe", Name: "safe"})
	var out bytes.Buffer
	err := runCmd(t, mgr, []string{"delete", "safe"}, strings.NewReader(""), &out, &out)
	if !errors.Is(err, errAborted) {
		t.Errorf("EOF prompt: want errAborted, got %v", err)
	}
	if len(fake.deleted) != 0 {
		t.Errorf("EOF should not delete: %v", fake.deleted)
	}
}

// renderJobDetail must emit human-readable times for NextRun / LastRun.
// Until now a missing test let a renderer regression hide LastRun entirely.
func TestRenderJobDetailIncludesAllTimestamps(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	earlier := now.Add(-time.Hour)
	mgr, _ := newFakeManager(cron.Job{
		ID: "fake:full", Name: "full",
		Schedule:   "@daily",
		Status:     "loaded",
		NextRun:    &now,
		LastRun:    &earlier,
		PID:        4242,
		StdoutPath: "/tmp/o",
		StderrPath: "/tmp/e",
		Path:       "/var/foo",
		Command:    "/bin/echo hi",
	})
	var out bytes.Buffer
	mustOK(t, runCmd(t, mgr, []string{"show", "full"}, nil, &out, &out))
	got := out.String()
	for _, want := range []string{"2026-05-05T12:00:00", "2026-05-05T11:00:00", "PID:", "4242", "/tmp/o", "/tmp/e", "/var/foo"} {
		if !strings.Contains(got, want) {
			t.Errorf("renderJobDetail missing %q\n%s", want, got)
		}
	}
}

// An empty log file should produce zero output, not a single blank line.
// strings.Split("", "\n") yields [""] which would otherwise echo "" to the
// writer.
func TestTailEmptyFilePrintsNothing(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	f := filepath.Join(dir, "empty.log")
	if err := os.WriteFile(f, nil, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	var out bytes.Buffer
	if err := tail(&out, f, 100); err != nil {
		t.Fatalf("tail: %v", err)
	}
	if out.Len() != 0 {
		t.Errorf("empty file should print nothing, got %q", out.String())
	}
}

// File containing only newlines (no content) should also print nothing.
func TestTailWhitespaceOnlyFilePrintsNothing(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	f := filepath.Join(dir, "only-newlines.log")
	if err := os.WriteFile(f, []byte("\n\n\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	var out bytes.Buffer
	if err := tail(&out, f, 100); err != nil {
		t.Fatalf("tail: %v", err)
	}
	if out.Len() != 0 {
		t.Errorf("newline-only file should print nothing, got %q", out.String())
	}
}

func TestTailReturnsLastNLines(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	f := filepath.Join(dir, "log")
	var b strings.Builder
	for i := range 50 {
		b.WriteString("line ")
		b.WriteRune(rune('A' + i%26))
		b.WriteByte('\n')
	}
	body := b.String()
	if err := os.WriteFile(f, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	var out bytes.Buffer
	if err := tail(&out, f, 5); err != nil {
		t.Fatalf("tail: %v", err)
	}
	got := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(got) != 5 {
		t.Fatalf("want 5 lines, got %d: %v", len(got), got)
	}
}
