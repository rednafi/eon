package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
)

const (
	headerHeight = 5 // info + keymap row, including borders
	statusHeight = 1
	// Panel chrome: 1 border + 1 padding on each side horizontally; 1 border
	// each side vertically.
	panelChromeX = 4
	panelChromeY = 2
)

// tableCols is the canonical column order. renderListPanel and the cell
// projection in state.go both index into this — a drift between the two
// would panic on the first cell.
var tableCols = []string{"ID", "SCOPE", "KIND", "NAME", "SCHEDULE", "STATUS"}

// computeColumnWidths sizes each column to its widest cell, then squeezes
// ID → NAME → KIND if the row exceeds the available width. SCOPE/SCHEDULE/
// STATUS are short and most informative so they keep their natural width.
// The caller projects jobs into [6]string rows so this stays a pure layout
// helper with no cron dependency.
func computeColumnWidths(headers []string, rows [][6]string, available int) []int {
	widths := make([]int, len(headers))
	for i, h := range headers {
		widths[i] = len(h)
	}
	for _, row := range rows {
		for i, c := range row {
			if len(c) > widths[i] {
				widths[i] = len(c)
			}
		}
	}
	gutter := 2 * (len(headers) - 1)
	used := gutter
	for _, w := range widths {
		used += w
	}
	for _, idx := range []int{0, 3, 2} {
		if used <= available {
			break
		}
		over := used - available
		shrink := over
		if widths[idx]-shrink < 8 {
			shrink = max(0, widths[idx]-8)
		}
		widths[idx] -= shrink
		used -= shrink
	}
	return widths
}

// renderRow joins cells into one space-padded line. Per-cell styles in
// overrides take precedence over base; nil entries fall back to base. Padding
// is measured against rendered cell width so ANSI codes don't shift columns.
func renderRow(cells []string, widths []int, base *lipgloss.Style, overrides []*lipgloss.Style) string {
	// Pre-size the builder: sum(widths) + 2-cell gutters + slack for ANSI
	// escape sequences. Without Grow the builder doubles 2-3 times per
	// row; for a TUI redrawing on every keystroke this matters.
	total := 2 * (len(cells) - 1)
	for _, w := range widths {
		total += w
	}
	var b strings.Builder
	b.Grow(total + 32)
	for i, c := range cells {
		text := truncateMiddle(c, widths[i])
		var s *lipgloss.Style
		if i < len(overrides) && overrides[i] != nil {
			s = overrides[i]
		} else {
			s = base
		}
		styled := text
		if s != nil {
			styled = s.Render(text)
		}
		b.WriteString(styled)
		if i == len(cells)-1 {
			break
		}
		pad := max(1, widths[i]-lipgloss.Width(styled)+2)
		b.WriteString(strings.Repeat(" ", pad))
	}
	return b.String()
}

// windowedRange returns [start, end) so cursor stays roughly centred in a
// scrollable window of `capacity` rows over `total` items.
func windowedRange(cursor, capacity, total int) (int, int) {
	if total <= capacity {
		return 0, total
	}
	start := cursor - capacity/2
	if start < 0 {
		start = 0
	}
	if start+capacity > total {
		start = total - capacity
	}
	return start, start + capacity
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
