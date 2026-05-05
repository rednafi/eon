package tui

import (
	"strings"
	"time"

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
		m.flashUntil = time.Now().Add(flashDuration)
		// Only reload on successful mutations — a failed delete didn't
		// change anything on disk, so re-listing every Source is wasted.
		if msg.ok {
			return m, reload(m.mgr)
		}
		return m, nil

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
			id := m.selectedJob.ID
			m.view = viewList
			return m, deleteCmd(m.mgr, id)
		case key.Matches(msg, m.keys.Cancel):
			m.view = viewList
		}
		return m, nil

	case viewReadOnly:
		// Any key dismisses. We send no Cmd because there's nothing to refresh.
		m.view = viewList
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
			m.startDelete()
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
		case key.Matches(msg, m.keys.Add):
			return m.openAddForm()
		case key.Matches(msg, m.keys.Edit):
			return m.openEditForm()
		case key.Matches(msg, m.keys.Delete):
			m.startDelete()
		}

	case viewForm:
		return m.handleFormKey(msg)
	}
	return m, nil
}

func (m Model) handleFormKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.view = viewList
		m.formError = ""
		return m, nil
	case "tab":
		m.formFocus = (m.formFocus + 1) % 2
		m.refocusForm()
		return m, nil
	case "shift+tab":
		m.formFocus = (m.formFocus + 1) % 2 // only two fields, same as tab
		m.refocusForm()
		return m, nil
	case "enter":
		spec := cron.JobSpec{
			Schedule: m.formSchedule.Value(),
			Command:  m.formCommand.Value(),
		}
		if strings.TrimSpace(spec.Schedule) == "" || strings.TrimSpace(spec.Command) == "" {
			m.formError = "schedule and command are required"
			return m, nil
		}
		m.view = viewList
		m.formError = ""
		if m.formMode == formAdd {
			return m, addCmd(m.mgr, spec)
		}
		return m, editCmd(m.mgr, m.selectedJob.ID, spec)
	}
	var cmd tea.Cmd
	if m.formFocus == 0 {
		m.formSchedule, cmd = m.formSchedule.Update(msg)
	} else {
		m.formCommand, cmd = m.formCommand.Update(msg)
	}
	return m, cmd
}
