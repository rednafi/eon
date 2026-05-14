// Package sched is the scheduler loop. On each wake it asks the
// store "what's due now?", fires them, then sleeps until the soonest
// next_fire_at — that deadline comes from a SQL index lookup, not
// from any in-memory cache. SIGHUP-driven Wake() breaks the sleep
// early when a CLI mutation might have produced a sooner deadline.
//
// There is no heap, no control channel, no reload protocol, no
// in-memory schedule. The database is the schedule. The scheduler is
// a pump.
package sched

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/rednafi/eon"
	"github.com/rednafi/eon/store"
	"golang.org/x/sync/semaphore"
)

// Config customises a [Scheduler]. Zero values are filled in by [New].
type Config struct {
	// MaxConcurrent caps simultaneously-running jobs. Excess firings
	// queue (their goroutines block on the semaphore) until a slot
	// frees up. Default: 100.
	MaxConcurrent int

	// MaxSleep bounds the longest interval the scheduler will sleep
	// without re-querying the store. A very long sleep is safe — the
	// schedule lives in SQL and Wake() interrupts on mutations — but
	// a defensive ceiling guards against the (impossible-on-paper)
	// case of a missed signal on a long-idle daemon. Default: 1 hour.
	MaxSleep time.Duration

	// Now provides the current time. Override for deterministic tests.
	Now func() time.Time

	// Runner executes individual jobs. Default: [ExecRunner].
	Runner Runner

	// Logger is used for warnings and one-line lifecycle events.
	// nil ⇒ slog.Default().
	Logger *slog.Logger
}

// Scheduler drives the scheduler loop. Build one with [New], drive
// it with [Scheduler.Start]. To shut it down, cancel the context you
// passed to Start — that is the only stop signal. [Scheduler.Wake]
// can be called from any goroutine to interrupt the current sleep
// early when a write may have produced a sooner deadline.
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
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Runner == nil {
		cfg.Runner = ExecRunner{}
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

// Wake interrupts the current sleep so the scheduler re-queries the
// store on the next iteration. Use it after a write that might have
// produced a sooner deadline than what the scheduler is currently
// sleeping on. Non-blocking; if a wake is already pending, the call
// collapses into it.
func (s *Scheduler) Wake() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// Start runs the scheduler loop until ctx is cancelled. Blocks;
// callers typically invoke it in its own goroutine. The deferred
// wg.Wait() drains in-flight runners before Start returns ctx.Err().
//
// Runs one GC pass at startup to enforce retention. The daemon is
// long-running, so a single startup pass plus the assumption that
// daemons get restarted on upgrades/reboots keeps growth bounded
// without a separate scheduler.
func (s *Scheduler) Start(ctx context.Context) error {
	s.warn("gc", s.store.GC(ctx, s.cfg.Now(), store.RetentionPerJob, store.RetentionMaxAge))
	defer s.wg.Wait()

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

// fire advances next_fire_at, then either records an overlap (if the
// previous run for this job is still in flight) or spawns a worker
// goroutine. next_fire_at is advanced *before* the runner starts so
// a crashed daemon does not replay this fire on restart.
func (s *Scheduler) fire(ctx context.Context, now time.Time, job eon.Job) {
	next := eon.NextFire(job, now)
	s.warnJob("advance next_fire_at", job.ID, s.store.AdvanceNextFire(ctx, job.ID, next))

	if !s.running.reserve(job.ID) {
		s.warnJob("record overlap", job.ID, s.store.RecordOverlap(ctx, job.ID, job.NextFireAt))
		return
	}
	s.wg.Go(func() {
		defer s.running.release(job.ID)
		s.runJob(ctx, job)
	})
}

// sleep blocks until the soonest scheduled fire, capped by MaxSleep,
// or until a wake or ctx cancellation interrupts. ctx cancellation
// is detected by the loop on the next iteration via ctx.Err().
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
// Clamped to [1ms, MaxSleep]: zero or negative would busy-spin, and
// the upper bound is a safety net against a hypothetical lost wake
// on a long-idle daemon.
func (s *Scheduler) nextSleep(ctx context.Context, now time.Time) time.Duration {
	soonest, err := s.store.SoonestDeadline(ctx, now)
	s.warn("soonest", err)
	d := s.cfg.MaxSleep
	if !soonest.IsZero() {
		if until := time.Until(soonest); until < d {
			d = until
		}
	}
	if d < time.Millisecond {
		d = time.Millisecond
	}
	return d
}

// runJob is the worker goroutine. It acquires the concurrency
// semaphore, executes the job, and records the run. next_fire_at was
// already advanced by [Scheduler.fire] before this goroutine started,
// so a crash mid-run does not replay the fire on restart.
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

	_, recErr := s.store.RecordRun(ctx, job.ID, startedAt, finishedAt, exitCode, status, buf.Bytes())
	s.warnJob("record run", job.ID, recErr)
	s.warnJob("mark ran", job.ID, s.store.MarkJobRan(ctx, job.ID, status, finishedAt))
	if job.Kind == eon.KindOneshot {
		s.warnJob("mark oneshot done", job.ID, s.store.SetJobStatus(ctx, job.ID, eon.StatusDone, finishedAt))
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
