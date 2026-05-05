// Pure-parser tests for the launchd backend. No build tag — these run on
// every platform so the parser is exercised in CI on Linux as well as
// macOS.

package launchd

import (
	"bytes"
	"strings"
	"testing"
	"unicode/utf8"

	"howett.net/plist"

	"github.com/rednafi/eon/cron"
)

func TestParsePlistMinimum(t *testing.T) {
	t.Parallel()
	body := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0"><dict>
<key>Label</key><string>com.foo.bar</string>
<key>ProgramArguments</key><array><string>/bin/echo</string></array>
<key>StartInterval</key><integer>60</integer>
</dict></plist>`)
	j, err := parsePlist(body, "user", "/tmp/com.foo.bar.plist")
	if err != nil {
		t.Fatalf("parsePlist: %v", err)
	}
	if j.ID != "launchd-user:com.foo.bar" {
		t.Errorf("ID=%q want launchd-user:com.foo.bar", j.ID)
	}
	if j.Name != "com.foo.bar" || j.Command != "/bin/echo" {
		t.Errorf("name/cmd: %+v", j)
	}
	if j.Schedule != "every 1m" {
		t.Errorf("Schedule=%q", j.Schedule)
	}
}

// FuzzParsePlist asserts the plist decoder + Job-construction code never
// panics on arbitrary bytes. The vast majority of inputs will fail the
// XML decode and return an error, which is fine — what we're guarding
// against is a corrupt plist on disk crashing eon at startup.
func FuzzParsePlist(f *testing.F) {
	for _, seed := range [][]byte{
		[]byte(`<?xml version="1.0"?><plist version="1.0"><dict></dict></plist>`),
		[]byte(`<?xml version="1.0"?><plist><dict><key>Label</key><string>x</string></dict></plist>`),
		[]byte(``),
		[]byte(`not-xml-at-all`),
		[]byte("\x00\x00\x00\x00"),
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		j, err := parsePlist(raw, "user", "/tmp/x.plist")
		if err != nil {
			return
		}
		if !strings.HasPrefix(j.ID, "launchd-user:") {
			t.Errorf("malformed ID: %q", j.ID)
		}
		if j.Kind != cron.KindLaunchd {
			t.Errorf("Kind=%v want launchd", j.Kind)
		}
	})
}

// hasInvalidXMLChar returns true if s contains any byte that XML 1.0
// rejects in character data. Used by FuzzRenderPlistRoundTrips to drop
// invalid inputs the renderer's contract doesn't cover.
func hasInvalidXMLChar(s string) bool {
	for _, r := range s {
		switch {
		case r == '\t' || r == '\n' || r == '\r':
			continue
		case r < 0x20:
			return true
		}
	}
	return false
}

// FuzzRenderPlistRoundTrips checks that any rendered plist parses back to
// itself. This catches XML-escaping regressions: a label or command
// containing `<`, `&`, or `'` would otherwise corrupt the plist body and
// silently fail at load time.
func FuzzRenderPlistRoundTrips(f *testing.F) {
	for _, seed := range []struct {
		label, cmd string
		secs       int
	}{
		{"plain", "/bin/echo hi", 60},
		{"x&y", "/bin/echo 'hi & bye'", 3600},
		{"<weird>", `/bin/sh -c "echo <world>"`, 86400},
	} {
		f.Add(seed.label, seed.cmd, seed.secs)
	}
	f.Fuzz(func(t *testing.T, label, cmd string, secs int) {
		// Plist labels and commands must be valid UTF-8 — XML 1.0
		// doesn't permit raw bytes outside the Unicode range, and
		// xml.EscapeText would substitute U+FFFD. Validation upstream
		// (the CLI/TUI form) is responsible for rejecting bad input;
		// the renderer is only required to round-trip *valid* input.
		if label == "" || cmd == "" {
			return
		}
		if !utf8.ValidString(label) || !utf8.ValidString(cmd) {
			return
		}
		// XML 1.0 only permits #x9, #xA, #xD, and #x20+ in character
		// data; any other control byte gets rewritten by xml.EscapeText
		// to U+FFFD. Labels and commands containing those bytes are
		// upstream-validation territory; the renderer is only obliged
		// to round-trip *valid* XML char data.
		if hasInvalidXMLChar(label) || hasInvalidXMLChar(cmd) {
			return
		}
		body := renderPlist(label, cmd, cron.ScheduleInterval{Descriptor: "daily"})
		var doc plistDoc
		if err := plist.NewDecoder(bytes.NewReader([]byte(body))).Decode(&doc); err != nil {
			t.Fatalf("rendered plist failed to decode: %v\n---\n%s", err, body)
		}
		if doc.Label != label {
			t.Errorf("Label=%q want %q", doc.Label, label)
		}
	})
}
