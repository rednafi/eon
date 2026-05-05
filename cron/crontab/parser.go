// Pure parsing helpers for the user crontab. No syscall, no Runner: the
// functional core that the imperative Source in crontab.go delegates to.
// Living in its own file makes it obvious what's testable in isolation
// (everything here) versus what needs a process boundary (the methods
// in crontab.go that shell out to the `crontab` binary).

package crontab

import (
	"strings"
)

// utf8BOM is the byte-order mark some editors prepend to UTF-8 files.
// strings.TrimSpace doesn't remove it, so we strip it explicitly per
// line.
const utf8BOM = "\uFEFF"

// splitCrontabLine separates the schedule expression from the command.
// Supports both 5-field and descriptor (@daily, @reboot, ...) syntax.
// Tabs are accepted as field separators alongside spaces (man 5
// crontab); a leading UTF-8 BOM is stripped so files saved by Notepad
// don't poison the first line.
func splitCrontabLine(line string) (schedule, command string, ok bool) {
	line = strings.TrimSpace(strings.TrimPrefix(line, utf8BOM))
	if strings.HasPrefix(line, "@") {
		// "@daily<sep>cmd" — split on first whitespace run, space OR tab.
		i := strings.IndexAny(line, " \t")
		if i < 0 {
			return "", "", false
		}
		cmd := strings.TrimSpace(line[i:])
		if cmd == "" {
			return "", "", false
		}
		return line[:i], cmd, true
	}
	// 5 fields then command. Fields can contain commas/dashes/slashes but
	// not spaces, so a simple field-walk is sufficient. We must use a C-
	// style loop here — `for i := range len(line)` ignores mutations to
	// i, which we rely on for the whitespace skip.
	fields := 0
	for i := 0; i < len(line); i++ {
		if line[i] != ' ' && line[i] != '\t' {
			continue
		}
		j := i
		for j < len(line) && (line[j] == ' ' || line[j] == '\t') {
			j++
		}
		fields++
		if fields == 5 {
			return strings.Join(strings.Fields(line[:i]), " "), strings.TrimSpace(line[j:]), true
		}
		i = j - 1
	}
	return "", "", false
}
