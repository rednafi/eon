// Package tui implements eon's k9s-style interactive shell. Layout: header
// strip (info + keymap), main panel (resource table or detail tabs), status
// bar. lipgloss/v2 strips ANSI on non-TTY/NO_COLOR environments without our
// involvement.
package tui

import (
	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/compat"

	"github.com/rednafi/eon/cron"
)

func adaptive(light, dark string) compat.AdaptiveColor {
	return compat.AdaptiveColor{Light: lipgloss.Color(light), Dark: lipgloss.Color(dark)}
}

type theme struct {
	Title, Subtle, Header               lipgloss.Style
	Panel, MainPanel, FocusPanel        lipgloss.Style
	HeaderCell, Selected                lipgloss.Style
	Status, StatusBad, StatusWarn       lipgloss.Style
	HelpKey, Help                       lipgloss.Style
	TabActive, TabInactive              lipgloss.Style
	Filter, Error                       lipgloss.Style
}

func newTheme() theme {
	primary := adaptive("#5A4FCF", "#B6B3FF")
	accent := adaptive("#0891B2", "#67E8F9")
	muted := adaptive("#6C737B", "#9BA0A6")
	hl := adaptive("#E8E8FF", "#3B3578")
	good := adaptive("#1E823C", "#7BD88F")
	bad := adaptive("#B2002A", "#FF6B81")
	warn := adaptive("#B45309", "#FBBF24")

	border := lipgloss.RoundedBorder()
	panel := lipgloss.NewStyle().Border(border).BorderForeground(muted).Padding(0, 1)

	return theme{
		Title:       lipgloss.NewStyle().Bold(true).Foreground(primary),
		Subtle:      lipgloss.NewStyle().Foreground(muted),
		Header:      lipgloss.NewStyle().Bold(true).Foreground(primary),
		Panel:       panel,
		MainPanel:   panel.BorderForeground(primary),
		FocusPanel:  panel.BorderForeground(accent),
		HeaderCell:  lipgloss.NewStyle().Bold(true).Foreground(accent),
		Selected:    lipgloss.NewStyle().Foreground(primary).Background(hl).Bold(true),
		Status:      lipgloss.NewStyle().Foreground(good),
		StatusBad:   lipgloss.NewStyle().Foreground(bad),
		StatusWarn:  lipgloss.NewStyle().Foreground(warn),
		HelpKey:     lipgloss.NewStyle().Foreground(accent).Bold(true),
		Help:        lipgloss.NewStyle().Foreground(muted),
		TabActive:   lipgloss.NewStyle().Bold(true).Foreground(primary).Background(hl).Padding(0, 2),
		TabInactive: lipgloss.NewStyle().Foreground(muted).Padding(0, 2),
		Filter:      lipgloss.NewStyle().Foreground(primary).Bold(true),
		Error:       lipgloss.NewStyle().Foreground(bad).Bold(true),
	}
}

func (t theme) statusStyle(s string) lipgloss.Style {
	switch s {
	case "running", "loaded", "scheduled":
		return t.Status
	case "disabled", "on-demand", "":
		return t.StatusWarn
	default:
		return t.StatusBad
	}
}

// scopeStyle dims SYSTEM rows so user-scope crons read at a glance, and
// keeps user rows in the accent palette.
func (t theme) scopeStyle(s cron.Scope) lipgloss.Style {
	if s == cron.ScopeSystem {
		return t.Subtle
	}
	return t.HelpKey
}
