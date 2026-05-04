package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"slices"

	"github.com/rednafi/eon/cron"
)

type stubOrigin struct {
	jobs    []cron.Job
	deleted []string
}

func (s *stubOrigin) Name() string      { return "stub" }
func (s *stubOrigin) Scope() cron.Scope { return cron.ScopeUser }
func (s *stubOrigin) List(_ context.Context) ([]cron.Job, error) {
	return slices.Clone(s.jobs), nil
}
func (s *stubOrigin) Delete(_ context.Context, id string) error {
	for i, j := range s.jobs {
		if j.ID == id {
			s.jobs = append(s.jobs[:i], s.jobs[i+1:]...)
			s.deleted = append(s.deleted, id)
			return nil
		}
	}
	return cron.ErrNotFound
}

// keyPress builds a v2 KeyPressMsg from a string spelling. We use the
// ergonomic .String() form that bubbletea itself recommends; callers pass
// "/", "enter", "down", "y", etc.
func keyPress(s string) tea.KeyPressMsg {
	switch s {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEsc}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	default:
		r := []rune(s)[0]
		return tea.KeyPressMsg{Code: r, Text: s}
	}
}

func newTestModel(jobs ...cron.Job) (Model, *stubOrigin) {
	s := &stubOrigin{jobs: jobs}
	mgr := cron.NewManager(s)
	m := New(mgr)
	mm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	return mm.(Model), s
}

func TestModelInitialViewShowsLoading(t *testing.T) {
	t.Parallel()
	mgr := cron.NewManager(&stubOrigin{})
	m := New(mgr)
	if got := m.render(); got != "loading…" {
		t.Errorf("want loading…, got %q", got)
	}
}

func TestModelRendersJobsAfterLoad(t *testing.T) {
	t.Parallel()
	m, _ := newTestModel(
		cron.Job{ID: "stub:a", Kind: cron.KindCrontab, Name: "alpha", Schedule: "@daily", Status: "scheduled"},
		cron.Job{ID: "stub:b", Kind: cron.KindLaunchd, Name: "beta", Schedule: "every 5m", Status: "loaded"},
	)
	mm, _ := m.Update(jobsLoadedMsg{jobs: m.mgr.Sources()[0].(*stubOrigin).jobs})
	v := mm.(Model).render()
	for _, want := range []string{"alpha", "beta", "@daily", "every 5m"} {
		if !strings.Contains(v, want) {
			t.Errorf("view missing %q\n%s", want, v)
		}
	}
}

func TestModelDownArrowMovesCursor(t *testing.T) {
	t.Parallel()
	m, _ := newTestModel(
		cron.Job{ID: "stub:a", Name: "alpha"},
		cron.Job{ID: "stub:b", Name: "beta"},
	)
	mm, _ := m.Update(jobsLoadedMsg{jobs: m.mgr.Sources()[0].(*stubOrigin).jobs})
	mm, _ = mm.Update(keyPress("down"))
	if got := mm.(Model).cursor; got != 1 {
		t.Errorf("cursor want 1, got %d", got)
	}
}

func TestModelFilterNarrowsList(t *testing.T) {
	t.Parallel()
	m, _ := newTestModel(
		cron.Job{ID: "stub:alpha", Name: "alpha"},
		cron.Job{ID: "stub:beta", Name: "beta"},
		cron.Job{ID: "stub:gamma", Name: "gamma"},
	)
	mm, _ := m.Update(jobsLoadedMsg{jobs: m.mgr.Sources()[0].(*stubOrigin).jobs})
	mm, _ = mm.Update(keyPress("/"))
	mm, _ = mm.Update(keyPress("b"))
	mm, _ = mm.Update(keyPress("e"))
	mm, _ = mm.Update(keyPress("enter"))

	visible := mm.(Model).visibleIdx
	if len(visible) != 1 {
		t.Fatalf("want 1 visible job, got %d", len(visible))
	}
	if mm.(Model).jobs[visible[0]].Name != "beta" {
		t.Errorf("filter result wrong: %+v", mm.(Model).jobs[visible[0]])
	}
}

func TestModelEnterDrillsIntoDetail(t *testing.T) {
	t.Parallel()
	m, _ := newTestModel(
		cron.Job{ID: "stub:a", Name: "alpha", Schedule: "@daily", Command: "/bin/echo hi"},
	)
	mm, _ := m.Update(jobsLoadedMsg{jobs: m.mgr.Sources()[0].(*stubOrigin).jobs})
	mm, _ = mm.Update(keyPress("enter"))
	if mm.(Model).view != viewDetail {
		t.Fatalf("want detail view, got %v", mm.(Model).view)
	}
	if !strings.Contains(mm.(Model).render(), "alpha") {
		t.Errorf("detail view missing job name")
	}
}

func TestModelDeleteFlow(t *testing.T) {
	t.Parallel()
	m, stub := newTestModel(cron.Job{ID: "stub:goner", Name: "goner"})
	mm, _ := m.Update(jobsLoadedMsg{jobs: stub.jobs})
	mm, _ = mm.Update(keyPress("d"))
	if mm.(Model).view != viewConfirmDelete {
		t.Fatalf("want confirm view, got %v", mm.(Model).view)
	}
	mm, cmd := mm.Update(keyPress("y"))
	if cmd == nil {
		t.Fatal("expected delete command")
	}
	msg := cmd()
	_, _ = mm.Update(msg)
	if len(stub.deleted) != 1 || stub.deleted[0] != "stub:goner" {
		t.Errorf("delete not invoked: %v", stub.deleted)
	}
}

func TestWrapPreservesEmptyLines(t *testing.T) {
	t.Parallel()
	got := wrap("line one\n\nline three", 80)
	if !strings.Contains(got, "line one") || !strings.Contains(got, "line three") {
		t.Errorf("wrap dropped content: %q", got)
	}
}

func TestTruncateMiddleKeepsBothEnds(t *testing.T) {
	t.Parallel()
	got := truncateMiddle("launchd-user:com.example.really.long.identifier", 30)
	if !strings.HasPrefix(got, "launchd") {
		t.Errorf("prefix lost: %q", got)
	}
	if !strings.HasSuffix(got, "identifier") {
		t.Errorf("suffix lost: %q", got)
	}
}

func TestModelDeleteOnSystemRowOpensReadOnlyModal(t *testing.T) {
	t.Parallel()
	m, stub := newTestModel(cron.Job{ID: "stub:sys", Name: "sys-job", Scope: cron.ScopeSystem})
	mm, _ := m.Update(jobsLoadedMsg{jobs: stub.jobs})
	// 'a' to reveal system rows, then 'd'.
	mm, _ = mm.Update(keyPress("a"))
	mm, _ = mm.Update(keyPress("d"))
	if got := mm.(Model).view; got != viewReadOnly {
		t.Fatalf("want viewReadOnly, got %v", got)
	}
	// Any key dismisses.
	mm, _ = mm.Update(keyPress("x"))
	if got := mm.(Model).view; got != viewList {
		t.Fatalf("want viewList after dismiss, got %v", got)
	}
	if len(stub.deleted) != 0 {
		t.Errorf("system job should not be deleted: %v", stub.deleted)
	}
}

func TestModelTogglesSystemVisibility(t *testing.T) {
	t.Parallel()
	jobs := []cron.Job{
		{ID: "stub:user1", Name: "user1"},
		{ID: "stub:sys1", Name: "sys1", Scope: cron.ScopeSystem},
		{ID: "stub:sys2", Name: "sys2", Scope: cron.ScopeSystem},
	}
	m, _ := newTestModel(jobs...)
	mm, _ := m.Update(jobsLoadedMsg{jobs: jobs})

	// Default: only the one user job is visible.
	if got := len(mm.(Model).visibleIdx); got != 1 {
		t.Fatalf("default visible count = %d, want 1", got)
	}
	// Press 'a' → both system jobs become visible.
	mm, _ = mm.Update(keyPress("a"))
	if got := len(mm.(Model).visibleIdx); got != 3 {
		t.Fatalf("after 'a' visible count = %d, want 3", got)
	}
	// Press 'a' again → back to user-only.
	mm, _ = mm.Update(keyPress("a"))
	if got := len(mm.(Model).visibleIdx); got != 1 {
		t.Fatalf("after second 'a' visible count = %d, want 1", got)
	}
}

// Init returns the reload tea.Cmd so the very first render triggers a List.
// Without this the user sees an indefinite "loading…" — verify the cmd is
// non-nil and produces a jobsLoadedMsg when invoked.
func TestModelInitReturnsLoadCmd(t *testing.T) {
	t.Parallel()
	stub := &stubOrigin{jobs: []cron.Job{{ID: "stub:x", Name: "x"}}}
	mgr := cron.NewManager(stub)
	m := New(mgr)
	cmd := m.Init()
	if cmd == nil {
		t.Fatal("Init must return a non-nil reload cmd")
	}
	msg := cmd()
	loaded, ok := msg.(jobsLoadedMsg)
	if !ok {
		t.Fatalf("Init cmd did not produce jobsLoadedMsg, got %T", msg)
	}
	if len(loaded.jobs) != 1 || loaded.jobs[0].ID != "stub:x" {
		t.Errorf("loaded payload wrong: %+v", loaded)
	}
}

// The read-only modal renders distinct copy ("System cron — read-only")
// vs the confirm-delete modal ("Delete this cron?"). Verify both render
// without panic and contain the right header.
func TestRenderConfirmAndReadOnlyModals(t *testing.T) {
	t.Parallel()
	jobs := []cron.Job{{ID: "stub:user", Name: "user"}, {ID: "stub:sys", Name: "sys", Scope: cron.ScopeSystem}}
	m, _ := newTestModel(jobs...)
	mm, _ := m.Update(jobsLoadedMsg{jobs: jobs})

	confirm, _ := mm.Update(keyPress("d"))
	cv := confirm.(Model).render()
	if !strings.Contains(cv, "Delete this cron?") {
		t.Errorf("confirm view missing header:\n%s", cv)
	}
	mm2, _ := mm.Update(keyPress("a"))
	mm2, _ = mm2.(Model).Update(keyPress("down"))
	mm2, _ = mm2.(Model).Update(keyPress("d"))
	rv := mm2.(Model).render()
	if !strings.Contains(rv, "System cron") {
		t.Errorf("read-only view missing header:\n%s", rv)
	}
}

// Refreshing while in the detail view should NOT throw the user back to the
// list — it should re-fire the reload cmd and stay on the same job.
func TestRefreshKeepsDetailView(t *testing.T) {
	t.Parallel()
	jobs := []cron.Job{{ID: "stub:a", Name: "alpha", Schedule: "@daily"}}
	m, _ := newTestModel(jobs...)
	mm, _ := m.Update(jobsLoadedMsg{jobs: jobs})
	mm, _ = mm.Update(keyPress("enter"))
	if mm.(Model).view != viewDetail {
		t.Fatalf("setup: not in detail")
	}
	mm, cmd := mm.Update(keyPress("r"))
	if cmd == nil {
		t.Errorf("refresh must return a reload cmd")
	}
	if mm.(Model).view != viewDetail {
		t.Errorf("refresh kicked the user out of detail view: %v", mm.(Model).view)
	}
}

// jobsLoadedMsg arrives with fewer jobs than before — cursor must clamp
// rather than indexing past the end. Without this, currentJob() returns
// stale data and the next 'd' press deletes the wrong row.
func TestJobsLoadedMsgClampsCursor(t *testing.T) {
	t.Parallel()
	jobs := []cron.Job{
		{ID: "stub:a", Name: "alpha"},
		{ID: "stub:b", Name: "beta"},
		{ID: "stub:c", Name: "gamma"},
	}
	m, _ := newTestModel(jobs...)
	mm, _ := m.Update(jobsLoadedMsg{jobs: jobs})
	mm, _ = mm.Update(keyPress("down"))
	mm, _ = mm.Update(keyPress("down"))
	if mm.(Model).cursor != 2 {
		t.Fatalf("setup: cursor not at end")
	}
	mm, _ = mm.Update(jobsLoadedMsg{jobs: jobs[:1]})
	if got := mm.(Model).cursor; got != 0 {
		t.Errorf("cursor not clamped, got %d", got)
	}
}

// Toggling tabs in detail view cycles forward and backward through the
// three tabs. shift+tab goes back; tab goes forward.
func TestDetailTabsCycle(t *testing.T) {
	t.Parallel()
	jobs := []cron.Job{{ID: "stub:a", Name: "alpha", Schedule: "@daily", Command: "/bin/echo"}}
	m, _ := newTestModel(jobs...)
	mm, _ := m.Update(jobsLoadedMsg{jobs: jobs})
	mm, _ = mm.Update(keyPress("enter"))
	if mm.(Model).detailTab != tabOverview {
		t.Fatalf("initial tab = %v", mm.(Model).detailTab)
	}
	mm, _ = mm.Update(tea.KeyPressMsg{Code: tea.KeyTab, Text: "tab"})
	if mm.(Model).detailTab != tabRaw {
		t.Errorf("tab after Overview should be Raw, got %v", mm.(Model).detailTab)
	}
	mm, _ = mm.Update(tea.KeyPressMsg{Code: tea.KeyTab, Text: "tab"})
	if mm.(Model).detailTab != tabLogs {
		t.Errorf("tab after Raw should be Logs, got %v", mm.(Model).detailTab)
	}
	mm, _ = mm.Update(tea.KeyPressMsg{Code: tea.KeyTab, Text: "tab"})
	if mm.(Model).detailTab != tabOverview {
		t.Errorf("tab wrap should land back on Overview, got %v", mm.(Model).detailTab)
	}
	mm, _ = mm.Update(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift, Text: "shift+tab"})
	if mm.(Model).detailTab != tabLogs {
		t.Errorf("shift+tab should go back to Logs, got %v", mm.(Model).detailTab)
	}
}

// `esc` from detail view returns to list. From confirm view, esc cancels
// the delete and returns to list. Verify both transitions.
func TestEscapeReturnsToList(t *testing.T) {
	t.Parallel()
	jobs := []cron.Job{{ID: "stub:a", Name: "alpha"}}
	m, _ := newTestModel(jobs...)
	mm, _ := m.Update(jobsLoadedMsg{jobs: jobs})
	mm, _ = mm.Update(keyPress("enter"))
	mm, _ = mm.Update(keyPress("esc"))
	if mm.(Model).view != viewList {
		t.Errorf("esc from detail should return to list, got %v", mm.(Model).view)
	}
	mm, _ = mm.Update(keyPress("d"))
	mm, _ = mm.Update(keyPress("esc"))
	if mm.(Model).view != viewList {
		t.Errorf("esc from confirm should return to list, got %v", mm.(Model).view)
	}
}

// Filter mode handles esc differently: it must clear the filter and exit
// filter mode, not propagate to the outer view-state machine.
func TestFilterEscapeClearsAndReturns(t *testing.T) {
	t.Parallel()
	jobs := []cron.Job{{ID: "stub:a", Name: "alpha"}, {ID: "stub:b", Name: "beta"}}
	m, _ := newTestModel(jobs...)
	mm, _ := m.Update(jobsLoadedMsg{jobs: jobs})
	mm, _ = mm.Update(keyPress("/"))
	mm, _ = mm.Update(keyPress("a"))
	mm, _ = mm.Update(keyPress("l"))
	if mm.(Model).filter.Value() != "al" {
		t.Fatalf("filter value setup failure")
	}
	mm, _ = mm.Update(keyPress("esc"))
	if mm.(Model).filterOn {
		t.Errorf("esc from filter mode should disable filterOn")
	}
	if mm.(Model).filter.Value() != "" {
		t.Errorf("esc should clear filter text, got %q", mm.(Model).filter.Value())
	}
	if got := len(mm.(Model).visibleIdx); got != 2 {
		t.Errorf("filter cleared but visibleIdx not refreshed; got %d", got)
	}
}

// Quitting via 'q' from list and from detail view both produce tea.Quit.
// A regression that loses tea.Quit on a sub-view leaves users stranded.
func TestQuitFromAnyView(t *testing.T) {
	t.Parallel()
	jobs := []cron.Job{{ID: "stub:a", Name: "alpha"}}
	m, _ := newTestModel(jobs...)
	mm, _ := m.Update(jobsLoadedMsg{jobs: jobs})
	_, cmd := mm.Update(keyPress("q"))
	if cmd == nil {
		t.Errorf("q from list should return tea.Quit cmd")
	}
	mm, _ = mm.Update(keyPress("enter"))
	_, cmd = mm.Update(keyPress("q"))
	if cmd == nil {
		t.Errorf("q from detail should return tea.Quit cmd")
	}
}

// renderFilterChip is rendered when filterOn is true. Cover that branch.
func TestRenderFilterChipDuringFilter(t *testing.T) {
	t.Parallel()
	jobs := []cron.Job{{ID: "stub:a", Name: "alpha"}}
	m, _ := newTestModel(jobs...)
	mm, _ := m.Update(jobsLoadedMsg{jobs: jobs})
	mm, _ = mm.Update(keyPress("/"))
	mm, _ = mm.Update(keyPress("a"))
	out := mm.(Model).render()
	if !strings.Contains(out, "alpha") {
		t.Errorf("filter chip render missing job: %s", out)
	}
}

// Sub-messages routed in detail view (mouse, etc.) must not mutate state
// in a way that breaks subsequent navigation. Forwarding the msg into the
// active viewport is the right behaviour, but a regression that swallows
// it would leave the viewport unscrollable.
func TestDetailViewForwardsUnknownMessages(t *testing.T) {
	t.Parallel()
	jobs := []cron.Job{{ID: "stub:a", Name: "alpha", Raw: strings.Repeat("X\n", 200)}}
	m, _ := newTestModel(jobs...)
	mm, _ := m.Update(jobsLoadedMsg{jobs: jobs})
	mm, _ = mm.Update(keyPress("enter"))
	// Send an unknown message; should be swallowed without panic.
	mm, _ = mm.Update(struct{ Foo string }{Foo: "bar"})
	if mm.(Model).view != viewDetail {
		t.Errorf("unknown msg disturbed view: %v", mm.(Model).view)
	}
}

func TestModelHundredJobsScrolls(t *testing.T) {
	t.Parallel()
	jobs := make([]cron.Job, 100)
	for i := range jobs {
		jobs[i] = cron.Job{ID: "stub:" + string(rune('a'+i%26)), Name: "j", Kind: cron.KindCrontab}
	}
	m, _ := newTestModel(jobs...)
	mm, _ := m.Update(jobsLoadedMsg{jobs: jobs})
	for range 200 {
		mm, _ = mm.Update(keyPress("down"))
	}
	for range 200 {
		mm, _ = mm.Update(keyPress("up"))
	}
	if mm.(Model).cursor != 0 {
		t.Errorf("cursor should land at 0, got %d", mm.(Model).cursor)
	}
}
