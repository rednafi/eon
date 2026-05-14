package sched

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rednafi/eon"
	"github.com/rednafi/eon/store"
)

func newStore(t *testing.T) *store.Store {
	t.Helper()
	r, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = r.Close() })
	return r
}

// recRunner records invocations and blocks on a per-call gate so tests
// can hold a job "running" for as long as they need to observe overlap
// behaviour.
type recRunner struct {
	mu      sync.Mutex
	calls   atomic.Int64
	wait    chan struct{}
	written []string
}

func (r *recRunner) Run(ctx context.Context, job eon.Job, out io.Writer) (int, error) {
	r.calls.Add(1)
	r.mu.Lock()
	r.written = append(r.written, job.Name)
	r.mu.Unlock()
	_, _ = io.WriteString(out, "hi from "+job.Name+"\n")
	if r.wait != nil {
		select {
		case <-r.wait:
		case <-ctx.Done():
			return -1, ctx.Err()
		}
	}
	return 0, nil
}

func TestSchedulerFiresCronJob(t *testing.T) {
	st := newStore(t)
	rr := &recRunner{}
	s := New(st, Config{
		MaxConcurrent: 4,
		Runner:        rr,
	})

	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()

	now := time.Now()
	if _, err := st.AddJob(ctx, eon.JobSpec{
		Name: "tick", Command: []string{"true"}, Cron: "@every 1s",
	}, now); err != nil {
		t.Fatalf("AddJob: %v", err)
	}

	go func() { _ = s.Start(ctx) }()

	deadline := time.Now().Add(2500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if rr.calls.Load() >= 2 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("expected ≥2 firings within 2.5s, got %d", rr.calls.Load())
}

func TestSchedulerSkipsOverlap(t *testing.T) {
	st := newStore(t)
	gate := make(chan struct{})
	rr := &recRunner{wait: gate}
	s := New(st, Config{
		MaxConcurrent: 4,
		Runner:        rr,
	})

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	now := time.Now()
	job, err := st.AddJob(ctx, eon.JobSpec{
		Name: "slow", Command: []string{"true"}, Cron: "@every 500ms",
	}, now)
	if err != nil {
		t.Fatalf("AddJob: %v", err)
	}

	go func() { _ = s.Start(ctx) }()
	// recRunner's wait-loop already selects on ctx.Done(), so the
	// gated runner unblocks when the test's defer cancel() fires.

	// Wait until the first run has actually been started, then give
	// the scheduler enough wall time to attempt at least one more
	// firing while the first is still held by the gate.
	if !waitFor(2*time.Second, func() bool { return rr.calls.Load() >= 1 }) {
		t.Fatalf("first run never started")
	}
	time.Sleep(1500 * time.Millisecond)

	runs, err := st.ListRuns(ctx, job.ID, 10)
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	var overlaps int
	for _, r := range runs {
		if r.Status == eon.RunSkippedOverlap {
			overlaps++
		}
	}
	if overlaps == 0 {
		t.Fatalf("expected at least one overlap row, got runs=%+v", runs)
	}
}

func TestSchedulerHonoursDisabled(t *testing.T) {
	st := newStore(t)
	rr := &recRunner{}
	s := New(st, Config{
		Runner: rr,
	})

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()

	now := time.Now()
	job, err := st.AddJob(ctx, eon.JobSpec{
		Name: "off", Command: []string{"true"}, Cron: "@every 200ms",
	}, now)
	if err != nil {
		t.Fatalf("AddJob: %v", err)
	}
	if err := st.SetJobStatus(ctx, job.ID, eon.StatusDisabled, now); err != nil {
		t.Fatalf("SetJobStatus: %v", err)
	}

	go func() { _ = s.Start(ctx) }()

	time.Sleep(1 * time.Second)
	if got := rr.calls.Load(); got != 0 {
		t.Fatalf("disabled job fired %d times", got)
	}
}

func TestSchedulerWakePicksUpNewJob(t *testing.T) {
	// Scheduler starts with no jobs → sleeps on its MaxSleep. AddJob
	// updates next_fire_at; Wake() interrupts the sleep so the
	// scheduler picks the new row up without waiting it out.
	st := newStore(t)
	rr := &recRunner{}
	s := New(st, Config{Runner: rr})

	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()

	go func() { _ = s.Start(ctx) }()

	// Let the scheduler settle into its first sleep.
	time.Sleep(100 * time.Millisecond)

	now := time.Now()
	if _, err := st.AddJob(ctx, eon.JobSpec{
		Name: "late", Command: []string{"true"}, Cron: "@every 500ms",
	}, now); err != nil {
		t.Fatalf("AddJob: %v", err)
	}
	// In real CLI usage the service's notify() sends SIGHUP to the
	// daemon, which calls Wake(); the test reaches under that and
	// calls Wake directly.
	s.Wake()

	if !waitFor(2*time.Second, func() bool { return rr.calls.Load() >= 1 }) {
		t.Fatalf("scheduler did not pick up the new job; calls=%d", rr.calls.Load())
	}
}

func TestSchedulerMissedOneshotFiresOnStartup(t *testing.T) {
	// A one-shot whose FireAt is in the past (because the daemon was
	// down at the scheduled time) must fire as soon as the scheduler
	// comes up. The previous behaviour silently dropped it.
	st := newStore(t)
	rr := &recRunner{}
	s := New(st, Config{
		Runner: rr,
	})

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()

	now := time.Now()
	// Insert directly via the store so we bypass JobSpec.Validate (the
	// service rejects past fire times at the front door; only the
	// store layer can hold the simulated "the daemon was down"
	// invariant).
	if _, err := st.AddJob(ctx, eon.JobSpec{
		Name: "missed", Command: []string{"true"}, FireAt: now.Add(-1 * time.Hour),
	}, now); err != nil {
		t.Fatalf("AddJob: %v", err)
	}

	go func() { _ = s.Start(ctx) }()

	if !waitFor(1500*time.Millisecond, func() bool { return rr.calls.Load() == 1 }) {
		t.Fatalf("missed oneshot did not fire on startup")
	}
}

func TestSchedulerOneshotMarksDone(t *testing.T) {
	st := newStore(t)
	rr := &recRunner{}
	s := New(st, Config{
		Runner: rr,
	})

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()

	now := time.Now()
	job, err := st.AddJob(ctx, eon.JobSpec{
		Name: "once", Command: []string{"true"}, FireAt: now.Add(200 * time.Millisecond),
	}, now)
	if err != nil {
		t.Fatalf("AddJob: %v", err)
	}

	go func() { _ = s.Start(ctx) }()

	if !waitFor(1500*time.Millisecond, func() bool { return rr.calls.Load() == 1 }) {
		t.Fatalf("oneshot did not fire; calls=%d", rr.calls.Load())
	}
	if !waitFor(500*time.Millisecond, func() bool {
		got, err := st.Job(ctx, job.ID)
		return err == nil && got.Status == eon.StatusDone
	}) {
		t.Fatalf("oneshot not marked done")
	}
}

func TestExecRunnerCapturesNonZeroExit(t *testing.T) {
	// Sanity check the real runner without involving the scheduler.
	var sb strBuf
	code, err := ExecRunner{}.Run(t.Context(), eon.Job{Command: []string{"sh", "-c", "echo hi; exit 7"}}, &sb)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if code != 7 {
		t.Fatalf("exit code = %d, want 7", code)
	}
	if sb.String() != "hi\n" {
		t.Fatalf("stdout = %q", sb.String())
	}
}

func TestSchedulerCapsLargeOutput(t *testing.T) {
	st := newStore(t)
	s := New(st, Config{
		// Use the real exec runner so we genuinely exercise the
		// capping path with a chatty process.
	})

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	now := time.Now()
	job, err := st.AddJob(ctx, eon.JobSpec{
		Name:    "fat",
		Command: []string{"/bin/sh", "-c", "yes A | head -c 200000"},
		FireAt:  now.Add(200 * time.Millisecond),
	}, now)
	if err != nil {
		t.Fatalf("AddJob: %v", err)
	}

	go func() { _ = s.Start(ctx) }()

	var runID int64
	if !waitFor(3*time.Second, func() bool {
		run, err := st.LatestRun(ctx, job.ID)
		if err != nil {
			return false
		}
		runID = run.ID
		return !run.FinishedAt.IsZero()
	}) {
		t.Fatalf("fat job did not complete")
	}

	rc, err := st.OpenRunLog(ctx, runID)
	if err != nil {
		t.Fatalf("OpenRunLog: %v", err)
	}
	defer rc.Close()
	buf, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(buf) > store.MaxOutputBytes+128 {
		t.Fatalf("output bytes = %d, expected ≤ MaxOutputBytes(%d) + marker", len(buf), store.MaxOutputBytes)
	}
	if !bytes.Contains(buf, []byte("output truncated")) {
		t.Fatalf("expected truncation marker, got tail %q", buf[max(0, len(buf)-64):])
	}
}

func TestExecRunnerReportsStartError(t *testing.T) {
	_, err := ExecRunner{}.Run(t.Context(), eon.Job{Command: []string{"/no/such/binary"}}, io.Discard)
	if err == nil {
		t.Fatalf("expected error from missing binary")
	}
	if errors.Is(err, context.Canceled) {
		t.Fatalf("unexpected context cancel masquerading as start error")
	}
}

// TestExecRunnerCapturesStartErrorInOutput pins the fix for the
// silent-failure bug: a job whose argv[0] doesn't exist used to
// produce an empty run log, so `eon logs JOB` returned nothing and
// the user had no way to diagnose. The runner now writes the start
// error to the captured-output writer so it persists with the run.
func TestExecRunnerCapturesStartErrorInOutput(t *testing.T) {
	var sb strBuf
	_, err := ExecRunner{}.Run(t.Context(), eon.Job{Command: []string{"echo hello"}}, &sb)
	if err == nil {
		t.Fatalf("expected start error for command with space in argv[0]")
	}
	if !strings.Contains(sb.String(), "failed to start") {
		t.Fatalf("captured output missing 'failed to start': %q", sb.String())
	}
}

func waitFor(d time.Duration, pred func() bool) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if pred() {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return pred()
}

type strBuf struct {
	mu sync.Mutex
	b  []byte
}

func (s *strBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.b = append(s.b, p...)
	return len(p), nil
}
func (s *strBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return string(s.b)
}
