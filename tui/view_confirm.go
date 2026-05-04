package tui

import "charm.land/lipgloss/v2"

func (m Model) viewConfirm() string {
	_, _, contentWidth := m.bodyDims()
	body := lipgloss.JoinVertical(lipgloss.Left,
		m.theme.Header.Render("Delete this cron?"),
		"",
		m.kv("ID:", m.selectedJob.ID),
		m.kv("Schedule:", m.selectedJob.Schedule),
		m.kv("Command:", truncateMiddle(m.selectedJob.Command, contentWidth-12)),
		"",
		m.theme.HelpKey.Render("y")+" "+m.theme.Help.Render("confirm")+"   "+
			m.theme.HelpKey.Render("n/esc")+" "+m.theme.Help.Render("cancel"),
	)
	return m.panel("Confirm", body)
}
