package tui

import "charm.land/lipgloss/v2"

func (m Model) viewFormPanel() string {
	header := "New cron"
	if m.formMode == formEdit {
		header = "Edit cron"
	}
	var errLine string
	if m.formError != "" {
		errLine = m.theme.Error.Render(m.formError)
	}
	body := lipgloss.JoinVertical(lipgloss.Left,
		m.theme.Header.Render(header),
		"",
		m.formSchedule.View(),
		m.formCommand.View(),
		"",
		errLine,
		m.theme.HelpKey.Render("tab")+" "+m.theme.Help.Render("next field")+"   "+
			m.theme.HelpKey.Render("enter")+" "+m.theme.Help.Render("submit")+"   "+
			m.theme.HelpKey.Render("esc")+" "+m.theme.Help.Render("cancel"),
	)
	return m.panel(header, body)
}
