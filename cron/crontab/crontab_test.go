package crontab

import (
	"context"
	"strings"
	"testing"

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
	f.calls = append(f.calls, append([]string(nil), args...))
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
	if err := c.Delete(t.Context(), "crontab:deadbeef"); err != cron.ErrNotFound {
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
	cases := []struct {
		in       string
		schedule string
		command  string
		ok       bool
	}{
		{"*/5 * * * * /bin/foo", "*/5 * * * *", "/bin/foo", true},
		{"  0  9 *  *  1   /bin/foo --x", "0 9 * * 1", "/bin/foo --x", true},
		{"@daily /bin/foo", "@daily", "/bin/foo", true},
		{"@reboot", "", "", false},
		{"too few", "", "", false},
	}
	for _, tc := range cases {
		s, c, ok := splitCrontabLine(tc.in)
		if ok != tc.ok || s != tc.schedule || c != tc.command {
			t.Errorf("split(%q) = (%q,%q,%v), want (%q,%q,%v)", tc.in, s, c, ok, tc.schedule, tc.command, tc.ok)
		}
	}
}

func TestCommandShortName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"/usr/bin/echo hi", "echo"},
		{"PATH=/x:/y /usr/local/bin/run", "run"},
		{"foo bar", "foo"},
		{"", ""},
	}
	for _, tc := range cases {
		if got := cron.CommandShortName(tc.in); got != tc.want {
			t.Errorf("cron.CommandShortName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
