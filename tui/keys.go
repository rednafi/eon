package tui

import "charm.land/bubbles/v2/key"

type keyMap struct {
	Up, Down, Top, Bottom        key.Binding
	Filter, Refresh, Enter, Back key.Binding
	Delete, Tab, Quit, Help      key.Binding
	ToggleSystem                 key.Binding
	Confirm, Cancel              key.Binding
}

func newKeyMap() keyMap {
	return keyMap{
		Up:           key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑/k", "up")),
		Down:         key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓/j", "down")),
		Top:          key.NewBinding(key.WithKeys("g", "home"), key.WithHelp("g", "top")),
		Bottom:       key.NewBinding(key.WithKeys("G", "end"), key.WithHelp("G", "bottom")),
		Filter:       key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "filter")),
		Refresh:      key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "refresh")),
		Enter:        key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "open")),
		Back:         key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back")),
		Delete:       key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "delete")),
		Tab:          key.NewBinding(key.WithKeys("tab", "shift+tab"), key.WithHelp("tab", "switch tab")),
		Quit:         key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit")),
		Help:         key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
		ToggleSystem: key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "toggle system")),
		Confirm:      key.NewBinding(key.WithKeys("y", "Y")),
		Cancel:       key.NewBinding(key.WithKeys("n", "N", "esc")),
	}
}
