package store

import (
	"errors"
	"io"
	"testing"
	"time"

	"github.com/rednafi/eon"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	r, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = r.Close() })
	return r
}

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	got, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	return got
}

func TestStoreJobRoundtrip(t *testing.T) {
	r := newStore(t)
	ctx := t.Context()
	now := mustTime(t, "2026-05-13T10:00:00Z")

	spec := eon.JobSpec{Name: "ping", Command: []string{"echo", "hi"}, Cron: "@hourly"}
	got, err := r.AddJob(ctx, spec, now)
	if err != nil {
		t.Fatalf("AddJob: %v", err)
	}
	if got.ID == "" || got.Kind != eon.KindCron || got.Status != eon.StatusEnabled {
		t.Fatalf("AddJob returned %+v", got)
	}
	if len(got.Command) != 2 || got.Command[0] != "echo" {
		t.Fatalf("AddJob command = %v", got.Command)
	}

	fetched, err := r.Job(ctx, got.ID)
	if err != nil {
		t.Fatalf("Job lookup: %v", err)
	}
	if fetched.Name != "ping" {
		t.Fatalf("Job(name) = %q", fetched.Name)
	}

	if err := r.DeleteJob(ctx, got.ID); err != nil {
		t.Fatalf("DeleteJob: %v", err)
	}
	if _, err := r.Job(ctx, got.ID); !errors.Is(err, eon.ErrNotFound) {
		t.Fatalf("Job(after delete) = %v, want ErrNotFound", err)
	}
}

// TestStoreListJobsOrder pins the "ls is reverse-chronological by
// creation" invariant. ID-order is meaningless with random IDs;
// users expect the most-recently-added job at the top.
func TestStoreListJobsOrder(t *testing.T) {
	r := newStore(t)
	ctx := t.Context()
	base := mustTime(t, "2026-05-13T10:00:00Z")

	for i, name := range []string{"oldest", "middle", "newest"} {
		when := base.Add(time.Duration(i) * time.Minute)
		if _, err := r.AddJob(ctx, eon.JobSpec{
			Name: name, Command: []string{"echo"}, Cron: "@hourly",
		}, when); err != nil {
			t.Fatal(err)
		}
	}
	jobs, err := r.ListJobs(ctx, ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 3 {
		t.Fatalf("len=%d, want 3", len(jobs))
	}
	want := []string{"newest", "middle", "oldest"}
	for i, j := range jobs {
		if j.Name != want[i] {
			t.Errorf("jobs[%d].Name = %q, want %q", i, j.Name, want[i])
		}
	}
}

func TestStoreListFilters(t *testing.T) {
	r := newStore(t)
	ctx := t.Context()
	now := mustTime(t, "2026-05-13T10:00:00Z")
	future := now.Add(time.Hour)

	_, err := r.AddJob(ctx, eon.JobSpec{Name: "c1", Command: []string{"echo"}, Cron: "@hourly"}, now)
	if err != nil {
		t.Fatal(err)
	}
	one, err := r.AddJob(ctx, eon.JobSpec{Name: "o1", Command: []string{"echo"}, FireAt: future}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.SetJobStatus(ctx, one.ID, eon.StatusDisabled, now); err != nil {
		t.Fatal(err)
	}

	all, err := r.ListJobs(ctx, ListOpts{})
	if err != nil || len(all) != 2 {
		t.Fatalf("ListJobs all: len=%d err=%v", len(all), err)
	}
	cronOnly, err := r.ListJobs(ctx, ListOpts{Kind: eon.KindCron})
	if err != nil || len(cronOnly) != 1 || cronOnly[0].Name != "c1" {
		t.Fatalf("ListJobs cron: %+v err=%v", cronOnly, err)
	}
	enabledOnly, err := r.ListJobs(ctx, ListOpts{Status: eon.StatusEnabled})
	if err != nil || len(enabledOnly) != 1 || enabledOnly[0].Name != "c1" {
		t.Fatalf("ListJobs enabled: %+v err=%v", enabledOnly, err)
	}
}

func TestStoreRunLifecycleAndLog(t *testing.T) {
	r := newStore(t)
	ctx := t.Context()
	now := mustTime(t, "2026-05-13T10:00:00Z")

	job, err := r.AddJob(ctx, eon.JobSpec{Name: "c", Command: []string{"echo"}, Cron: "@hourly"}, now)
	if err != nil {
		t.Fatal(err)
	}

	run, err := r.RecordRun(ctx, job.ID, now, now.Add(time.Second), 0, eon.RunOK, []byte("hello\n"))
	if err != nil {
		t.Fatalf("RecordRun: %v", err)
	}
	if run.Status != eon.RunOK || run.ExitCode != 0 {
		t.Fatalf("RecordRun = %+v", run)
	}
	if err := r.MarkJobRan(ctx, job.ID, eon.RunOK, now.Add(time.Second)); err != nil {
		t.Fatalf("MarkJobRan: %v", err)
	}

	latest, err := r.LatestRun(ctx, job.ID)
	if err != nil {
		t.Fatalf("LatestRun: %v", err)
	}
	if latest.Status != eon.RunOK || latest.ExitCode != 0 {
		t.Fatalf("LatestRun = %+v", latest)
	}

	rc, err := r.OpenRunLog(ctx, latest.ID)
	if err != nil {
		t.Fatalf("OpenRunLog: %v", err)
	}
	defer rc.Close()
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != "hello\n" {
		t.Fatalf("log content = %q", got)
	}
}

func TestStoreRecordOverlap(t *testing.T) {
	r := newStore(t)
	ctx := t.Context()
	now := mustTime(t, "2026-05-13T10:00:00Z")

	job, err := r.AddJob(ctx, eon.JobSpec{Name: "c", Command: []string{"echo"}, Cron: "@hourly"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.RecordOverlap(ctx, job.ID, now); err != nil {
		t.Fatalf("RecordOverlap: %v", err)
	}
	runs, err := r.ListRuns(ctx, job.ID, 0)
	if err != nil || len(runs) != 1 || runs[0].Status != eon.RunSkippedOverlap {
		t.Fatalf("ListRuns = %+v err=%v", runs, err)
	}
}

func TestStoreGCEnforcesRetention(t *testing.T) {
	r := newStore(t)
	ctx := t.Context()
	now := mustTime(t, "2026-05-13T10:00:00Z")

	job, err := r.AddJob(ctx, eon.JobSpec{Name: "c", Command: []string{"echo"}, Cron: "@hourly"}, now)
	if err != nil {
		t.Fatal(err)
	}

	// Force a tight retention so we can exercise both axes.
	const perJob = 3

	// Five runs across two days; the GC should keep the 3 most recent
	// AND prune anything older than RetentionMaxAge (the oldest is
	// fine for that axis since it is still within 100 days).
	starts := []time.Time{
		now.Add(-4 * time.Hour),
		now.Add(-3 * time.Hour),
		now.Add(-2 * time.Hour),
		now.Add(-1 * time.Hour),
		now,
	}
	for _, s := range starts {
		if _, err := r.RecordRun(ctx, job.ID, s, s.Add(time.Second), 0, eon.RunOK, nil); err != nil {
			t.Fatal(err)
		}
	}

	if err := r.GC(ctx, now, perJob, RetentionMaxAge); err != nil {
		t.Fatalf("GC: %v", err)
	}
	runs, err := r.ListRuns(ctx, job.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 3 {
		t.Fatalf("after GC: %d runs, want 3", len(runs))
	}
}

func TestStoreListRunsSince(t *testing.T) {
	r := newStore(t)
	ctx := t.Context()
	now := mustTime(t, "2026-05-13T10:00:00Z")

	job, err := r.AddJob(ctx, eon.JobSpec{Name: "c", Command: []string{"x"}, Cron: "@hourly"}, now)
	if err != nil {
		t.Fatal(err)
	}

	starts := []time.Time{
		now.Add(-4 * time.Hour),
		now.Add(-2 * time.Hour),
		now.Add(-30 * time.Minute),
		now.Add(-1 * time.Minute),
	}
	for _, s := range starts {
		if _, err := r.RecordRun(ctx, job.ID, s, s.Add(time.Second), 0, eon.RunOK, []byte("ok\n")); err != nil {
			t.Fatal(err)
		}
	}

	runs, err := r.ListRunsSince(ctx, job.ID, now.Add(-1*time.Hour))
	if err != nil {
		t.Fatalf("ListRunsSince: %v", err)
	}
	if len(runs) != 2 {
		t.Fatalf("got %d runs, want 2", len(runs))
	}
	// Oldest-first ordering.
	if !runs[0].StartedAt.Before(runs[1].StartedAt) {
		t.Fatalf("expected oldest-first, got %v then %v", runs[0].StartedAt, runs[1].StartedAt)
	}
}

// Stale-run heal isn't needed any more — the scheduler only records
// a row via RecordRun, after the runner has finished, so a crashed
// daemon leaves nothing behind. The invariant we DO check: a crashed
// daemon's lost work is simply absent from the run history, never
// half-written.
func TestStoreNoHalfWrittenRunsOnCrash(t *testing.T) {
	r := newStore(t)
	ctx := t.Context()
	now := mustTime(t, "2026-05-13T10:00:00Z")

	job, err := r.AddJob(ctx, eon.JobSpec{Name: "c", Command: []string{"x"}, Cron: "@hourly"}, now)
	if err != nil {
		t.Fatal(err)
	}

	// One completed run.
	if _, err := r.RecordRun(ctx, job.ID, now, now.Add(time.Second), 0, eon.RunOK, []byte("ok\n")); err != nil {
		t.Fatal(err)
	}

	// Simulate a crash by NOT recording the second run.
	// The history must contain exactly one row, never a half-written one.
	runs, err := r.ListRuns(ctx, job.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 {
		t.Fatalf("got %d runs, want exactly 1 — no half-written rows allowed", len(runs))
	}
	if runs[0].Status != eon.RunOK {
		t.Fatalf("status = %q, want ok", runs[0].Status)
	}
}

func TestStoreCounts(t *testing.T) {
	r := newStore(t)
	ctx := t.Context()
	now := mustTime(t, "2026-05-13T10:00:00Z")
	future := now.Add(time.Hour)

	_, _ = r.AddJob(ctx, eon.JobSpec{Name: "c1", Command: []string{"x"}, Cron: "@hourly"}, now)
	_, _ = r.AddJob(ctx, eon.JobSpec{Name: "c2", Command: []string{"x"}, Cron: "@daily"}, now)
	o1, _ := r.AddJob(ctx, eon.JobSpec{Name: "o1", Command: []string{"x"}, FireAt: future}, now)
	o2, _ := r.AddJob(ctx, eon.JobSpec{Name: "o2", Command: []string{"x"}, FireAt: future}, now)
	if err := r.SetJobStatus(ctx, o2.ID, eon.StatusDone, now); err != nil {
		t.Fatal(err)
	}
	_ = o1

	c, err := r.Counts(ctx)
	if err != nil {
		t.Fatalf("Counts: %v", err)
	}
	want := eon.JobCounts{Total: 4, Cron: 2, OneshotPending: 1, OneshotDone: 1}
	if c != want {
		t.Fatalf("Counts = %+v, want %+v", c, want)
	}
}

// TestRepoNextFireAtInvariant pins the store-side contract the
// scheduler relies on: AddJob writes next_fire_at, SetJobStatus(done)
// zeros it, and AdvanceNextFire is reflected in DueJobs and
// SoonestDeadline lookups. If any of these silently regress the
// scheduler would either stop firing or fire too often.
func TestStoreNextFireAtInvariant(t *testing.T) {
	r := newStore(t)
	ctx := t.Context()
	now := mustTime(t, "2026-05-13T10:00:00Z")

	// AddJob computes next_fire_at on insert.
	cron, err := r.AddJob(ctx, eon.JobSpec{
		Name: "c", Command: []string{"true"}, Cron: "@hourly",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if cron.NextFireAt.IsZero() {
		t.Fatalf("cron next_fire_at not set on insert")
	}
	if !cron.NextFireAt.After(now) {
		t.Fatalf("next_fire_at = %v, want > now (%v)", cron.NextFireAt, now)
	}

	one, err := r.AddJob(ctx, eon.JobSpec{
		Name: "o", Command: []string{"true"}, FireAt: now.Add(time.Hour),
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if !one.NextFireAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("oneshot next_fire_at = %v, want %v", one.NextFireAt, now.Add(time.Hour))
	}

	// DueJobs at now sees nothing (both deadlines are future).
	due, err := r.DueJobs(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 0 {
		t.Fatalf("DueJobs(now) returned %d, want 0", len(due))
	}

	// SoonestDeadline returns the cron job's deadline (sooner).
	soonest, err := r.SoonestDeadline(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if !soonest.Equal(cron.NextFireAt) {
		t.Fatalf("SoonestDeadline = %v, want %v", soonest, cron.NextFireAt)
	}

	// Advancing past 'now+2h' moves both deadlines out of view.
	if err := r.AdvanceNextFire(ctx, cron.ID, now.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := r.AdvanceNextFire(ctx, one.ID, now.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	due, _ = r.DueJobs(ctx, now)
	if len(due) != 0 {
		t.Fatalf("after advance, DueJobs returned %d, want 0", len(due))
	}

	// Marking the one-shot done zeros its next_fire_at.
	if err := r.SetJobStatus(ctx, one.ID, eon.StatusDone, now); err != nil {
		t.Fatal(err)
	}
	got, err := r.Job(ctx, one.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.NextFireAt.IsZero() {
		t.Fatalf("done job retained next_fire_at = %v, want zero", got.NextFireAt)
	}

	// SoonestDeadline now skips it (status filter + the cron is still
	// out at +2h, which is past now).
	soonest, _ = r.SoonestDeadline(ctx, now)
	if !soonest.Equal(now.Add(2 * time.Hour)) {
		t.Fatalf("SoonestDeadline after done = %v, want %v", soonest, now.Add(2*time.Hour))
	}
}

// TestRepoDueJobsRespectsStatus ensures disabled rows never surface
// in DueJobs even when next_fire_at is in the past. Otherwise the
// scheduler would fire a job the user just disabled.
func TestStoreDueJobsRespectsStatus(t *testing.T) {
	r := newStore(t)
	ctx := t.Context()
	now := mustTime(t, "2026-05-13T10:00:00Z")

	j, err := r.AddJob(ctx, eon.JobSpec{
		Name: "j", Command: []string{"true"}, Cron: "@hourly",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.AdvanceNextFire(ctx, j.ID, now.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	// Confirm it would be due.
	due, _ := r.DueJobs(ctx, now)
	if len(due) != 1 {
		t.Fatalf("due before disable: %d, want 1", len(due))
	}
	// Disable; it must drop out of DueJobs even though next_fire_at
	// remains in the past.
	if err := r.SetJobStatus(ctx, j.ID, eon.StatusDisabled, now); err != nil {
		t.Fatal(err)
	}
	due, _ = r.DueJobs(ctx, now)
	if len(due) != 0 {
		t.Fatalf("due after disable: %d, want 0", len(due))
	}
}
