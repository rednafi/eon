package tui

import (
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/rednafi/eon/cron"
)

const (
	headerHeight = 5 // info + keymap row, including borders
	statusHeight = 1
	// Panel chrome: 1 border + 1 padding on each side horizontally; 1 border
	// each side vertically.
	panelChromeX = 4
	panelChromeY = 2
)

// tableCols is the canonical column order. renderListPanel and
// computeColumnWidths both rely on this order — a drift would panic on the
// first cell.
var tableCols = []string{"ID", "SCOPE", "KIND", "NAME", "SCHEDULE", "STATUS"}

// jobCell pulls a single column out of a Job. Centralising the projection
// keeps tableCols and every renderer in lockstep — change it here and both
// width-measuring and row-rendering pick it up.
func jobCell(j cron.Job, col int) string {
	switch col {
	case 0:
		return j.ID
	case 1:
		return string(j.Scope)
	case 2:
		return string(j.Kind)
	case 3:
		return j.Name
	case 4:
		return j.Schedule
	case 5:
		return j.Status
	}
	return ""
}

// computeColumnWidths sizes each column to its widest cell, then squeezes
// ID → NAME → KIND if the row exceeds the available width. SCOPE/SCHEDULE/
// STATUS keep their natural width — they're short and most informative.
func computeColumnWidths(jobs []cron.Job, available int) []int {
	widths := make([]int, len(tableCols))
	for i, h := range tableCols {
		widths[i] = len(h)
	}
	for _, j := range jobs {
		for i := range tableCols {
			if w := len(jobCell(j, i)); w > widths[i] {
				widths[i] = w
			}
		}
	}
	gutter := 2 * (len(tableCols) - 1)
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
	// Pre-size the builder: sum(widths) + 2-cell gutters + ~16 bytes per
	// cell of ANSI escape slack (a styled cell carries roughly that for
	// fg+bg+reset). Without Grow the builder doubles 2-3 times per row.
	total := 2 * (len(cells) - 1)
	for _, w := range widths {
		total += w
	}
	var b strings.Builder
	b.Grow(total + 16*len(cells))
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
