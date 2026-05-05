// Pure parsing helpers for systemd unit files. Lives outside the linux
// build tag so the parser can be unit-tested on every platform — only the
// systemctl-driving Source itself is linux-only.

package systemd

import (
	"bufio"
	"strings"
)

// utf8BOM is the byte-order mark some editors prepend to UTF-8 files.
// strings.TrimSpace doesn't remove it, so we strip it explicitly per line
// to handle files saved by Notepad or similar.
const utf8BOM = "\uFEFF"

// parseUnit reads a systemd unit file into a flat map keyed by "Section.Key".
// Multi-line values and continuations aren't supported — eon doesn't need
// them, and ignoring them keeps the parser tiny and predictable.
func parseUnit(content string) map[string]string {
	out := map[string]string{}
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
			out[section+"."+k] = v
		}
	}
	return out
}

// prefixed returns p+s when s is non-empty, "" otherwise. Lets cmp.Or
// chains express conditional fallbacks ("every <v>" only if v is set).
func prefixed(p, s string) string {
	if s == "" {
		return ""
	}
	return p + s
}
