package tui

import "charm.land/lipgloss/v2"

func (m Model) viewReadOnlyPanel() string {
	panelWidth, panelHeight, contentWidth := m.bodyDims()
	body := lipgloss.JoinVertical(lipgloss.Left,
		m.theme.Error.Render("System cron — read-only"),
		"",
		m.theme.Help.Render("Jobs in /Library/Launch*, /etc/crontab, /etc/cron.d, and"),
		m.theme.Help.Render("/etc/systemd/system are owned by the OS or its package"),
		m.theme.Help.Render("manager. eon refuses to delete them."),
		"",
		m.kv("ID:", m.pendingDelete.ID),
		m.kv("Path:", truncateMiddle(m.pendingDelete.Path, contentWidth-12)),
		"",
		m.theme.HelpKey.Render("any key")+" "+m.theme.Help.Render("dismiss"),
	)
	panel := m.theme.MainPanel.Width(panelWidth).Height(panelHeight).Render(body)
	return lipgloss.JoinVertical(lipgloss.Left, m.renderHeader("Read-only"), panel, m.renderStatusBar())
}
