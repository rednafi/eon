package tui

import (
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"

	"github.com/rednafi/eon/cron"
)

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		body := max(1, m.height-headerHeight-statusHeight-2)
		w := max(20, m.width-panelChromeX)
		m.detailVP = viewport.New(viewport.WithWidth(w), viewport.WithHeight(body-2))
		m.rawVP = viewport.New(viewport.WithWidth(w), viewport.WithHeight(body-2))
		m.logsVP = viewport.New(viewport.WithWidth(w), viewport.WithHeight(body-2))
		m.recomputeColWidths()
		if m.view == viewDetail {
			m.refreshDetailContent()
		}
		return m, nil

	case jobsLoadedMsg:
		m.jobs = msg.jobs
		m.loadErr = msg.err
		m.recomputeFilter()
		m.recomputeColWidths()
		if m.cursor >= len(m.jobs) {
			m.cursor = max(0, len(m.jobs)-1)
		}
		if m.view == viewDetail && len(m.visibleIdx) == 0 {
			m.view = viewList
		}
		if m.view == viewDetail {
			m.refreshDetailContent()
		}
		return m, nil

	case flashMsg:
		m.flash = msg.text
		return m, reload(m.mgr)

	case tea.KeyPressMsg:
		return m.handleKey(msg)
	}

	if m.view == viewDetail {
		var cmd tea.Cmd
		vp := m.activeVP()
		*vp, cmd = vp.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.filterOn {
		switch msg.String() {
		case "esc":
			m.filterOn = false
			m.filter.Blur()
			m.filter.SetValue("")
			m.recomputeFilter()
			return m, nil
		case "enter":
			m.filterOn = false
			m.filter.Blur()
			return m, nil
		}
		var cmd tea.Cmd
		m.filter, cmd = m.filter.Update(msg)
		m.recomputeFilter()
		if m.cursor >= len(m.visibleIdx) {
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
		var cmd tea.Cmd
		vp := m.activeVP()
		*vp, cmd = vp.Update(msg)
		return m, cmd

	case viewList:
		switch {
		case key.Matches(msg, m.keys.Quit):
			return m, tea.Quit
		case key.Matches(msg, m.keys.Filter):
			m.filterOn = true
			return m, m.filter.Focus()
		case key.Matches(msg, m.keys.Up):
			if m.cursor > 0 {
				m.cursor--
			}
		case key.Matches(msg, m.keys.Down):
			if m.cursor < len(m.visibleIdx)-1 {
				m.cursor++
			}
		case key.Matches(msg, m.keys.Top):
			m.cursor = 0
		case key.Matches(msg, m.keys.Bottom):
			m.cursor = max(0, len(m.visibleIdx)-1)
		case key.Matches(msg, m.keys.Refresh):
			return m, reload(m.mgr)
		case key.Matches(msg, m.keys.ToggleSystem):
			m.showSystem = !m.showSystem
			m.recomputeFilter()
			if m.cursor >= len(m.visibleIdx) {
				m.cursor = max(0, len(m.visibleIdx)-1)
			}
		case key.Matches(msg, m.keys.Enter):
			if _, ok := m.currentJob(); ok {
				m.view = viewDetail
				m.detailTab = tabOverview
				m.refreshDetailContent()
			}
		case key.Matches(msg, m.keys.Delete):
			if j, ok := m.currentJob(); ok {
				m.pendingDelete = j
				m.view = viewConfirmDelete
			}
		}
	}
	return m, nil
}

// activeVP returns the viewport backing the current detail tab. Returning a
// pointer lets callers do `*vp, cmd = vp.Update(msg)` in one place instead of
// repeating the three-way switch.
func (m *Model) activeVP() *viewport.Model {
	switch m.detailTab {
	case tabRaw:
		return &m.rawVP
	case tabLogs:
		return &m.logsVP
	default:
		return &m.detailVP
	}
}

func (m Model) currentJob() (cron.Job, bool) {
	if len(m.visibleIdx) == 0 || m.cursor >= len(m.visibleIdx) {
		return cron.Job{}, false
	}
	return m.jobs[m.visibleIdx[m.cursor]], true
}

// recomputeFilter rebuilds visibleIdx from the current filter text and the
// showSystem toggle. Cheap enough to run on every keystroke during typing.
func (m *Model) recomputeFilter() {
	q := strings.ToLower(strings.TrimSpace(m.filter.Value()))
	m.visibleIdx = m.visibleIdx[:0]
	if cap(m.visibleIdx) < len(m.jobs) {
		m.visibleIdx = make([]int, 0, len(m.jobs))
	}
	for i, j := range m.jobs {
		if j.Scope == cron.ScopeSystem && !m.showSystem {
			continue
		}
		if q == "" || jobMatches(&j, q) {
			m.visibleIdx = append(m.visibleIdx, i)
		}
	}
}

func jobMatches(j *cron.Job, lowerQuery string) bool {
	return strings.Contains(strings.ToLower(j.ID), lowerQuery) ||
		strings.Contains(strings.ToLower(j.Name), lowerQuery) ||
		strings.Contains(strings.ToLower(j.Command), lowerQuery) ||
		strings.Contains(strings.ToLower(j.Schedule), lowerQuery)
}
