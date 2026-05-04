package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// wrap fits multi-line text inside a column of the given width. Empty lines
// are preserved (so paragraph spacing survives) and individual long words are
// hard-wrapped rather than truncated, because a truncated PATH or URL would
// hide the very thing a user came here to read.
func wrap(s string, width int) string {
	if width <= 0 {
		return s
	}
	var out strings.Builder
	lines := strings.Split(s, "\n")
	for li, line := range lines {
		if line == "" {
			if li < len(lines)-1 {
				out.WriteString("\n")
			}
			continue
		}
		// lipgloss has its own width-aware renderer; using it here means we
		// stay consistent with how the rest of the UI counts cells (handles
		// ANSI escapes and double-width runes correctly).
		wrapped := lipgloss.NewStyle().Width(width).Render(line)
		out.WriteString(wrapped)
		if li < len(lines)-1 {
			out.WriteString("\n")
		}
	}
	return out.String()
}

// truncateMiddle shortens s to maxWidth by replacing the middle with "…". We
// elide in the middle (rather than the end) so both the source-prefix and the
// label tail of an ID stay visible: "launchd-user:com.…some.long.label" is
// more informative than "launchd-user:com.examp…".
func truncateMiddle(s string, maxWidth int) string {
	if maxWidth <= 1 || len(s) <= maxWidth {
		return s
	}
	keep := maxWidth - 1
	left := keep / 2
	right := keep - left
	return s[:left] + "…" + s[len(s)-right:]
}
