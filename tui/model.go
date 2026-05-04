package tui

import (
	"context"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"

	"github.com/rednafi/eon/cron"
)

type view int

const (
	viewList view = iota
	viewDetail
	viewConfirmDelete
	viewReadOnly // shown when the user tries to delete a system-scope job
)

const flashDuration = 3 * time.Second

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
	mgr   *cron.Manager
	keys  keyMap
	theme theme

	view          view
	width, height int
	jobs          []cron.Job
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

	selectedJob cron.Job
	flash       string
	flashUntil  time.Time
}

func New(mgr *cron.Manager) Model {
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
	jobs []cron.Job
	err  string
}

// flashMsg carries a one-line status update. ok=true means the underlying
// op mutated state and the UI should reload; ok=false (errors, aborts) only
// updates the flash and skips the round-trip.
type flashMsg struct {
	text string
	ok   bool
}

// listTimeout bounds Manager.List — fans out across multiple Sources, each
// of which may shell out to launchctl/systemctl/crontab. A stuck binary
// shouldn't freeze the UI, but we need headroom for cold caches.
const listTimeout = 5 * time.Second

// deleteTimeout bounds a single Source.Delete — typically one unlink or one
// launchctl unload, both sub-100ms in the healthy case. 2s is generous
// enough for a slow disk and tight enough to surface a stuck call.
const deleteTimeout = 2 * time.Second

func reload(mgr *cron.Manager) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), listTimeout)
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

func deleteCmd(mgr *cron.Manager, id string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), deleteTimeout)
		defer cancel()
		if err := mgr.Delete(ctx, id); err != nil {
			return flashMsg{text: "delete failed: " + err.Error()}
		}
		return flashMsg{text: "deleted " + id, ok: true}
	}
}
