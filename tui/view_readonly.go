package tui

import "charm.land/lipgloss/v2"

func (m Model) viewReadOnlyPanel() string {
	_, _, contentWidth := m.bodyDims()
	body := lipgloss.JoinVertical(lipgloss.Left,
		m.theme.Error.Render("System cron — read-only"),
		"",
		m.theme.Help.Render("This job is owned by the OS or your package manager."),
		m.theme.Help.Render("eon refuses to delete it."),
		"",
		m.kv("ID:", m.selectedJob.ID),
		m.kv("Path:", truncateMiddle(m.selectedJob.Path, contentWidth-12)),
		"",
		m.theme.HelpKey.Render("any key")+" "+m.theme.Help.Render("dismiss"),
	)
	return m.modal("Read-only", body)
}
