package tui

import "github.com/charmbracelet/bubbles/key"

// keyMap centralises every keybinding so the help line stays in sync with
// what the model actually responds to.
type keyMap struct {
	Up      key.Binding
	Down    key.Binding
	Top     key.Binding
	Bottom  key.Binding
	Filter  key.Binding
	Refresh key.Binding
	Enter   key.Binding
	Back    key.Binding
	Delete  key.Binding
	Tab     key.Binding
	Quit    key.Binding
	Help    key.Binding
	Confirm key.Binding
	Cancel  key.Binding
}

func newKeyMap() keyMap {
	return keyMap{
		Up:      key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑/k", "up")),
		Down:    key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓/j", "down")),
		Top:     key.NewBinding(key.WithKeys("g", "home"), key.WithHelp("g", "top")),
		Bottom:  key.NewBinding(key.WithKeys("G", "end"), key.WithHelp("G", "bottom")),
		Filter:  key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "filter")),
		Refresh: key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "refresh")),
		Enter:   key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "open")),
		Back:    key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back")),
		Delete:  key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "delete")),
		Tab:     key.NewBinding(key.WithKeys("tab", "shift+tab"), key.WithHelp("tab", "switch tab")),
		Quit:    key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit")),
		Help:    key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
		Confirm: key.NewBinding(key.WithKeys("y", "Y")),
		Cancel:  key.NewBinding(key.WithKeys("n", "N", "esc")),
	}
}
