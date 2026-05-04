package tui

import (
	"strings"

	"charm.land/bubbles/v2/viewport"

	"github.com/rednafi/eon/cron"
)

// startDelete routes to the read-only modal for system-scope jobs. Without
// this gate, the source's Delete would error and the flash would silently
// swallow it.
func (m *Model) startDelete() {
	j, ok := m.currentJob()
	if !ok {
		return
	}
	m.selectedJob = j
	if j.Scope == cron.ScopeSystem {
		m.view = viewReadOnly
		return
	}
	m.view = viewConfirmDelete
}

// activeVP returns the viewport backing the current detail tab. Returning a
// pointer lets the Update path do `*vp, cmd = vp.Update(msg)` once instead
// of repeating the three-way switch in every place that forwards a message.
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
// showSystem toggle. Reuses the existing slice's capacity so typing into the
// filter doesn't allocate per keystroke.
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

func (m *Model) recomputeColWidths() {
	_, _, contentWidth := m.bodyDims()
	m.colWidths = computeColumnWidths(tableCols, jobsToCells(m.jobs), contentWidth)
}

// jobsToCells projects []cron.Job into the layout-package shape so the
// layout file stays free of cron domain types.
func jobsToCells(jobs []cron.Job) [][6]string {
	out := make([][6]string, len(jobs))
	for i, j := range jobs {
		out[i] = [6]string{j.ID, string(j.Scope), string(j.Kind), j.Name, j.Schedule, j.Status}
	}
	return out
}

func jobMatches(j *cron.Job, lowerQuery string) bool {
	return strings.Contains(strings.ToLower(j.ID), lowerQuery) ||
		strings.Contains(strings.ToLower(j.Name), lowerQuery) ||
		strings.Contains(strings.ToLower(j.Command), lowerQuery) ||
		strings.Contains(strings.ToLower(j.Schedule), lowerQuery)
}
