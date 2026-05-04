package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/rednafi/eon/internal/origin"
)

// Update implements tea.Model.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		// Subtract title (1) + header (1) + separator (1) + status line (1).
		body := max(1, msg.Height-4)
		m.detailVP = viewport.New(max(20, msg.Width-2), body-2)
		m.logsVP = viewport.New(max(20, msg.Width-2), body-2)
		m.rawVP = viewport.New(max(20, msg.Width-2), body-2)
		m.refreshDetailContent()
		return m, nil

	case jobsLoadedMsg:
		m.jobs = msg.jobs
		m.loadErr = msg.err
		if m.cursor >= len(m.jobs) {
			m.cursor = max(0, len(m.jobs)-1)
		}
		// If we're on the detail view and the underlying job is gone, drop
		// back to the list. This covers the case where a delete succeeded
		// while the user was reading detail.
		if m.view == viewDetail && len(m.filteredIndexes()) == 0 {
			m.view = viewList
		}
		m.refreshDetailContent()
		return m, nil

	case flashMsg:
		m.flash = msg.text
		// Reload to reflect any deletion.
		return m, reload(m.mgr)

	case tea.KeyMsg:
		return m.handleKey(msg)
	}

	// Forward to active viewport for mouse-wheel and other passthrough.
	switch m.view {
	case viewDetail:
		var cmd tea.Cmd
		switch m.detailTab {
		case tabOverview:
			m.detailVP, cmd = m.detailVP.Update(msg)
		case tabRaw:
			m.rawVP, cmd = m.rawVP.Update(msg)
		case tabLogs:
			m.logsVP, cmd = m.logsVP.Update(msg)
		}
		return m, cmd
	}
	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Filter input owns keystrokes when active. We only intercept Esc and
	// Enter so the user can dismiss/commit; everything else flows to the
	// textinput so typing /, j, k, etc. doesn't move the cursor underneath.
	if m.filterOn {
		switch msg.String() {
		case "esc":
			m.filterOn = false
			m.filter.Blur()
			m.filter.SetValue("")
			return m, nil
		case "enter":
			m.filterOn = false
			m.filter.Blur()
			return m, nil
		}
		var cmd tea.Cmd
		m.filter, cmd = m.filter.Update(msg)
		// Reset cursor when the visible list shrinks under us.
		if m.cursor >= len(m.filteredIndexes()) {
			m.cursor = 0
		}
		return m, cmd
	}

	switch m.view {
	case viewConfirmDelete:
		switch {
		case key.Matches(msg, m.keys.Confirm):
			id := m.pendingDelete.ID
			m.view = viewList
			return m, deleteCmd(m.mgr, id)
		case key.Matches(msg, m.keys.Cancel):
			m.view = viewList
			return m, nil
		}
		return m, nil

	case viewDetail:
		switch {
		case key.Matches(msg, m.keys.Back):
			m.view = viewList
			return m, nil
		case key.Matches(msg, m.keys.Tab):
			if msg.String() == "shift+tab" {
				m.detailTab = (m.detailTab + tabCount - 1) % tabCount
			} else {
				m.detailTab = (m.detailTab + 1) % tabCount
			}
			m.refreshDetailContent()
			return m, nil
		case key.Matches(msg, m.keys.Refresh):
			return m, reload(m.mgr)
		case key.Matches(msg, m.keys.Delete):
			if j, ok := m.currentJob(); ok {
				m.pendingDelete = j
				m.view = viewConfirmDelete
			}
			return m, nil
		case key.Matches(msg, m.keys.Quit):
			return m, tea.Quit
		}
		// Forward to the active viewport for scrolling.
		var cmd tea.Cmd
		switch m.detailTab {
		case tabOverview:
			m.detailVP, cmd = m.detailVP.Update(msg)
		case tabRaw:
			m.rawVP, cmd = m.rawVP.Update(msg)
		case tabLogs:
			m.logsVP, cmd = m.logsVP.Update(msg)
		}
		return m, cmd

	case viewList:
		visible := m.filteredIndexes()
		switch {
		case key.Matches(msg, m.keys.Quit):
			return m, tea.Quit
		case key.Matches(msg, m.keys.Filter):
			m.filterOn = true
			m.filter.Focus()
			return m, nil
		case key.Matches(msg, m.keys.Up):
			if m.cursor > 0 {
				m.cursor--
			}
			return m, nil
		case key.Matches(msg, m.keys.Down):
			if m.cursor < len(visible)-1 {
				m.cursor++
			}
			return m, nil
		case key.Matches(msg, m.keys.Top):
			m.cursor = 0
			return m, nil
		case key.Matches(msg, m.keys.Bottom):
			m.cursor = max(0, len(visible)-1)
			return m, nil
		case key.Matches(msg, m.keys.Refresh):
			return m, reload(m.mgr)
		case key.Matches(msg, m.keys.Enter):
			if _, ok := m.currentJob(); ok {
				m.view = viewDetail
				m.detailTab = tabOverview
				m.refreshDetailContent()
			}
			return m, nil
		case key.Matches(msg, m.keys.Delete):
			if j, ok := m.currentJob(); ok {
				m.pendingDelete = j
				m.view = viewConfirmDelete
			}
			return m, nil
		}
	}
	return m, nil
}

// currentJob returns the job at the cursor (after filtering), or false when
// the list is empty.
func (m Model) currentJob() (origin.Job, bool) {
	visible := m.filteredIndexes()
	if len(visible) == 0 || m.cursor >= len(visible) {
		return origin.Job{}, false
	}
	return m.jobs[visible[m.cursor]], true
}

// filteredIndexes returns the indexes into m.jobs that match the current
// filter. The empty filter returns every index.
func (m Model) filteredIndexes() []int {
	if !m.filterOn && m.filter.Value() == "" {
		idx := make([]int, len(m.jobs))
		for i := range m.jobs {
			idx[i] = i
		}
		return idx
	}
	q := strings.ToLower(strings.TrimSpace(m.filter.Value()))
	if q == "" {
		idx := make([]int, len(m.jobs))
		for i := range m.jobs {
			idx[i] = i
		}
		return idx
	}
	var out []int
	for i, j := range m.jobs {
		if strings.Contains(strings.ToLower(j.ID), q) ||
			strings.Contains(strings.ToLower(j.Name), q) ||
			strings.Contains(strings.ToLower(j.Command), q) ||
			strings.Contains(strings.ToLower(j.Schedule), q) {
			out = append(out, i)
		}
	}
	return out
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
