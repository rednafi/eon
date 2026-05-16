// Package sched is the scheduler loop.
//
// Core model:
//   - The database is the schedule.
//   - There is no in-memory heap.
//   - There is no reload protocol.
//   - Wake interrupts sleep after CLI mutations.
package sched

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/rednafi/eon"
	"github.com/rednafi/eon/store"
	"golang.org/x/sync/semaphore"
)

// errRecordTimedOut is the cause used when run recording exceeds
// Config.RecordTimeout.
var errRecordTimedOut = errors.New("scheduler: record-run timeout")

// Config customises a [Scheduler]. Zero values are filled in by [New].
type Config struct {
	// MaxConcurrent caps simultaneously-running jobs. Excess firings
	// queue (their goroutines block on the semaphore) until a slot
	// frees up. Default: 100.
	MaxConcurrent int

	// MaxSleep bounds the longest sleep between store checks.
	// Wake interrupts this after CLI mutations.
	// Default: 1 hour.
	MaxSleep time.Duration

	// GCInterval is the cadence at which the scheduler re-runs
	// store.Store.GC to trim run history.
	// First pass runs at startup.
	// Default: 1 hour.
	GCInterval time.Duration

	// JobGracePeriod is how long a child process is given to exit
	// after receiving SIGTERM on cancellation, before os/exec
	// escalates to SIGKILL. Default: 5 seconds.
	JobGracePeriod time.Duration

	// RecordTimeout bounds the per-run DB-write phase.
	// Recording is detached from shutdown cancellation.
	// This keeps killed runs in the audit trail.
	// Default: 5 seconds.
	RecordTimeout time.Duration

	// Now provides the current time. Override for deterministic tests.
	Now func() time.Time

	// Runner executes individual jobs. Default: [ExecRunner].
	Runner Runner

	// Logger is used for warnings. nil uses slog.Default().
	Logger *slog.Logger
}

// Scheduler drives the scheduler loop.
//
// Lifecycle:
//   - Start blocks until its context is cancelled.
//   - Cancelling that context is the stop signal.
//   - Wake can be called from any goroutine.
type Scheduler struct {
	store *store.Store
	cfg   Config

	sem     *semaphore.Weighted
	wake    chan struct{}
	wg      sync.WaitGroup
	running *runningSet
}

// New constructs a Scheduler without starting it.
func New(st *store.Store, cfg Config) *Scheduler {
	if cfg.MaxConcurrent <= 0 {
		cfg.MaxConcurrent = 100
	}
	if cfg.MaxSleep <= 0 {
		cfg.MaxSleep = time.Hour
	}
	if cfg.GCInterval <= 0 {
		cfg.GCInterval = time.Hour
	}
	if cfg.JobGracePeriod <= 0 {
		cfg.JobGracePeriod = 5 * time.Second
	}
	if cfg.RecordTimeout <= 0 {
		cfg.RecordTimeout = 5 * time.Second
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Runner == nil {
		cfg.Runner = ExecRunner{GracePeriod: cfg.JobGracePeriod}
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &Scheduler{
		store:   st,
		cfg:     cfg,
		sem:     semaphore.NewWeighted(int64(cfg.MaxConcurrent)),
		wake:    make(chan struct{}, 1),
		running: newRunningSet(),
	}
}

// Wake interrupts the current sleep.
// Use it after a write that may have produced a sooner deadline.
// Non-blocking. Multiple wakeups collapse into one pending signal.
func (s *Scheduler) Wake() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// Start runs until ctx is cancelled.
// It drains in-flight runners before returning ctx.Err().
//
// GC runs at startup and then on every Config.GCInterval tick.
func (s *Scheduler) Start(ctx context.Context) error {
	defer s.wg.Wait()
	s.wg.Go(func() { s.gcLoop(ctx) })

	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		now := s.cfg.Now()
		due, err := s.store.DueJobs(ctx, now)
		s.warn("due jobs", err)
		for _, job := range due {
			s.fire(ctx, now, job)
		}

		s.sleep(ctx, now)
	}
}

// fire claims the deadline before execution.
//
// This matters because:
//   - A slow run must not be started twice.
//   - A crashed daemon must not replay this fire on restart.
//   - Recurring overlaps are recorded instead of double-fired.
func (s *Scheduler) fire(ctx context.Context, now time.Time, job eon.Job) {
	next := eon.NextFire(job, now)
	if job.Kind == eon.KindOneshot {
		next = time.Time{}
	}
	// Claim the deadline before exec so a slow run cannot be started twice.
	s.warnJob("advance next_fire_at", job.ID, s.store.AdvanceNextFire(ctx, job.ID, next))

	if !s.running.reserve(job.ID) {
		// One-shots are already claimed above.
		// This path should only describe recurring overlap.
		s.warnJob("record overlap", job.ID, s.store.RecordOverlap(ctx, job.ID, job.NextFireAt))
		return
	}
	s.wg.Go(func() {
		defer s.running.release(job.ID)
		s.runJob(ctx, job)
	})
}

// sleep blocks until a deadline, Wake, or cancellation.
// The loop observes cancellation on the next ctx.Err check.
func (s *Scheduler) sleep(ctx context.Context, now time.Time) {
	timer := time.NewTimer(s.nextSleep(ctx, now))
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-s.wake:
	case <-timer.C:
	}
}

// nextSleep returns how long to sleep before re-checking the store.
//
// Bounds:
//   - At least 1ms, to avoid busy-spins.
//   - At most MaxSleep, as a safety net for long-idle daemons.
func (s *Scheduler) nextSleep(ctx context.Context, now time.Time) time.Duration {
	soonest, err := s.store.SoonestDeadline(ctx, now)
	s.warn("soonest", err)
	d := s.cfg.MaxSleep
	if !soonest.IsZero() {
		if until := soonest.Sub(now); until < d {
			d = until
		}
	}
	if d < time.Millisecond {
		d = time.Millisecond
	}
	return d
}

// runJob executes one claimed job.
// next_fire_at was already advanced by fire.
// A mid-run crash does not replay the fire on restart.
func (s *Scheduler) runJob(ctx context.Context, job eon.Job) {
	if err := s.sem.Acquire(ctx, 1); err != nil {
		return // ctx cancelled while queued
	}
	defer s.sem.Release(1)

	startedAt := s.cfg.Now()
	buf := newCappedBuf(store.MaxOutputBytes)
	exitCode, runErr := s.cfg.Runner.Run(ctx, job, buf)

	status := eon.RunOK
	if runErr != nil || exitCode != 0 {
		status = eon.RunFail
	}
	finishedAt := s.cfg.Now()

	// Detach recording from the parent context.
	// Shutdown-killed jobs still need an audit row.
	// The timeout prevents a stuck SQLite write from blocking drain.
	writeCtx, cancel := context.WithTimeoutCause(
		context.WithoutCancel(ctx), s.cfg.RecordTimeout, errRecordTimedOut)
	defer cancel()

	_, recErr := s.store.RecordRun(writeCtx, job.ID, startedAt, finishedAt, exitCode, status, buf.Bytes())
	s.warnJob("record run", job.ID, recErr)
	s.warnJob("mark ran", job.ID, s.store.MarkJobRan(writeCtx, job.ID, status, finishedAt))
	if job.Kind == eon.KindOneshot {
		s.warnJob("mark oneshot done", job.ID, s.store.SetJobStatus(writeCtx, job.ID, eon.StatusDone, finishedAt))
	}
}

func (s *Scheduler) gcLoop(ctx context.Context) {
	gc := func() {
		s.warn("gc", s.store.GC(ctx, s.cfg.Now(), store.RetentionPerJob, store.RetentionMaxAge, store.RetentionMaxTotal))
	}
	gc()
	tick := time.Tick(s.cfg.GCInterval)
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick:
			gc()
		}
	}
}

func (s *Scheduler) warn(op string, err error) {
	if err != nil {
		s.cfg.Logger.Warn(op, "err", err)
	}
}

func (s *Scheduler) warnJob(op string, jobID eon.JobID, err error) {
	if err != nil {
		s.cfg.Logger.Warn(op, "job", jobID, "err", err)
	}
}
