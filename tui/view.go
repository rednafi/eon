package tui

import tea "charm.land/bubbletea/v2"

// View is the bubbletea entry point. It picks the right sub-view (list,
// detail, confirm, read-only) and asks for a string, which tea.NewView
// turns into a renderable. Layout primitives are in layout.go; the actual
// renderers live in view_*.go.
func (m Model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	return v
}

// render is the string-returning sibling of View — tests assert against it
// directly to skip unwrapping the tea.View struct.
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
