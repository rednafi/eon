package tui

import "charm.land/lipgloss/v2"

func (m Model) viewConfirm() string {
	panelWidth, panelHeight, contentWidth := m.bodyDims()
	body := lipgloss.JoinVertical(lipgloss.Left,
		m.theme.Header.Render("Delete this cron?"),
		"",
		m.kv("ID:", m.pendingDelete.ID),
		m.kv("Schedule:", m.pendingDelete.Schedule),
		m.kv("Command:", truncateMiddle(m.pendingDelete.Command, contentWidth-12)),
		"",
		m.theme.HelpKey.Render("y")+" "+m.theme.Help.Render("confirm")+"   "+
			m.theme.HelpKey.Render("n/esc")+" "+m.theme.Help.Render("cancel"),
	)
	panel := m.theme.MainPanel.Width(panelWidth).Height(panelHeight).Render(body)
	return lipgloss.JoinVertical(lipgloss.Left, m.renderHeader("Confirm"), panel, m.renderStatusBar())
}
