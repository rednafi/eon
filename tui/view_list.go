package tui

import (
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/rednafi/eon/cron"
)

// tableCols is the canonical column order. Both renderListPanel and
// computeColumnWidths read this; extending one without the other will panic.
var tableCols = []string{"ID", "SCOPE", "KIND", "NAME", "SCHEDULE", "STATUS"}

func (m Model) viewList() string {
	return lipgloss.JoinVertical(lipgloss.Left,
		m.renderHeader("Crons"),
		m.renderListPanel(),
		m.renderStatusBar(),
	)
}

func (m Model) renderListPanel() string {
	panelWidth, panelHeight, contentWidth := m.bodyDims()
	widths := m.colWidths
	if widths == nil {
		widths = computeColumnWidths(tableCols, m.jobs, contentWidth)
	}
	headerStyle := m.theme.HeaderCell
	headerLine := renderRow(tableCols, widths, &headerStyle, nil)
	rule := m.theme.Subtle.Render(strings.Repeat("─", contentWidth))

	listRows := panelHeight - panelChromeY - 2
	filterRow := ""
	if m.filterOn || m.filter.Value() != "" {
		filterRow = m.renderFilterChip()
		listRows--
	}
	if listRows < 1 {
		listRows = 1
	}

	start, end := windowedRange(m.cursor, listRows, len(m.visibleIdx))
	rows := make([]string, 0, listRows)
	for i := start; i < end; i++ {
		j := m.jobs[m.visibleIdx[i]]
		cells := []string{j.ID, string(j.Scope), string(j.Kind), j.Name, j.Schedule, j.Status}
		statusStyle := m.theme.statusStyle(j.Status)
		scopeStyle := m.theme.scopeStyle(j.Scope)
		overrides := []*lipgloss.Style{nil, &scopeStyle, nil, nil, nil, &statusStyle}
		line := renderRow(cells, widths, nil, overrides)
		if i == m.cursor {
			line = m.theme.Selected.Width(contentWidth).Render(line)
		}
		rows = append(rows, line)
	}
	if len(m.visibleIdx) == 0 {
		rows = append([]string{m.theme.Subtle.Render("(no scheduled jobs)")}, rows...)
	}
	for len(rows) < listRows {
		rows = append(rows, "")
	}

	parts := []string{headerLine, rule}
	if filterRow != "" {
		parts = append(parts, filterRow)
	}
	parts = append(parts, rows...)
	return m.theme.MainPanel.Width(panelWidth).Height(panelHeight).Render(strings.Join(parts, "\n"))
}

func (m Model) renderFilterChip() string {
	if m.filterOn {
		return m.theme.Filter.Render("filter ") + m.filter.View()
	}
	return m.theme.Filter.Render("filter ") + m.filter.Value() + "  " + m.theme.Subtle.Render("(esc clears)")
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

// computeColumnWidths sizes each column to its widest cell, then squeezes
// ID → NAME → KIND if the row exceeds the available width. Layout order is
// [ID, SCOPE, KIND, NAME, SCHEDULE, STATUS] — must match the cells slice in
// renderListPanel.
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
	// Skip SCOPE (fixed-vocab "user"/"system"), SCHEDULE, STATUS — they're
	// short and most informative. Squeeze ID, then NAME, then KIND.
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

func (m *Model) recomputeColWidths() {
	_, _, contentWidth := m.bodyDims()
	m.colWidths = computeColumnWidths(tableCols, m.jobs, contentWidth)
}
