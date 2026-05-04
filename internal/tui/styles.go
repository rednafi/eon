// Package tui implements eon's interactive TUI built on bubbletea.
//
// The TUI is a two-pane k9s-like interface: a scrollable list of all known
// crons and a detail view that drills into a single job's metadata, raw
// definition, and tail of stdout/stderr. Styling falls back gracefully when
// the terminal is monochrome or NO_COLOR is set.
package tui

import (
	"github.com/charmbracelet/lipgloss"
)

// theme is the shared style palette. Colors are AdaptiveColor so the same
// palette renders sensibly on light and dark backgrounds. lipgloss already
// honours NO_COLOR / non-TTY environments by stripping ANSI when appropriate.
type theme struct {
	Title       lipgloss.Style
	Subtle      lipgloss.Style
	Header      lipgloss.Style
	Selected    lipgloss.Style
	Status      lipgloss.Style
	StatusError lipgloss.Style
	Filter      lipgloss.Style
	Help        lipgloss.Style
	TabActive   lipgloss.Style
	TabInactive lipgloss.Style
	Border      lipgloss.Style
	Error       lipgloss.Style
}

func newTheme() theme {
	primary := lipgloss.AdaptiveColor{Light: "#5A4FCF", Dark: "#A1A2FF"}
	muted := lipgloss.AdaptiveColor{Light: "#6C737B", Dark: "#9BA0A6"}
	hl := lipgloss.AdaptiveColor{Light: "#E8E8FF", Dark: "#2D2A55"}
	good := lipgloss.AdaptiveColor{Light: "#1E823C", Dark: "#7BD88F"}
	bad := lipgloss.AdaptiveColor{Light: "#B2002A", Dark: "#FF6B81"}

	return theme{
		Title:       lipgloss.NewStyle().Bold(true).Foreground(primary),
		Subtle:      lipgloss.NewStyle().Foreground(muted),
		Header:      lipgloss.NewStyle().Bold(true).Foreground(primary),
		Selected:    lipgloss.NewStyle().Foreground(primary).Background(hl).Bold(true),
		Status:      lipgloss.NewStyle().Foreground(good),
		StatusError: lipgloss.NewStyle().Foreground(bad),
		Filter:      lipgloss.NewStyle().Foreground(primary).Bold(true),
		Help:        lipgloss.NewStyle().Foreground(muted),
		TabActive:   lipgloss.NewStyle().Bold(true).Foreground(primary).Underline(true),
		TabInactive: lipgloss.NewStyle().Foreground(muted),
		Border:      lipgloss.NewStyle().Foreground(muted),
		Error:       lipgloss.NewStyle().Foreground(bad).Bold(true),
	}
}
