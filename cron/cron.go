// Package cron is a small, embeddable library for monitoring and mutating
// the cron-style jobs on a host. It exposes one interface — Source — that
// every backend (crontab, launchd, systemd, /etc/crontab, …) implements.
// Manager fans List / Find / Add / Edit / Delete calls across a set of
// Sources so callers can treat heterogeneous schedulers as one.
//
// The package has no dependency on cli/ or tui/: everything in eon's CLI
// and TUI is built on top of cron.Manager + the public types here, and the
// same API is sufficient for any other consumer (a daemon, a webhook, a
// batch tool).
//
// Backend authors will also want cron/spec.go (validation + portable
// schedule DSL) and cron/helpers.go (LineScanner, ShortHash, …).
package cron

import (
	"context"
	"errors"
	"time"
)

// Kind identifies the backend a Job came from. Stored on Job so renderers
// can tell launchd from systemd without re-parsing the ID.
type Kind string

const (
	KindCrontab Kind = "crontab"
	KindLaunchd Kind = "launchd"
	KindSystemd Kind = "systemd"
)

// Scope distinguishes user-writable jobs from read-only OS-installed ones.
// Manager attaches the owning Source's scope to every Job it returns, so
// callers can filter on Job.Scope without knowing which Source produced it.
type Scope string

const (
	ScopeUser   Scope = "user"
	ScopeSystem Scope = "system"
)

// Job is a single scheduled task surfaced by a Source.
type Job struct {
	// ID is unique across all sources (e.g. "launchd-user:com.foo.bar").
	ID string
	// Kind is the backend type — crontab, launchd, systemd.
	Kind Kind
	// Scope is "user" for jobs the running user can mutate; "system" for
	// read-only OS-installed crons (/Library/Launch*, /etc/crontab, etc.).
	Scope Scope
	// Name is a short human label. For launchd this is the Label; for
	// crontab it is the basename of the program token.
	Name string
	// Schedule is the human-readable schedule (cron expression, "every 5m",
	// "at load", …).
	Schedule string
	// Command is the full command line.
	Command string
	// NextRun, LastRun are best-effort — left nil when the backend can't
	// answer.
	NextRun, LastRun *time.Time
	// Status is a short label ("loaded", "running", "exited 1", …).
	Status       string
	PID          int
	LastExitCode int
	// Stdout/StderrPath, Path are the files the renderer needs to display
	// raw definitions and tail logs.
	StdoutPath, StderrPath, Path string
	// Raw is the verbatim source line / plist content for the "raw" tab.
	Raw string
}

// Source enumerates and (when writable) mutates jobs from one backend.
// Implement Source to plug a new scheduler in: Manager will then fan out
// List / Find / Delete to it like any built-in backend. To opt into
// Manager.Add / Manager.Edit, additionally implement Mutator.
type Source interface {
	// Name returns a short, stable identifier (e.g. "crontab", "launchd-user").
	Name() string
	// Scope reports whether this Source is user-writable or system-readonly.
	// Manager stamps every Job with its owning Source's scope.
	Scope() Scope
	// List returns the current snapshot of jobs.
	List(ctx context.Context) ([]Job, error)
	// Delete removes a job by ID. Idempotent: deleting an already-gone job
	// returns ErrNotFound. Read-only Sources return a non-nil error.
	Delete(ctx context.Context, id string) error
}

// JobSpec carries the minimum a writable Source needs to create or replace
// a job. It is intentionally backend-agnostic: sources translate Schedule
// + Command into their own native representation (a crontab line, a
// launchd plist, a systemd unit pair).
type JobSpec struct {
	// Schedule is the cron expression or descriptor (e.g. "*/5 * * * *",
	// "@daily"). Sources reject inputs they can't parse.
	Schedule string
	// Command is the full shell command to run.
	Command string
}

// Mutator is implemented by Sources that can create or edit jobs. Sources
// that don't satisfy Mutator are necessarily read-only for these
// operations — Manager.Add / Manager.Edit return ErrReadOnly when no
// Source claims the request.
//
// Add returns the freshly created Job (so callers can show its ID).
// Edit returns the updated Job; the returned ID is the one callers
// should use for subsequent Delete or follow-up Edit calls. Backends
// with stable, label-based IDs (launchd, systemd) preserve the original
// ID; backends with content-derived IDs (the user crontab, where the ID
// is a hash of the line) may return a different ID after Edit. The
// original ID becomes invalid in the latter case — Delete on the old ID
// returns ErrNotFound.
type Mutator interface {
	Add(ctx context.Context, spec JobSpec) (Job, error)
	Edit(ctx context.Context, id string, spec JobSpec) (Job, error)
}

// ErrNotFound is returned by Source.Delete and Mutator.Edit when no
// matching job exists.
var ErrNotFound = errors.New("job not found")

// ErrReadOnly indicates an attempt to mutate a Source that doesn't
// implement Mutator (or for which Mutator returns a guard error). Callers
// should fall through to a writable Source rather than treating this as
// fatal — same shape as ErrNotFound.
var ErrReadOnly = errors.New("source is read-only")
