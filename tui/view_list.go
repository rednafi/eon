package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
)

func (m Model) viewList() string {
	return m.frame("Crons", m.renderListPanel())
}

func (m Model) renderListPanel() string {
	panelWidth, panelHeight, contentWidth := m.bodyDims()
	widths := m.colWidths
	if widths == nil {
		widths = computeColumnWidths(tableCols, jobsToCells(m.jobs), contentWidth)
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
	// Per-row arrays sit on the stack (Go inlines fixed-size [6]T into the
	// caller's frame) so the inner loop allocates nothing beyond what
	// renderRow itself needs.
	for i := start; i < end; i++ {
		j := m.jobs[m.visibleIdx[i]]
		cells := [6]string{j.ID, string(j.Scope), string(j.Kind), j.Name, j.Schedule, j.Status}
		statusStyle := m.theme.statusStyle(j.Status)
		scopeStyle := m.theme.scopeStyle(j.Scope)
		overrides := [6]*lipgloss.Style{nil, &scopeStyle, nil, nil, nil, &statusStyle}
		line := renderRow(cells[:], widths, nil, overrides[:])
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
