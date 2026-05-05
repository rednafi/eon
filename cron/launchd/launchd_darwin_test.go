//go:build darwin

package launchd

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rednafi/eon/cron"
)

// minimalPlist returns a launchd plist body with the supplied label and
// minute interval. Tests use it to spin up dozens of fixtures without keeping
// a separate testdata tree.
func minimalPlist(label string, intervalSeconds int) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0"><dict>
<key>Label</key><string>%s</string>
<key>ProgramArguments</key><array><string>/bin/echo</string><string>%s</string></array>
<key>StartInterval</key><integer>%d</integer>
<key>StandardOutPath</key><string>/tmp/%s.out</string>
</dict></plist>`, label, label, intervalSeconds, label)
}

// arrayCalendarPlist exercises the "StartCalendarInterval is an array" path
// that broke real-world git-scm plists. We assert eon parses it without
// dropping the job — a regression here would silently hide users' jobs.
func arrayCalendarPlist(label string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0"><dict>
<key>Label</key><string>%s</string>
<key>ProgramArguments</key><array><string>/bin/echo</string></array>
<key>StartCalendarInterval</key>
<array>
<dict><key>Hour</key><integer>9</integer><key>Minute</key><integer>0</integer></dict>
<dict><key>Hour</key><integer>17</integer><key>Minute</key><integer>0</integer></dict>
</array>
</dict></plist>`, label)
}

func TestLaunchdListFromTempDir(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "com.example.foo.plist"), []byte(minimalPlist("com.example.foo", 60)), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "com.example.array.plist"), []byte(arrayCalendarPlist("com.example.array")), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	// Garbage file shouldn't crash listing.
	if err := os.WriteFile(filepath.Join(dir, "ignore-me.txt"), []byte("not a plist"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	src := &Launchd{Dir: dir, Tag: "test"}
	jobs, err := src.List(t.Context())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(jobs) != 2 {
		t.Fatalf("want 2 jobs, got %d: %v", len(jobs), jobs)
	}
	for _, j := range jobs {
		if !strings.HasPrefix(j.ID, "launchd-test:") {
			t.Errorf("bad ID prefix: %q", j.ID)
		}
		if j.Schedule == "" {
			t.Errorf("empty schedule for %q", j.ID)
		}
	}
}

func TestLaunchdDeleteRemovesPlist(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "com.example.target.plist")
	if err := os.WriteFile(path, []byte(minimalPlist("com.example.target", 60)), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	src := &Launchd{Dir: dir, Tag: "test"}

	if err := src.Delete(t.Context(), "launchd-test:com.example.target"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("plist still exists: %v", err)
	}
	if err := src.Delete(t.Context(), "launchd-test:com.example.target"); !errors.Is(err, cron.ErrNotFound) {
		t.Errorf("second delete should return cron.ErrNotFound, got %v", err)
	}
}

func TestLaunchdReadOnlyRejectsDelete(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "com.example.lock.plist"), []byte(minimalPlist("com.example.lock", 60)), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	src := &Launchd{Dir: dir, Tag: "system", ReadOnly: true}
	err := src.Delete(t.Context(), "launchd-system:com.example.lock")
	if err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Errorf("want read-only error, got %v", err)
	}
}

func TestLaunchdHundredJobs(t *testing.T) {
	t.Parallel()
	// The user explicitly asked us to verify 100-job listing and deletion
	// behavior. We materialize 100 plists in a tmp dir, list them, then
	// delete every other one and re-list.
	dir := t.TempDir()
	for i := range 100 {
		label := fmt.Sprintf("com.eon.test.%03d", i)
		path := filepath.Join(dir, label+".plist")
		if err := os.WriteFile(path, []byte(minimalPlist(label, 30+i)), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	src := &Launchd{Dir: dir, Tag: "test"}
	jobs, err := src.List(t.Context())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(jobs) != 100 {
		t.Fatalf("want 100 jobs, got %d", len(jobs))
	}
	// Sort is alphabetical, so jobs[0] should be label 000.
	if jobs[0].Name != "com.eon.test.000" {
		t.Errorf("sort order broken: %q", jobs[0].Name)
	}

	deleted := 0
	for _, j := range jobs {
		// Delete the even-suffixed jobs.
		if strings.HasSuffix(j.Name, "0") || strings.HasSuffix(j.Name, "2") ||
			strings.HasSuffix(j.Name, "4") || strings.HasSuffix(j.Name, "6") || strings.HasSuffix(j.Name, "8") {
			if err := src.Delete(t.Context(), j.ID); err != nil {
				t.Errorf("delete %s: %v", j.ID, err)
			}
			deleted++
		}
	}
	if deleted != 50 {
		t.Fatalf("expected to delete 50 jobs, deleted %d", deleted)
	}
	jobs, _ = src.List(t.Context())
	if len(jobs) != 50 {
		t.Fatalf("want 50 jobs remaining, got %d", len(jobs))
	}
}

func TestLaunchdMissingDir(t *testing.T) {
	t.Parallel()
	src := &Launchd{Dir: "/no/such/path/in/this/test", Tag: "test"}
	jobs, err := src.List(t.Context())
	if err != nil {
		t.Errorf("missing dir should not error: %v", err)
	}
	if jobs != nil {
		t.Errorf("want nil jobs for missing dir, got %v", jobs)
	}
}

// formatInterval boundaries: day/hour/minute thresholds are picked off
// modular zero, so a value like 90s should land in seconds (not minutes),
// and 86400s should round to days.
func TestFormatInterval(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   int
		want string
	}{
		{45, "every 45s"},
		{60, "every 1m"},
		{90, "every 90s"}, // not a clean minute, falls through to seconds
		{300, "every 5m"},
		{3600, "every 1h"},
		{7200, "every 2h"},
		{86400, "every 1d"},
		{172800, "every 2d"},
	}
	for _, tc := range cases {
		if got := formatInterval(tc.in); got != tc.want {
			t.Errorf("formatInterval(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestFormatLaunchdScheduleAllBranches(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		doc  plistDoc
		want string
	}{
		{"interval beats everything", plistDoc{StartInterval: 60, RunAtLoad: true}, "every 1m"},
		{"single-dict calendar", plistDoc{StartCalendarInterval: map[string]any{"Hour": 9, "Minute": 0}}, "0 9 * * *"},
		{"single-element array", plistDoc{StartCalendarInterval: []any{map[string]any{"Hour": 17, "Minute": 30}}}, "30 17 * * *"},
		{"multi-element array", plistDoc{StartCalendarInterval: []any{map[string]any{"Hour": 9}, map[string]any{"Hour": 17}}}, "2 triggers"},
		{"empty calendar map", plistDoc{StartCalendarInterval: map[string]any{}, RunAtLoad: true}, "at load"},
		{"empty calendar array", plistDoc{StartCalendarInterval: []any{}, RunAtLoad: true}, "at load"},
		{"only RunAtLoad", plistDoc{RunAtLoad: true}, "at load"},
		{"no schedule keys", plistDoc{}, "on-demand"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := formatLaunchdSchedule(tc.doc); got != tc.want {
				t.Errorf("formatLaunchdSchedule(%+v) = %q, want %q", tc.doc, got, tc.want)
			}
		})
	}
}

// formatCalendar renders cron fields with "*" for unset slots. Each key is
// also accepted as different numeric types depending on plist provenance —
// int, int64, uint64, float64. Dropping any of them silently turns a daily
// 9am job into a pseudo "* * * * *" schedule, which users will read as
// "every minute". Verify each numeric type round-trips.
func TestFormatCalendarAcceptsNumericVariants(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   map[string]any
		want string
	}{
		{"int", map[string]any{"Hour": 9, "Minute": 30}, "30 9 * * *"},
		{"int64", map[string]any{"Hour": int64(9), "Minute": int64(30)}, "30 9 * * *"},
		{"uint64", map[string]any{"Hour": uint64(9), "Minute": uint64(30)}, "30 9 * * *"},
		{"float64", map[string]any{"Hour": float64(9), "Minute": float64(30)}, "30 9 * * *"},
		{"weekday only", map[string]any{"Weekday": 1}, "* * * * 1"},
		{"all five", map[string]any{"Minute": 0, "Hour": 9, "Day": 15, "Month": 6, "Weekday": 1}, "0 9 15 6 1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := formatCalendar(tc.in); got != tc.want {
				t.Errorf("formatCalendar(%v) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// A plist without a <Label> should still parse — we fall back to the
// basename so the user sees *something* in the list. Without this, a missing
// Label silently turned into ID="launchd-test:" and a blank Name column.
func TestLaunchdLabelFallsBackToBasename(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	body := `<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0"><dict>
<key>ProgramArguments</key><array><string>/bin/echo</string></array>
</dict></plist>`
	if err := os.WriteFile(filepath.Join(dir, "fallback.plist"), []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	src := &Launchd{Dir: dir, Tag: "test"}
	jobs, err := src.List(t.Context())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("want 1 job, got %d", len(jobs))
	}
	if jobs[0].Name != "fallback" {
		t.Errorf("label fallback = %q, want %q", jobs[0].Name, "fallback")
	}
	if jobs[0].ID != "launchd-test:fallback" {
		t.Errorf("ID = %q, want launchd-test:fallback", jobs[0].ID)
	}
}

// Disabled=true must surface as Status="disabled" so users see why a job
// they expect isn't running. Naive code that hardcodes "loaded" hides that.
func TestLaunchdDisabledStatus(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	body := `<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0"><dict>
<key>Label</key><string>com.example.off</string>
<key>ProgramArguments</key><array><string>/bin/echo</string></array>
<key>Disabled</key><true/>
</dict></plist>`
	if err := os.WriteFile(filepath.Join(dir, "off.plist"), []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	src := &Launchd{Dir: dir, Tag: "test"}
	jobs, _ := src.List(t.Context())
	if len(jobs) != 1 || jobs[0].Status != "disabled" {
		t.Fatalf("want one disabled job, got %+v", jobs)
	}
}

// A corrupt or truncated plist should be silently skipped — surfacing it as
// an error would break listing for everyone the moment a single bad file
// lands in ~/Library/LaunchAgents (which happens with crashed installers).
func TestLaunchdCorruptPlistIsSkipped(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "good.plist"), []byte(minimalPlist("good", 60)), 0o600); err != nil {
		t.Fatalf("write good: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "corrupt.plist"), []byte("not even xml"), 0o600); err != nil {
		t.Fatalf("write corrupt: %v", err)
	}
	src := &Launchd{Dir: dir, Tag: "test"}
	jobs, err := src.List(t.Context())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(jobs) != 1 || jobs[0].Name != "good" {
		t.Errorf("corrupt plist should be skipped, kept good; got %+v", jobs)
	}
}

// `launchctl list` returns "-" in the PID column for jobs that aren't
// currently running. Atoi("-") errors and we should treat that as PID 0.
// A regression here would crash the source on every launchctl call.
func TestLaunchdEnrichHandlesDashPID(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "com.example.idle.plist"), []byte(minimalPlist("com.example.idle", 60)), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	src := &Launchd{
		Dir: dir,
		Tag: "test",
		Runner: func(_ context.Context, _ []string) ([]byte, error) {
			return []byte("PID\tStatus\tLabel\n-\t78\tcom.example.idle\n"), nil
		},
	}
	jobs, err := src.List(t.Context())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("want 1 job, got %d", len(jobs))
	}
	if jobs[0].PID != 0 {
		t.Errorf("dash PID should yield 0, got %d", jobs[0].PID)
	}
	if jobs[0].LastExitCode != 78 {
		t.Errorf("exit code = %d, want 78", jobs[0].LastExitCode)
	}
	if jobs[0].Status != "exited 78" {
		t.Errorf("status = %q, want %q", jobs[0].Status, "exited 78")
	}
}

// `launchctl list` returns "-" in the Status column for jobs that have
// never run since being loaded. The previous code's `strconv.Atoi("-")`
// silently produced 0, which the renderer turned into "exited 0" — same
// label as a successful exit. Verify "-" now produces a distinct
// "never run" label.
func TestLaunchdEnrichHandlesDashStatus(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "com.example.fresh.plist"), []byte(minimalPlist("com.example.fresh", 60)), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	src := &Launchd{
		Dir: dir,
		Tag: "test",
		Runner: func(_ context.Context, _ []string) ([]byte, error) {
			return []byte("PID\tStatus\tLabel\n-\t-\tcom.example.fresh\n"), nil
		},
	}
	jobs, err := src.List(t.Context())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if jobs[0].Status != "never run" {
		t.Errorf("Status = %q, want never run", jobs[0].Status)
	}
}

// Plist files in the same dir that share a Label must not surface as
// duplicate Job.IDs — listing should remain deterministic.
func TestLaunchdDuplicateLabelsDeduped(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.plist"), []byte(minimalPlist("com.example.dup", 60)), 0o600); err != nil {
		t.Fatalf("write a: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.plist"), []byte(minimalPlist("com.example.dup", 30)), 0o600); err != nil {
		t.Fatalf("write b: %v", err)
	}
	src := &Launchd{Dir: dir, Tag: "test"}
	jobs, err := src.List(t.Context())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(jobs) != 1 {
		t.Errorf("expected 1 job after dedup, got %d: %v", len(jobs), jobs)
	}
}

// renderPlist must XML-escape user-supplied labels and commands so a
// stray `<` doesn't corrupt the file. Verify with characters that XML 1.0
// reserves: `<`, `>`, `&`, `"`, `'`. We assert by parsing the output as
// XML — any leak would cause a parse error, which is the strictest
// possible check.
func TestLaunchdRenderPlistEscapesXML(t *testing.T) {
	t.Parallel()
	body := renderPlist(`eon.tricky&<label>`, `bash -c "echo a < b"`, cron.ScheduleInterval{Descriptor: "hourly"})
	dec := xml.NewDecoder(strings.NewReader(body))
	for {
		_, err := dec.Token()
		if err != nil {
			if err.Error() == "EOF" {
				break
			}
			t.Fatalf("rendered plist is not valid XML: %v\n%s", err, body)
		}
	}
	// Spot-check that the escape sequences ended up in the right places.
	for _, want := range []string{"&amp;", "&lt;label&gt;", "&#34;echo"} {
		if !strings.Contains(body, want) {
			t.Errorf("renderPlist missing escape %q:\n%s", want, body)
		}
	}
}

// formatLaunchdSchedule should treat an empty-dict array `[{}]` as
// "no real trigger" rather than rendering it as "* * * * *", which would
// imply the job fires every minute (the opposite of what launchd does).
func TestFormatLaunchdScheduleSkipsEmptyDictArray(t *testing.T) {
	t.Parallel()
	doc := plistDoc{
		StartCalendarInterval: []any{map[string]any{}},
		RunAtLoad:             true,
	}
	got := formatLaunchdSchedule(doc)
	if got == "* * * * *" {
		t.Errorf("empty-dict array shouldn't render as a real schedule, got %q", got)
	}
	if got != "at load" {
		t.Errorf("expected fallthrough to RunAtLoad, got %q", got)
	}
}

// When BOTH StartInterval and a calendar are present, the schedule label
// should mention both — otherwise the user is misled into thinking the
// agent only fires every interval, missing the calendar triggers.
func TestFormatLaunchdScheduleBothIntervalAndCalendar(t *testing.T) {
	t.Parallel()
	doc := plistDoc{
		StartInterval: 60,
		StartCalendarInterval: []any{
			map[string]any{"Hour": 9},
			map[string]any{"Hour": 17},
		},
	}
	got := formatLaunchdSchedule(doc)
	if !strings.Contains(got, "every 1m") || !strings.Contains(got, "+ 2 cal") {
		t.Errorf("combined schedule wasn't surfaced, got %q", got)
	}
}

// A failing launchctl runner is expected (sandboxed CI, weirdly broken
// install, etc). We must keep the static plist data and just skip enrichment.
func TestLaunchdEnrichRunnerErrorIsTolerated(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "com.example.solo.plist"), []byte(minimalPlist("com.example.solo", 60)), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	src := &Launchd{
		Dir: dir,
		Tag: "test",
		Runner: func(_ context.Context, _ []string) ([]byte, error) {
			return nil, fmt.Errorf("launchctl: permission denied")
		},
	}
	jobs, err := src.List(t.Context())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("static plists should still surface; got %d jobs", len(jobs))
	}
	// Status comes from the plist alone (no Disabled key) — should be "loaded".
	if jobs[0].Status != "loaded" {
		t.Errorf("status = %q, want %q", jobs[0].Status, "loaded")
	}
}

// Malformed enrich rows (fewer than 3 fields) should not panic. The current
// code has `len(fields) < 3` skip, but a regression here would silently
// crash the entire List call.
func TestLaunchdEnrichHandlesMalformedRow(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "com.example.short.plist"), []byte(minimalPlist("com.example.short", 60)), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	src := &Launchd{
		Dir: dir,
		Tag: "test",
		Runner: func(_ context.Context, _ []string) ([]byte, error) {
			// Header, an under-3-field row, and a real row.
			return []byte("PID\tStatus\tLabel\nbroken row\n1\t0\tcom.example.short\n"), nil
		},
	}
	jobs, err := src.List(t.Context())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("malformed row crashed list, got %d jobs", len(jobs))
	}
	if jobs[0].PID != 1 {
		t.Errorf("real row should have enriched, got PID %d", jobs[0].PID)
	}
}

// `launchctl unload` returns non-zero with "Could not find specified service"
// when the agent isn't loaded. The default runner swallows that; verify the
// Delete path works even when the runner returns that exact message.
func TestLaunchdDeleteIgnoresUnloadFailure(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	plistPath := filepath.Join(dir, "com.example.zombie.plist")
	if err := os.WriteFile(plistPath, []byte(minimalPlist("com.example.zombie", 60)), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	calls := [][]string{}
	src := &Launchd{
		Dir: dir,
		Tag: "test",
		Runner: func(_ context.Context, args []string) ([]byte, error) {
			calls = append(calls, args)
			return []byte("Could not find specified service"), fmt.Errorf("exit 113")
		},
	}
	if err := src.Delete(t.Context(), "launchd-test:com.example.zombie"); err != nil {
		t.Errorf("delete should still succeed when unload fails (best-effort): %v", err)
	}
	if _, err := os.Stat(plistPath); !os.IsNotExist(err) {
		t.Errorf("plist should have been removed, got %v", err)
	}
}

// ID without the source's "launchd-<tag>:" prefix should return ErrNotFound
// rather than mutating anything. The Manager fan-out depends on this so
// other sources get a chance to claim the ID.
func TestLaunchdDeleteForeignIDReturnsNotFound(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "com.example.untouchable.plist"), []byte(minimalPlist("com.example.untouchable", 60)), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	src := &Launchd{Dir: dir, Tag: "test"}
	err := src.Delete(t.Context(), "launchd-other:com.example.untouchable")
	if !errors.Is(err, cron.ErrNotFound) {
		t.Errorf("foreign-prefix ID should return ErrNotFound, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "com.example.untouchable.plist")); statErr != nil {
		t.Errorf("plist mutated by foreign ID: %v", statErr)
	}
}

// NewUser builds a source pointed at ~/Library/LaunchAgents. We don't test
// the actual home dir, just that the constructor returns a reasonable Tag
// and a non-nil Runner so List won't no-op on real systems.
func TestLaunchdAddWritesPlist(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	src := &Launchd{Dir: dir, Tag: "test"}
	j, err := src.Add(t.Context(), cron.JobSpec{Schedule: "@every 5m", Command: "/usr/bin/echo hi"})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if !strings.HasPrefix(j.ID, "launchd-test:eon.") {
		t.Errorf("returned ID = %q", j.ID)
	}
	files, _ := os.ReadDir(dir)
	if len(files) != 1 {
		t.Fatalf("want 1 plist, got %d", len(files))
	}
	body, _ := os.ReadFile(filepath.Join(dir, files[0].Name()))
	if !strings.Contains(string(body), "<integer>300</integer>") {
		t.Errorf("StartInterval not 300 seconds:\n%s", body)
	}
}

func TestLaunchdAddRejectsBadSpec(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	src := &Launchd{Dir: dir, Tag: "test"}
	cases := []cron.JobSpec{
		{Schedule: "", Command: "/bin/x"},
		{Schedule: "@every 5m", Command: ""},
		{Schedule: "@every 5m", Command: "/bin/x\nrm -rf /"},
		{Schedule: "*/5 * * * *", Command: "/bin/x"}, // 5-field cron unsupported here
		{Schedule: "@every notaduration", Command: "/bin/x"},
	}
	for _, spec := range cases {
		if _, err := src.Add(t.Context(), spec); err == nil {
			t.Errorf("expected validation error for %+v", spec)
		}
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("validation failures shouldn't write to disk; got %v", entries)
	}
}

func TestLaunchdAddRejectsDuplicate(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	src := &Launchd{Dir: dir, Tag: "test"}
	spec := cron.JobSpec{Schedule: "@hourly", Command: "/bin/echo same"}
	if _, err := src.Add(t.Context(), spec); err != nil {
		t.Fatalf("first add: %v", err)
	}
	if _, err := src.Add(t.Context(), spec); err == nil {
		t.Errorf("second add of same command should error (already exists)")
	}
}

func TestLaunchdEditRewritesPlist(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	src := &Launchd{Dir: dir, Tag: "test"}
	j, err := src.Add(t.Context(), cron.JobSpec{Schedule: "@every 5m", Command: "/usr/bin/echo hi"})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	updated, err := src.Edit(t.Context(), j.ID, cron.JobSpec{Schedule: "@hourly", Command: "/usr/bin/echo new"})
	if err != nil {
		t.Fatalf("edit: %v", err)
	}
	if updated.Schedule != "every 1h" {
		t.Errorf("schedule render after edit = %q, want 'every 1h'", updated.Schedule)
	}
	if !strings.Contains(updated.Command, "/usr/bin/echo new") {
		t.Errorf("command not updated: %q", updated.Command)
	}
}

func TestLaunchdEditUnknownIDIsNotFound(t *testing.T) {
	t.Parallel()
	src := &Launchd{Dir: t.TempDir(), Tag: "test"}
	_, err := src.Edit(t.Context(), "launchd-test:ghost", cron.JobSpec{Schedule: "@hourly", Command: "/bin/x"})
	if !errors.Is(err, cron.ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

func TestLaunchdReadOnlyRejectsAdd(t *testing.T) {
	t.Parallel()
	src := &Launchd{Dir: t.TempDir(), Tag: "system", ReadOnly: true}
	_, err := src.Add(t.Context(), cron.JobSpec{Schedule: "@hourly", Command: "/bin/x"})
	if err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Errorf("read-only Add must reject, got %v", err)
	}
}

func TestNewUserConstructor(t *testing.T) {
	t.Parallel()
	src, err := NewUser()
	if err != nil {
		t.Fatalf("NewUser: %v", err)
	}
	if src.Tag != "user" {
		t.Errorf("Tag = %q, want user", src.Tag)
	}
	if src.Runner == nil {
		t.Errorf("NewUser must set Runner so enrich runs on real installs")
	}
	if !strings.HasSuffix(src.Dir, "/Library/LaunchAgents") {
		t.Errorf("NewUser Dir = %q; expected to end with /Library/LaunchAgents", src.Dir)
	}
}

func TestNewSystemConstructor(t *testing.T) {
	t.Parallel()
	src := NewSystem()
	if !src.ReadOnly {
		t.Errorf("system source must be read-only")
	}
	if src.Dir != "/Library/LaunchAgents" {
		t.Errorf("system source Dir = %q", src.Dir)
	}
	if src.Scope() != cron.ScopeSystem {
		t.Errorf("scope = %v, want system", src.Scope())
	}
}

// formatCalendar's default branch handles types our schema doesn't pre-list.
// Pass a string and verify it survives stringification (Sprintf %v) — keeps
// rendering predictable rather than panicking on a surprise type.
func TestFormatCalendarUnknownType(t *testing.T) {
	t.Parallel()
	got := formatCalendar(map[string]any{"Hour": "morning"})
	if !strings.Contains(got, "morning") {
		t.Errorf("expected unknown-type fallthrough to keep raw value; got %q", got)
	}
}

func TestLaunchdNameAndScope(t *testing.T) {
	t.Parallel()
	user := &Launchd{Tag: "user"}
	if user.Name() != "launchd-user" {
		t.Errorf("user.Name() = %q", user.Name())
	}
	if user.Scope() != cron.ScopeUser {
		t.Errorf("user.Scope() = %v, want %v", user.Scope(), cron.ScopeUser)
	}
	system := &Launchd{Tag: "system", ReadOnly: true}
	if system.Scope() != cron.ScopeSystem {
		t.Errorf("system.Scope() = %v, want %v", system.Scope(), cron.ScopeSystem)
	}
}

func TestLaunchdEnrichWithFakeRunner(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "com.example.enrich.plist"), []byte(minimalPlist("com.example.enrich", 60)), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	src := &Launchd{
		Dir: dir,
		Tag: "test",
		Runner: func(_ context.Context, args []string) ([]byte, error) {
			return []byte("PID\tStatus\tLabel\n1234\t0\tcom.example.enrich\n"), nil
		},
	}
	jobs, err := src.List(t.Context())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("want 1 job, got %d", len(jobs))
	}
	if jobs[0].PID != 1234 {
		t.Errorf("enrich did not pick up PID: %+v", jobs[0])
	}
	if jobs[0].Status != "running" {
		t.Errorf("status should be 'running', got %q", jobs[0].Status)
	}
}
