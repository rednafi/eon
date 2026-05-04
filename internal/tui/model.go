package tui

import (
	"context"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/rednafi/eon/internal/origin"
)

// view enumerates the top-level UI states. We keep them as ints (not booleans)
// because future states (settings, error overlay) slot in without churn.
type view int

const (
	viewList view = iota
	viewDetail
	viewConfirmDelete
)

// detailTab indexes the tabs shown in viewDetail. Order is significant — the
// rendered tab strip iterates by enum value.
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

// Model is the bubbletea root model for eon.
type Model struct {
	mgr   *origin.Manager
	keys  keyMap
	theme theme

	view    view
	width   int
	height  int
	jobs    []origin.Job
	loadErr string

	// list state
	cursor   int
	filter   textinput.Model
	filterOn bool

	// detail state
	detailTab detailTab
	detailVP  viewport.Model
	logsVP    viewport.Model
	rawVP     viewport.Model

	// pending confirmation
	pendingDelete origin.Job
	flash         string
	flashUntil    time.Time
}

// New constructs an initial Model. The Manager must be ready to List.
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

// Init implements tea.Model.
func (m Model) Init() tea.Cmd {
	return tea.Batch(reload(m.mgr), tea.EnterAltScreen)
}

// jobsLoadedMsg is published when a list refresh completes.
type jobsLoadedMsg struct {
	jobs []origin.Job
	err  string
}

// flashMsg shows a transient one-liner at the bottom (e.g. "deleted X").
type flashMsg struct{ text string }

// reload is the tea.Cmd that fetches the current job list. We give it a short
// timeout so a stuck launchctl/systemctl can't hang the UI forever.
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

// deleteCmd issues a delete and returns a flashMsg for the result.
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
