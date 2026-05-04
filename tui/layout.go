package tui

import (
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/rednafi/eon/cron"
)

// Layout primitives shared by every view: panel chrome dimensions, column
// width math, row rendering, and integer helpers. None of this is render-
// stateful — the functions take their inputs explicitly so they're trivial
// to test outside of a Model.

const (
	headerHeight = 5 // info + keymap row, including borders
	statusHeight = 1
	// Panel chrome: 1 border + 1 padding on each side horizontally; 1 border
	// each side vertically.
	panelChromeX = 4
	panelChromeY = 2
)

// tableCols is the canonical column order. renderListPanel and
// computeColumnWidths both index into this — a drift between the two would
// panic on the first cell.
var tableCols = []string{"ID", "SCOPE", "KIND", "NAME", "SCHEDULE", "STATUS"}

// computeColumnWidths sizes each column to its widest cell, then squeezes
// ID → NAME → KIND if the row exceeds the available width. Layout order is
// [ID, SCOPE, KIND, NAME, SCHEDULE, STATUS]; SCOPE/SCHEDULE/STATUS are short
// and most informative so we don't shrink them.
func computeColumnWidths(headers []string, jobs []cron.Job, available int) []int {
	widths := make([]int, len(headers))
	for i, h := range headers {
		widths[i] = len(h)
	}
	for _, j := range jobs {
		row := [6]string{j.ID, string(j.Scope), string(j.Kind), j.Name, j.Schedule, j.Status}
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
	var b strings.Builder
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
		pad := widths[i] - lipgloss.Width(styled) + 2
		if pad < 1 {
			pad = 1
		}
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
