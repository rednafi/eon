// Pure parsing helpers for systemd unit files. Lives outside the linux
// build tag so the parser can be unit-tested on every platform — only the
// systemctl-driving Source itself is linux-only.

package systemd

import (
	"bufio"
	"fmt"
	"strings"
	"time"

	"github.com/rednafi/eon/cron"
)

// utf8BOM is the byte-order mark some editors prepend to UTF-8 files.
// strings.TrimSpace doesn't remove it, so we strip it explicitly per line
// to handle files saved by Notepad or similar.
const utf8BOM = "\uFEFF"

// parseUnit reads a systemd unit file into a flat map keyed by "Section.Key".
// Multi-line values and continuations aren't supported — eon doesn't need
// them, and ignoring them keeps the parser tiny and predictable.
//
// When the same key appears more than once in the same section (e.g.
// systemd allows multiple OnCalendar= lines, each adding a trigger), the
// last write wins in the returned map. Use parseUnitMulti when callers
// need to know how many copies appeared.
func parseUnit(content string) map[string]string {
	flat, _ := parseUnitMulti(content)
	out := make(map[string]string, len(flat))
	for k, v := range flat {
		if len(v) > 0 {
			out[k] = v[len(v)-1]
		}
	}
	return out
}

// parseUnitMulti is the multi-valued sibling of parseUnit. Each key maps
// to the slice of values seen, in source order. The second return value
// is any scanner error (typically bufio.ErrTooLong on lines > 1MB) so
// callers can surface "your unit file was truncated" instead of silently
// returning a partial parse.
func parseUnitMulti(content string) (map[string][]string, error) {
	out := map[string][]string{}
	scanner := bufio.NewScanner(strings.NewReader(content))
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	section := ""
	for scanner.Scan() {
		line := strings.TrimSpace(strings.TrimPrefix(scanner.Text(), utf8BOM))
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.TrimSuffix(strings.TrimPrefix(line, "["), "]")
			continue
		}
		if i := strings.Index(line, "="); i > 0 {
			k := strings.TrimSpace(line[:i])
			v := strings.TrimSpace(line[i+1:])
			key := section + "." + k
			out[key] = append(out[key], v)
		}
	}
	return out, scanner.Err()
}

// prefixed returns p+s when s is non-empty, "" otherwise. Lets cmp.Or
// chains express conditional fallbacks ("every <v>" only if v is set).
func prefixed(p, s string) string {
	if s == "" {
		return ""
	}
	return p + s
}

// validateSpec rejects obviously-broken inputs before the imperative
// shell touches disk. Pure — testable on every platform.
func validateSpec(spec cron.JobSpec) error {
	if strings.TrimSpace(spec.Schedule) == "" {
		return fmt.Errorf("schedule must not be empty")
	}
	if strings.TrimSpace(spec.Command) == "" {
		return fmt.Errorf("command must not be empty")
	}
	if strings.ContainsAny(spec.Command, "\r\n") {
		return fmt.Errorf("command must not contain newlines")
	}
	return nil
}

// systemdLabel derives a label from a command, prefixed with "eon-" so the
// source of an eon-created unit is obvious in `systemctl list-timers`.
func systemdLabel(command string) string {
	short := cron.CommandShortName(command)
	short = strings.ReplaceAll(short, "/", "-")
	if short == "" {
		short = "job"
	}
	return "eon-" + short
}

// renderTimer emits a minimal [Unit]+[Timer]+[Install] body. Pure: takes
// label + interval, returns the unit text. Linux Source uses it; tests
// can call it on any platform.
func renderTimer(label string, every time.Duration, descriptor string) string {
	var sched string
	switch {
	case every > 0:
		sched = fmt.Sprintf("OnUnitActiveSec=%s\nOnBootSec=%s", every, every)
	case descriptor != "":
		sched = "OnCalendar=" + descriptor
	}
	return fmt.Sprintf(`[Unit]
Description=eon-managed timer for %s

[Timer]
%s
Persistent=true

[Install]
WantedBy=timers.target
`, label, sched)
}

// renderService emits the matching .service body for a Timer.
func renderService(label, command string) string {
	return fmt.Sprintf(`[Unit]
Description=eon-managed service for %s

[Service]
Type=oneshot
ExecStart=%s
`, label, command)
}
