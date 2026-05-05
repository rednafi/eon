package tui

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/rednafi/eon/cron"
)

// mutStubOrigin extends stubOrigin (defined in tui_test.go) with cron.Mutator
// semantics. Lets the integration tests exercise the add/edit form against
// an in-memory backend without a real plist or crontab.
type mutStubOrigin struct {
	stubOrigin
	added  []cron.JobSpec
	edited map[string]cron.JobSpec
}

func (m *mutStubOrigin) Add(_ context.Context, spec cron.JobSpec) (cron.Job, error) {
	m.added = append(m.added, spec)
	j := cron.Job{
		ID: fmt.Sprintf("stub:add-%d", len(m.added)),
		Kind: cron.KindCrontab, Name: spec.Command,
		Schedule: spec.Schedule, Command: spec.Command,
	}
	m.jobs = append(m.jobs, j)
	return j, nil
}

func (m *mutStubOrigin) Edit(_ context.Context, id string, spec cron.JobSpec) (cron.Job, error) {
	if m.edited == nil {
		m.edited = map[string]cron.JobSpec{}
	}
	for i, j := range m.jobs {
		if j.ID == id {
			m.jobs[i].Schedule = spec.Schedule
			m.jobs[i].Command = spec.Command
			m.edited[id] = spec
			return m.jobs[i], nil
		}
	}
	return cron.Job{}, cron.ErrNotFound
}

// driver simulates a small slice of the bubbletea event loop: every Cmd
// returned by Update is invoked synchronously and its Msg fed back in.
// Stops after `maxSteps` iterations or when no Cmd is queued. We don't
// need full async semantics — the eon TUI uses ContextWithTimeout and
// returns simple flashMsg/jobsLoadedMsg values, both of which can be
// resolved on the calling goroutine.
type driver struct {
	t *testing.T
	m Model
}

func newDriver(t *testing.T, jobs ...cron.Job) (*driver, *mutStubOrigin) {
	t.Helper()
	stub := &mutStubOrigin{stubOrigin: stubOrigin{jobs: jobs}}
	mgr := cron.NewManager(stub)
	m := New(mgr)
	mm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	mm, _ = mm.Update(jobsLoadedMsg{jobs: jobs})
	return &driver{t: t, m: mm.(Model)}, stub
}

func (d *driver) send(msg tea.Msg) {
	d.t.Helper()
	mm, cmd := d.m.Update(msg)
	d.m = mm.(Model)
	d.drain(cmd)
}

// drain executes any pending Cmd, feeds its Msg back, and recurses. Each
// Cmd runs with a hard deadline so cursor-blink-style infinite tickers
// don't pin the test for seconds; we treat such "still pending" Cmds as
// done since they're not relevant to the assertions integration tests
// care about.
func (d *driver) drain(cmd tea.Cmd) {
	d.t.Helper()
	for i := 0; cmd != nil && i < 16; i++ {
		done := make(chan tea.Msg, 1)
		go func(c tea.Cmd) { done <- c() }(cmd)
		var msg tea.Msg
		select {
		case msg = <-done:
		case <-time.After(50 * time.Millisecond):
			return // give up on async/blinking cmds
		}
		if msg == nil {
			return
		}
		mm, next := d.m.Update(msg)
		d.m = mm.(Model)
		cmd = next
	}
}

func (d *driver) press(s string) { d.send(keyPress(s)) }

func (d *driver) view() string { return d.m.render() }

// TestIntegrationFilterThenDelete exercises the full flow:
// load jobs → '/' filter → type 'b' → enter to commit filter → 'd' to
// delete → 'y' to confirm → reload → verify the original 3 jobs are now 2.
func TestIntegrationFilterThenDelete(t *testing.T) {
	t.Parallel()
	d, stub := newDriver(t,
		cron.Job{ID: "stub:1", Name: "alpha", Schedule: "@daily"},
		cron.Job{ID: "stub:2", Name: "bravo", Schedule: "@daily"},
		cron.Job{ID: "stub:3", Name: "charlie", Schedule: "@daily"},
	)

	d.press("/")
	d.press("b")
	d.press("r") // bravo
	d.press("enter")

	if got := len(d.m.visibleIdx); got != 1 {
		t.Fatalf("filter narrowed to %d, want 1", got)
	}
	d.press("d") // delete confirm
	d.press("y")

	// Wait for async-ish reload to settle. The driver already drains all
	// cmds; verify the stub recorded the deletion.
	if len(stub.deleted) != 1 || stub.deleted[0] != "stub:2" {
		t.Errorf("delete didn't reach stub correctly: %+v", stub.deleted)
	}
	if len(stub.jobs) != 2 {
		t.Errorf("stub jobs after delete = %d, want 2", len(stub.jobs))
	}
}

// TestIntegrationAddViaForm: 'n' opens form, type schedule + tab + command,
// enter submits, mgr.Add called, list contains the new job.
func TestIntegrationAddViaForm(t *testing.T) {
	t.Parallel()
	d, stub := newDriver(t)

	d.press("n")
	if d.m.view != viewForm {
		t.Fatalf("setup: not in form view")
	}
	for _, ch := range "@daily" {
		d.press(string(ch))
	}
	d.send(tea.KeyPressMsg{Code: tea.KeyTab, Text: "tab"})
	for _, ch := range "/bin/echo hi" {
		d.press(string(ch))
	}
	d.press("enter")

	if len(stub.added) != 1 {
		t.Fatalf("add did not reach the stub: %+v", stub.added)
	}
	if stub.added[0].Schedule != "@daily" || stub.added[0].Command != "/bin/echo hi" {
		t.Errorf("add spec wrong: %+v", stub.added[0])
	}
	if d.m.view != viewList {
		t.Errorf("after submit should return to list, got %v", d.m.view)
	}
}

// TestIntegrationEditViaForm: 'e' on an existing row pre-fills the form;
// caret-key edit + enter submits, mgr.Edit called with new fields.
func TestIntegrationEditViaForm(t *testing.T) {
	t.Parallel()
	d, stub := newDriver(t,
		cron.Job{ID: "stub:1", Name: "alpha", Schedule: "@daily", Command: "/bin/old"},
	)
	d.press("e")
	if d.m.view != viewForm {
		t.Fatalf("setup: not in form view")
	}
	if d.m.formSchedule.Value() != "@daily" {
		t.Errorf("setup: schedule prefill missing")
	}
	// Replace the schedule by clearing and typing.
	d.m.formSchedule.SetValue("@hourly")
	d.press("enter")

	if got := stub.edited["stub:1"]; got.Schedule != "@hourly" {
		t.Errorf("edit not propagated: %+v", got)
	}
}

// TestIntegrationDeniedDeleteKeepsJob: 'd' then 'n' (cancel) leaves the
// job alone.
func TestIntegrationDeniedDeleteKeepsJob(t *testing.T) {
	t.Parallel()
	d, stub := newDriver(t,
		cron.Job{ID: "stub:keep", Name: "keep"},
	)
	d.press("d")
	if d.m.view != viewConfirmDelete {
		t.Fatalf("setup: not in confirm view")
	}
	d.press("n") // cancel

	if d.m.view != viewList {
		t.Errorf("after cancel should be on list, got %v", d.m.view)
	}
	if len(stub.deleted) != 0 {
		t.Errorf("cancelled delete should not have reached the stub: %v", stub.deleted)
	}
}

// TestIntegrationFlashClearsAfterTimeout: a flashMsg sets flash + flashUntil
// in the future; after the deadline render() should not include the flash.
// We don't actually wait — we just shift flashUntil into the past and check
// the renderer behaves.
func TestIntegrationFlashTimeout(t *testing.T) {
	t.Parallel()
	d, _ := newDriver(t, cron.Job{ID: "stub:1", Name: "alpha"})
	d.send(flashMsg{text: "deleted stub:1", ok: false})
	if !strings.Contains(d.view(), "deleted stub:1") {
		t.Errorf("flash text missing from render:\n%s", d.view())
	}
	d.m.flashUntil = time.Now().Add(-time.Second)
	if strings.Contains(d.view(), "deleted stub:1") {
		t.Errorf("expired flash should not render:\n%s", d.view())
	}
}
