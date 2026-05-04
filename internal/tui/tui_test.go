package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/rednafi/eon/internal/origin"
)

// stubOrigin is a no-op Origin used to instantiate a Manager for the TUI.
// We deliberately don't run the bubbletea program loop — the unit tests below
// exercise the pure Update/View logic with synthetic messages.
type stubOrigin struct {
	jobs    []origin.Job
	deleted []string
}

func (s *stubOrigin) Name() string { return "stub" }
func (s *stubOrigin) List(_ context.Context) ([]origin.Job, error) {
	return append([]origin.Job(nil), s.jobs...), nil
}
func (s *stubOrigin) Delete(_ context.Context, id string) error {
	for i, j := range s.jobs {
		if j.ID == id {
			s.jobs = append(s.jobs[:i], s.jobs[i+1:]...)
			s.deleted = append(s.deleted, id)
			return nil
		}
	}
	return origin.ErrNotFound
}

func newTestModel(jobs ...origin.Job) (Model, *stubOrigin) {
	s := &stubOrigin{jobs: jobs}
	mgr := origin.NewManager(s)
	m := New(mgr)
	// Apply a known size so View() can compute layout.
	mm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	return mm.(Model), s
}

func TestModelInitialViewShowsLoading(t *testing.T) {
	mgr := origin.NewManager(&stubOrigin{})
	m := New(mgr)
	if got := m.View(); got != "loading…" {
		t.Errorf("want loading…, got %q", got)
	}
}

func TestModelRendersJobsAfterLoad(t *testing.T) {
	m, _ := newTestModel(
		origin.Job{ID: "stub:a", Kind: origin.KindCrontab, Name: "alpha", Schedule: "@daily", Status: "scheduled"},
		origin.Job{ID: "stub:b", Kind: origin.KindLaunchd, Name: "beta", Schedule: "every 5m", Status: "loaded"},
	)
	mm, _ := m.Update(jobsLoadedMsg{jobs: m.mgr.Origins()[0].(*stubOrigin).jobs})
	v := mm.(Model).View()
	for _, want := range []string{"alpha", "beta", "@daily", "every 5m"} {
		if !strings.Contains(v, want) {
			t.Errorf("view missing %q\n%s", want, v)
		}
	}
}

func TestModelDownArrowMovesCursor(t *testing.T) {
	m, _ := newTestModel(
		origin.Job{ID: "stub:a", Name: "alpha"},
		origin.Job{ID: "stub:b", Name: "beta"},
	)
	mm, _ := m.Update(jobsLoadedMsg{jobs: m.mgr.Origins()[0].(*stubOrigin).jobs})
	mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyDown})
	if got := mm.(Model).cursor; got != 1 {
		t.Errorf("cursor want 1, got %d", got)
	}
}

func TestModelFilterNarrowsList(t *testing.T) {
	m, _ := newTestModel(
		origin.Job{ID: "stub:alpha", Name: "alpha"},
		origin.Job{ID: "stub:beta", Name: "beta"},
		origin.Job{ID: "stub:gamma", Name: "gamma"},
	)
	mm, _ := m.Update(jobsLoadedMsg{jobs: m.mgr.Origins()[0].(*stubOrigin).jobs})
	// Press '/' to enter filter mode, then type "be".
	mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("b")})
	mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")})
	mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyEnter}) // commit filter

	visible := mm.(Model).filteredIndexes()
	if len(visible) != 1 {
		t.Fatalf("want 1 visible job, got %d", len(visible))
	}
	if mm.(Model).jobs[visible[0]].Name != "beta" {
		t.Errorf("filter result wrong: %+v", mm.(Model).jobs[visible[0]])
	}
}

func TestModelEnterDrillsIntoDetail(t *testing.T) {
	m, _ := newTestModel(
		origin.Job{ID: "stub:a", Name: "alpha", Schedule: "@daily", Command: "/bin/echo hi"},
	)
	mm, _ := m.Update(jobsLoadedMsg{jobs: m.mgr.Origins()[0].(*stubOrigin).jobs})
	mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if mm.(Model).view != viewDetail {
		t.Fatalf("want detail view, got %v", mm.(Model).view)
	}
	if !strings.Contains(mm.View(), "alpha") {
		t.Errorf("detail view missing job name")
	}
}

func TestModelDeleteFlow(t *testing.T) {
	m, stub := newTestModel(origin.Job{ID: "stub:goner", Name: "goner"})
	mm, _ := m.Update(jobsLoadedMsg{jobs: stub.jobs})
	mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	if mm.(Model).view != viewConfirmDelete {
		t.Fatalf("want confirm view, got %v", mm.(Model).view)
	}
	mm, cmd := mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	if cmd == nil {
		t.Fatal("expected delete command")
	}
	// Run the cmd synchronously to actually invoke Manager.Delete.
	msg := cmd()
	_, _ = mm.Update(msg)
	if len(stub.deleted) != 1 || stub.deleted[0] != "stub:goner" {
		t.Errorf("delete not invoked: %v", stub.deleted)
	}
}

func TestWrapPreservesEmptyLines(t *testing.T) {
	got := wrap("line one\n\nline three", 80)
	if !strings.Contains(got, "line one") || !strings.Contains(got, "line three") {
		t.Errorf("wrap dropped content: %q", got)
	}
}

func TestTruncateMiddleKeepsBothEnds(t *testing.T) {
	got := truncateMiddle("launchd-user:com.example.really.long.identifier", 30)
	if !strings.HasPrefix(got, "launchd") {
		t.Errorf("prefix lost: %q", got)
	}
	if !strings.HasSuffix(got, "identifier") {
		t.Errorf("suffix lost: %q", got)
	}
}

// TestModelHundredJobsScrolls confirms the list view can render a long job
// set without panicking. We don't assert on cursor visibility (that depends
// on terminal size); we just assert the model survives 100 down-arrows and
// 100 up-arrows.
func TestModelHundredJobsScrolls(t *testing.T) {
	jobs := make([]origin.Job, 100)
	for i := range jobs {
		jobs[i] = origin.Job{ID: "stub:" + string(rune('a'+i%26)), Name: "j", Kind: origin.KindCrontab}
	}
	m, _ := newTestModel(jobs...)
	mm, _ := m.Update(jobsLoadedMsg{jobs: jobs})
	for i := 0; i < 200; i++ {
		mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyDown})
	}
	for i := 0; i < 200; i++ {
		mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyUp})
	}
	if mm.(Model).cursor != 0 {
		t.Errorf("cursor should land at 0, got %d", mm.(Model).cursor)
	}
}
