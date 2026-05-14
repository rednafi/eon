package main

// service is the CLI's composition root: wires the SQLite store and
// the daemon-state probe into the operations the commands need.
// Every mutation sends SIGHUP to a running daemon (silent no-op if
// none is running) so the scheduler interrupts its sleep and re-evaluates
// the schedule immediately.

import (
	"cmp"
	"context"
	"regexp"
	"syscall"
	"time"

	"github.com/rednafi/eon"
	"github.com/rednafi/eon/daemon"
	"github.com/rednafi/eon/store"
)

type service struct {
	st *store.Store
}

func newService(st *store.Store) *service { return &service{st: st} }

// notify sends SIGHUP to a running daemon so it interrupts its sleep
// and re-evaluates the schedule. Silent no-op when no daemon is up.
func (s *service) notify() {
	_, _ = daemon.SignalDaemon(s.st.DataDir(), syscall.SIGHUP)
}

func (s *service) Add(ctx context.Context, spec eon.JobSpec) (eon.Job, error) {
	now := time.Now()
	if err := spec.Validate(now); err != nil {
		return eon.Job{}, err
	}
	job, err := s.st.AddJob(ctx, spec, now)
	if err != nil {
		return eon.Job{}, err
	}
	s.notify()
	return job, nil
}

func (s *service) Get(ctx context.Context, id eon.JobID) (eon.Job, error) {
	return s.st.Job(ctx, id)
}

// idShape matches the 5-char alphanumeric handle produced by the
// store. Anything else is treated as a name when resolving CLI input.
var idShape = regexp.MustCompile(`^[0-9A-Za-z]{5}$`)

// Resolve looks up a job by either its ID or its name. ID format wins:
// a 5-char alphanumeric arg is tried as an ID first, then as a name
// if that misses. Anything else is treated as a name only. Callers
// pass user-supplied arg verbatim; the returned Job carries the real
// ID for any follow-on writes.
func (s *service) Resolve(ctx context.Context, arg string) (eon.Job, error) {
	if idShape.MatchString(arg) {
		job, err := s.st.Job(ctx, eon.JobID(arg))
		if err == nil {
			return job, nil
		}
		// Fall through to name lookup so an unlucky name that happens
		// to look like an ID still resolves.
	}
	return s.st.JobByName(ctx, arg)
}

// List returns jobs matching opts. If opts.Limit is zero, it caps the
// result at [store.DefaultListLimit]; pass a negative limit to disable
// the cap. The bool return is true when the underlying query had more
// rows than the cap — front-ends use it to show a "more available"
// hint.
func (s *service) List(ctx context.Context, opts store.ListOpts) ([]eon.Job, bool, error) {
	limit := opts.Limit
	if limit == 0 {
		limit = store.DefaultListLimit
	}
	if limit > 0 {
		// Ask for one more row than the cap so we can tell the caller
		// whether anything got trimmed without running a count query.
		opts.Limit = limit + 1
	} else {
		opts.Limit = 0
	}
	jobs, err := s.st.ListJobs(ctx, opts)
	if err != nil {
		return nil, false, err
	}
	hasMore := limit > 0 && len(jobs) > limit
	if hasMore {
		jobs = jobs[:limit]
	}
	return jobs, hasMore, nil
}

func (s *service) Delete(ctx context.Context, id eon.JobID) error {
	if err := s.st.DeleteJob(ctx, id); err != nil {
		return err
	}
	s.notify()
	return nil
}

// Enable re-activates a job. It restores next_fire_at from the
// current schedule + the latest reference instant (last_run_at, or
// created_at for a never-run job) so the scheduler schedules it
// correctly without depending on a runtime cache.
func (s *service) Enable(ctx context.Context, id eon.JobID) error {
	now := time.Now()
	job, err := s.st.Job(ctx, id)
	if err != nil {
		return err
	}
	if err := s.st.SetJobStatus(ctx, id, eon.StatusEnabled, now); err != nil {
		return err
	}
	job.Status = eon.StatusEnabled
	ref := cmp.Or(job.LastRunAt, job.CreatedAt)
	if err := s.st.AdvanceNextFire(ctx, id, eon.NextFire(job, ref)); err != nil {
		return err
	}
	s.notify()
	return nil
}

func (s *service) Disable(ctx context.Context, id eon.JobID) error {
	if err := s.st.SetJobStatus(ctx, id, eon.StatusDisabled, time.Now()); err != nil {
		return err
	}
	s.notify()
	return nil
}

func (s *service) Status(ctx context.Context) (eon.Status, error) {
	counts, err := s.st.Counts(ctx)
	if err != nil {
		return eon.Status{}, err
	}
	return eon.Status{
		Daemon:  s.DaemonState(),
		DataDir: s.st.DataDir(),
		DBPath:  s.st.DBPath(),
		Jobs:    counts,
	}, nil
}

// DaemonState probes the flock-based single-instance lock. The lock
// is held by the daemon for its lifetime; the OS releases it on any
// kind of exit, so a stale state is impossible.
func (s *service) DaemonState() eon.DaemonStatus {
	pid, startedAt, running, _ := daemon.ProbeRunLock(s.st.DataDir())
	st := eon.DaemonStatus{Supervised: daemon.IsSupervised()}
	if running {
		st.Running = true
		st.PID = pid
		st.StartedAt = startedAt
	}
	return st
}
