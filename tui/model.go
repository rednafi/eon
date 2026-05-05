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
	viewForm     // schedule/command entry for add or edit
)

type formMode int

const (
	formAdd formMode = iota
	formEdit
)

// Timing budget for asynchronous operations. flashDuration is how long a
// status-bar one-liner stays visible. listTimeout bounds Manager.List —
// fans out across multiple Sources that each may shell out to
// launchctl/systemctl/crontab; needs headroom for cold caches without
// freezing the UI on a stuck binary. deleteTimeout bounds one Source.Delete
// — a single unlink or launchctl unload is sub-100ms in the healthy case.
const (
	flashDuration = 3 * time.Second
	listTimeout   = 5 * time.Second
	deleteTimeout = 2 * time.Second
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

// Model holds every piece of TUI state: the loaded jobs, the cursor, the
// filter, the active view, and the bubbletea sub-models for the detail
// viewports. It is passed by value to bubbletea's Update/View loop and
// mutated in place by the helpers in state.go.
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

	// form state for the add/edit modal.
	formMode     formMode
	formSchedule textinput.Model
	formCommand  textinput.Model
	formFocus    int // 0=schedule, 1=command
	formError    string
}

// New constructs a Model wired to the given Manager. The Model is returned
// by value because bubbletea's Update loop expects a value-receiver Model
// it can copy on every event.
func New(mgr *cron.Manager) Model {
	ti := textinput.New()
	ti.Placeholder = "filter"
	ti.Prompt = "/ "
	ti.CharLimit = 128

	sched := textinput.New()
	sched.Placeholder = "@daily, @every 5m, * * * * *"
	sched.Prompt = "schedule: "
	sched.CharLimit = 128

	cmdIn := textinput.New()
	cmdIn.Placeholder = "/usr/local/bin/foo --flag"
	cmdIn.Prompt = "command:  "
	cmdIn.CharLimit = 512

	return Model{
		mgr:          mgr,
		keys:         newKeyMap(),
		theme:        newTheme(),
		view:         viewList,
		filter:       ti,
		formSchedule: sched,
		formCommand:  cmdIn,
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

func addCmd(mgr *cron.Manager, spec cron.JobSpec) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), deleteTimeout)
		defer cancel()
		j, err := mgr.Add(ctx, "", spec)
		if err != nil {
			return flashMsg{text: "add failed: " + err.Error()}
		}
		return flashMsg{text: "added " + j.ID, ok: true}
	}
}

func editCmd(mgr *cron.Manager, id string, spec cron.JobSpec) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), deleteTimeout)
		defer cancel()
		j, err := mgr.Edit(ctx, id, spec)
		if err != nil {
			return flashMsg{text: "edit failed: " + err.Error()}
		}
		return flashMsg{text: "edited " + j.ID, ok: true}
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
