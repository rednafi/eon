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
