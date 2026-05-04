package tui

import (
	tea "charm.land/bubbletea/v2"
)

// Layout constants used by every render group. Centralised so width math
// stays consistent across header / list / detail / confirm.
const (
	headerHeight = 5 // info + keymap row, including borders
	statusHeight = 1
	// Panel chrome: 1 border + 1 padding on each side horizontally; 1 border
	// each side vertically.
	panelChromeX = 4
	panelChromeY = 2
)

func (m Model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	return v
}

// render is the string-returning sibling of View. Tests compare against it
// directly to skip unwrapping tea.View on every assertion. The actual layout
// lives in view_header.go (header + status), view_list.go, view_detail.go,
// and view_confirm.go — this file only routes.
func (m Model) render() string {
	if m.width == 0 || m.height == 0 {
		return "loading…"
	}
	switch m.view {
	case viewDetail:
		return m.viewDetail()
	case viewConfirmDelete:
		return m.viewConfirm()
	case viewReadOnly:
		return m.viewReadOnlyPanel()
	default:
		return m.viewList()
	}
}
