package tui

import (
	"context"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"

	"github.com/rednafi/eon/internal/origin"
)

type view int

const (
	viewList view = iota
	viewDetail
	viewConfirmDelete
)

type detailTab int

const (
	tabOverview detailTab = iota
	tabRaw
	tabLogs
	tabCount
)

func (t detailTab) String() string {
	switch t {
	case tabOverview:
		return "Overview"
	case tabRaw:
		return "Raw"
	case tabLogs:
		return "Logs"
	}
	return ""
}

type Model struct {
	mgr   *origin.Manager
	keys  keyMap
	theme theme

	view          view
	width, height int
	jobs          []origin.Job
	loadErr       string

	cursor   int
	filter   textinput.Model
	filterOn bool
	// showSystem controls whether read-only system jobs (Job.System=true)
	// appear in the list. Default false: the user-scope view is the
	// signal, system jobs are noise unless explicitly requested.
	showSystem bool

	// Cached so we don't rebuild on every render. Recomputed when jobs or
	// filter text change.
	visibleIdx []int
	colWidths  []int

	detailTab detailTab
	detailVP  viewport.Model
	rawVP     viewport.Model
	logsVP    viewport.Model
	// Most recent job ID rendered into the detail viewports; used to skip
	// rebuilding when nothing changed (e.g. on resize while sitting on list).
	lastDetailID string

	pendingDelete origin.Job
	flash         string
	flashUntil    time.Time
}

func New(mgr *origin.Manager) Model {
	ti := textinput.New()
	ti.Placeholder = "filter"
	ti.Prompt = "/ "
	ti.CharLimit = 128
	return Model{
		mgr:    mgr,
		keys:   newKeyMap(),
		theme:  newTheme(),
		view:   viewList,
		filter: ti,
	}
}

func (m Model) Init() tea.Cmd { return reload(m.mgr) }

type jobsLoadedMsg struct {
	jobs []origin.Job
	err  string
}

type flashMsg struct{ text string }

// reload fetches the current snapshot. Bounded by 5s so a stuck launchctl/
// systemctl can't freeze the UI.
func reload(mgr *origin.Manager) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		jobs, errs := mgr.List(ctx)
		var msg string
		if len(errs) > 0 {
			parts := make([]string, 0, len(errs))
			for _, e := range errs {
				parts = append(parts, e.Error())
			}
			msg = strings.Join(parts, "; ")
		}
		return jobsLoadedMsg{jobs: jobs, err: msg}
	}
}

func deleteCmd(mgr *origin.Manager, id string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := mgr.Delete(ctx, id); err != nil {
			return flashMsg{text: "delete failed: " + err.Error()}
		}
		return flashMsg{text: "deleted " + id}
	}
}
