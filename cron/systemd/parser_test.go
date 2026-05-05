package systemd

import (
	"strings"
	"testing"
	"time"

	"github.com/rednafi/eon/cron"
)

func TestParseUnitBasicKeyVal(t *testing.T) {
	t.Parallel()
	got := parseUnit(`[Service]
ExecStart=/bin/echo hi
User=root
`)
	if got["Service.ExecStart"] != "/bin/echo hi" {
		t.Errorf("ExecStart = %q", got["Service.ExecStart"])
	}
	if got["Service.User"] != "root" {
		t.Errorf("User = %q", got["Service.User"])
	}
}

func TestParseUnitIgnoresHashAndSemicolonComments(t *testing.T) {
	t.Parallel()
	got := parseUnit(`# top hash
; top semicolon
[Service]
# inside hash
; inside semicolon
ExecStart=/bin/yes
`)
	if got["Service.ExecStart"] != "/bin/yes" {
		t.Errorf("ExecStart = %q", got["Service.ExecStart"])
	}
	if len(got) != 1 {
		t.Errorf("expected exactly 1 key, got %d: %v", len(got), got)
	}
}

func TestParseUnitMultiSection(t *testing.T) {
	t.Parallel()
	got := parseUnit(`[Unit]
Description=Demo

[Timer]
OnCalendar=hourly

[Install]
WantedBy=timers.target
`)
	for k, want := range map[string]string{
		"Unit.Description": "Demo",
		"Timer.OnCalendar": "hourly",
		"Install.WantedBy": "timers.target",
	} {
		if got[k] != want {
			t.Errorf("%s = %q, want %q", k, got[k], want)
		}
	}
}

// A re-entered section header must take precedence: systemd allows the same
// section to appear twice (parents reopen [Service] in drop-ins) and the
// last-write wins. Our parser keeps that semantic implicitly because it
// overwrites map keys.
func TestParseUnitSectionOverride(t *testing.T) {
	t.Parallel()
	got := parseUnit(`[Service]
ExecStart=/bin/old

[Service]
ExecStart=/bin/new
`)
	if got["Service.ExecStart"] != "/bin/new" {
		t.Errorf("late-write loss: got %q", got["Service.ExecStart"])
	}
}

// Lines with no '=' or with '=' at column 0 should be skipped silently —
// systemd unit files in the wild sometimes have decorative banners or
// commented-out blocks.
func TestParseUnitSkipsKeylessAndEmptyLines(t *testing.T) {
	t.Parallel()
	got := parseUnit(`[Service]
=novalue
ExecStart=/bin/foo

bareword
`)
	if got["Service.ExecStart"] != "/bin/foo" {
		t.Errorf("ExecStart = %q", got["Service.ExecStart"])
	}
	for k := range got {
		if k == "Service." || k == "Service.bareword" {
			t.Errorf("keyless line %q got captured", k)
		}
	}
}

// Values that contain '=' should keep everything after the first '='.
// Common in EnvironmentFile=KEY=VALUE style entries.
func TestParseUnitEqualInValue(t *testing.T) {
	t.Parallel()
	got := parseUnit(`[Service]
Environment=PATH=/usr/local/bin:/usr/bin
`)
	if got["Service.Environment"] != "PATH=/usr/local/bin:/usr/bin" {
		t.Errorf("Environment = %q", got["Service.Environment"])
	}
}

// Bracketed section headers with surrounding whitespace must still be
// recognised — the parser TrimSpace's the line first, so [Service]
// indented in a drop-in unit will work too.
func TestParseUnitTrimsSurroundingWhitespace(t *testing.T) {
	t.Parallel()
	got := parseUnit("  [Service]  \n  ExecStart=/bin/foo  \n")
	if got["Service.ExecStart"] != "/bin/foo" {
		t.Errorf("trimmed indent should still parse, got %q", got["Service.ExecStart"])
	}
}

// Keys outside any section land in the empty section. systemd would reject
// such a unit, but the parser shouldn't crash — and using the unit map
// keyed under "." lets a caller spot the malformed file by inspection.
func TestParseUnitOutsideAnySection(t *testing.T) {
	t.Parallel()
	got := parseUnit("Orphan=true\n[Service]\nExecStart=/bin/foo\n")
	if got[".Orphan"] != "true" {
		t.Errorf("orphan key not preserved under empty section: %v", got)
	}
}

// FuzzParseUnit asserts the unit parser is total. Surface-area is small
// (it only cares about line-shape) but it's invoked on user-controlled
// content (drop-in service files), so worth fuzzing.
func FuzzParseUnit(f *testing.F) {
	for _, seed := range []string{
		"",
		"[Service]\nExecStart=/bin/foo\n",
		"# comment\n; another\n",
		"[Unit]\n[Service]\n[Install]\n",
		"orphan=value\n",
		"=novalue\n",
		"[no-close-bracket\n",
		"section]without-open\n",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, content string) {
		_ = parseUnit(content) // must not panic
	})
}

// parseUnitMulti must collect every value for a repeated key in source
// order. systemd allows multiple OnCalendar= lines; the renderer needs
// the count so the user sees "+N more" rather than just the last one.
func TestParseUnitMultiCollectsRepeatedKeys(t *testing.T) {
	t.Parallel()
	got, err := parseUnitMulti(`[Timer]
OnCalendar=hourly
OnCalendar=daily
OnCalendar=Mon
`)
	if err != nil {
		t.Fatalf("parseUnitMulti: %v", err)
	}
	vs := got["Timer.OnCalendar"]
	if len(vs) != 3 {
		t.Fatalf("want 3 OnCalendar values, got %d: %v", len(vs), vs)
	}
	for i, want := range []string{"hourly", "daily", "Mon"} {
		if vs[i] != want {
			t.Errorf("position %d = %q, want %q", i, vs[i], want)
		}
	}
}

// systemd unit files saved by editors that prepend a UTF-8 BOM
// (U+FEFF) must still parse — the BOM shouldn't bleed into the section
// name or first key.
func TestParseUnitStripsUTF8BOM(t *testing.T) {
	t.Parallel()
	got := parseUnit("\uFEFF[Service]\nExecStart=/bin/foo\n")
	if got["Service.ExecStart"] != "/bin/foo" {
		t.Errorf("BOM bled into key/section: %v", got)
	}
}

// validateSpec is shared with the Linux Add/Edit flow. The pure-function
// tests here run on every platform.
func TestValidateSpec(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		spec cron.JobSpec
		fail bool
	}{
		{"happy", cron.JobSpec{Schedule: "@hourly", Command: "/bin/x"}, false},
		{"empty schedule", cron.JobSpec{Schedule: "", Command: "/bin/x"}, true},
		{"whitespace schedule", cron.JobSpec{Schedule: "   ", Command: "/bin/x"}, true},
		{"empty command", cron.JobSpec{Schedule: "@hourly", Command: ""}, true},
		{"command with newline", cron.JobSpec{Schedule: "@hourly", Command: "/bin/x\nbad"}, true},
		{"command with CR", cron.JobSpec{Schedule: "@hourly", Command: "/bin/x\rbad"}, true},
	}
	for _, tc := range cases {
		err := cron.ValidateSpec(tc.spec)
		if tc.fail && err == nil {
			t.Errorf("%s: expected error", tc.name)
		}
		if !tc.fail && err != nil {
			t.Errorf("%s: unexpected error: %v", tc.name, err)
		}
	}
}

func TestSystemdLabel(t *testing.T) {
	t.Parallel()
	cases := []struct{ command, want string }{
		{"/bin/echo hi", "eon-echo"},
		{"/usr/local/bin/run --flag", "eon-run"},
		{"FOO=bar /bin/x", "eon-x"},
		{"", "eon-job"},
	}
	for _, tc := range cases {
		if got := systemdLabel(tc.command); got != tc.want {
			t.Errorf("systemdLabel(%q) = %q, want %q", tc.command, got, tc.want)
		}
	}
}

// renderTimer must produce valid INI-shaped systemd output. We don't
// depend on systemd-analyze here; spot-check a few invariants.
func TestRenderTimerProducesValidUnit(t *testing.T) {
	t.Parallel()
	got := renderTimer("eon-test", 5*time.Minute, "")
	if !strings.Contains(got, "[Timer]") || !strings.Contains(got, "OnUnitActiveSec=5m0s") {
		t.Errorf("interval timer body wrong:\n%s", got)
	}
	got = renderTimer("eon-test", 0, "daily")
	if !strings.Contains(got, "OnCalendar=daily") {
		t.Errorf("descriptor timer body wrong:\n%s", got)
	}
}

func TestRenderServiceProducesValidUnit(t *testing.T) {
	t.Parallel()
	got := renderService("eon-test", "/bin/echo hi")
	if !strings.Contains(got, "[Service]") || !strings.Contains(got, "ExecStart=/bin/echo hi") {
		t.Errorf("service body wrong:\n%s", got)
	}
}

func TestPrefixedAddsPrefixWhenNonEmpty(t *testing.T) {
	t.Parallel()
	if got := prefixed("every ", "5min"); got != "every 5min" {
		t.Errorf("got %q", got)
	}
	if got := prefixed("every ", ""); got != "" {
		t.Errorf("empty input should produce empty output, got %q", got)
	}
}
