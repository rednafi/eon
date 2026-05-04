package tui

import (
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// wrap reflows multi-line text to fit width while preserving ANSI styling.
// We delegate to lipgloss.Wrap, which handles the cell-counting that a naive
// bytecount-based wrap would get wrong for unicode and styled output.
func wrap(s string, width int) string {
	if width <= 0 {
		return s
	}
	return lipgloss.Wrap(s, width, "")
}

// truncateMiddle shortens s to maxWidth (display cells) by replacing the
// middle with "…". Eliding the middle keeps the source-prefix and label
// suffix visible, which is what users care about for IDs like
// "launchd-user:com.foo.really.long.identifier".
func truncateMiddle(s string, maxWidth int) string {
	if maxWidth <= 1 || ansi.StringWidth(s) <= maxWidth {
		return s
	}
	keep := maxWidth - 1
	left := keep / 2
	right := keep - left
	return ansi.Truncate(s, left, "") + "…" + ansi.TruncateLeft(s, ansi.StringWidth(s)-right, "")
}
