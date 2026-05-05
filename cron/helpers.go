package cron

import (
	"bufio"
	"crypto/sha1"
	"encoding/hex"
	"strings"
)

// ShortHash returns a stable 8-hex-char fingerprint of s. Sources use it
// for Job IDs that need to survive reordering of unrelated lines (crontab
// rewrites, cron.d drop-ins). Exported so every backend computes IDs the
// same way and the CLI/TUI can rely on shape.
func ShortHash(s string) string {
	sum := sha1.Sum([]byte(s))
	return hex.EncodeToString(sum[:4])
}

// LineScanner returns a bufio.Scanner over s with the buffer pre-sized to
// 1 MB. Every backend's parser tokenises configuration files line-by-line
// and the default 64 KB buffer truncates pathological-but-real entries
// (long PATH= preludes in /etc/crontab, multi-arg ProgramArguments). One
// helper means one place to bump the cap.
func LineScanner(s string) *bufio.Scanner {
	const maxLine = 1 << 20
	scanner := bufio.NewScanner(strings.NewReader(s))
	scanner.Buffer(make([]byte, maxLine), maxLine)
	return scanner
}

// CommandShortName returns a readable label for a shell command: the
// basename of the first non-assignment token. Sources use it to populate
// Job.Name when no native label exists (crontab lines, cron.d entries).
//
// A trailing slash on the path (e.g. "/usr/local/bin/") would slice past
// the end of the string in a naive LastIndex implementation; we trim it
// first so the label falls back to the parent segment ("bin") rather than
// the empty string.
func CommandShortName(cmd string) string {
	for tok := range strings.FieldsSeq(cmd) {
		if strings.Contains(tok, "=") {
			continue
		}
		tok = strings.TrimRight(tok, "/")
		if tok == "" {
			continue
		}
		if i := strings.LastIndex(tok, "/"); i >= 0 {
			return tok[i+1:]
		}
		return tok
	}
	return cmd
}

// UTF8BOM is the byte-order mark some editors prepend to UTF-8 files.
// Backend parsers strip it per-line because strings.TrimSpace doesn't
// remove it.
const UTF8BOM = "\uFEFF"

// LabelFromCommand returns prefix + base, where base is
// CommandShortName(cmd) with slashes replaced by dashes (or fallback if
// the command yields nothing). Used by launchd and systemd to derive
// matching label conventions.
func LabelFromCommand(cmd, prefix, fallback string) string {
	short := strings.ReplaceAll(CommandShortName(cmd), "/", "-")
	if short == "" {
		short = fallback
	}
	return prefix + short
}
