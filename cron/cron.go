// Package cron exposes the eon domain types and the per-platform Source
// implementations. A Source produces Jobs from one backend (crontab, launchd,
// systemd, …); the Manager fans calls out across them. Everything that
// classifies, displays, or mutates a cron lives here so the CLI and TUI can
// stay narrow.
package cron

import (
	"cmp"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
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
// The Manager attaches the owning Source's scope to every Job it returns,
// so callers can filter on Job.Scope without knowing which Source produced
// it.
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
type Source interface {
	// Name returns a short, stable identifier (e.g. "crontab", "launchd-user").
	Name() string
	// Scope reports whether this Source is user-writable or system-readonly.
	// The Manager stamps every Job with its owning Source's scope.
	Scope() Scope
	// List returns the current snapshot of jobs.
	List(ctx context.Context) ([]Job, error)
	// Delete removes a job by ID. Idempotent: deleting an already-gone job
	// returns ErrNotFound. Read-only Sources may return a sentinel error.
	Delete(ctx context.Context, id string) error
}

// ErrNotFound is returned by Source.Delete when no matching job exists.
var ErrNotFound = errors.New("job not found")

// Manager fans calls out across multiple Sources.
type Manager struct {
	sources []Source
}

func NewManager(sources ...Source) *Manager { return &Manager{sources: sources} }

// Sources exposes the underlying Sources for diagnostics and TUI labels.
func (m *Manager) Sources() []Source { return m.sources }

// SourceNames returns one Name per Source, in registration order.
func (m *Manager) SourceNames() []string {
	out := make([]string, len(m.sources))
	for i, s := range m.sources {
		out[i] = s.Name()
	}
	return out
}

// List aggregates jobs from every Source. Per-Source errors are returned
// alongside the jobs that did succeed — a broken crontab parser shouldn't
// hide healthy launchd entries.
func (m *Manager) List(ctx context.Context) ([]Job, []error) {
	var (
		all  []Job
		errs []error
	)
	for _, s := range m.sources {
		jobs, err := s.List(ctx)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", s.Name(), err))
			continue
		}
		scope := s.Scope()
		for i := range jobs {
			// Don't clobber a Scope the Source already set; this lets test
			// fakes return mixed-scope job sets without subclassing the Source.
			if jobs[i].Scope == "" {
				jobs[i].Scope = scope
			}
		}
		all = append(all, jobs...)
	}
	slices.SortFunc(all, func(a, b Job) int {
		if c := cmp.Compare(a.Kind, b.Kind); c != 0 {
			return c
		}
		return cmp.Compare(a.Name, b.Name)
	})
	return all, errs
}

// Find resolves a job by ID across all Sources. Exact ID match wins; otherwise
// a case-insensitive substring match on ID, Name, or Command must produce
// exactly one hit.
func (m *Manager) Find(ctx context.Context, idOrPrefix string) (Job, error) {
	jobs, _ := m.List(ctx)
	if i := slices.IndexFunc(jobs, func(j Job) bool { return j.ID == idOrPrefix }); i >= 0 {
		return jobs[i], nil
	}
	q := strings.ToLower(idOrPrefix)
	var matches []Job
	for _, j := range jobs {
		if strings.Contains(strings.ToLower(j.ID), q) ||
			strings.Contains(strings.ToLower(j.Name), q) ||
			strings.Contains(strings.ToLower(j.Command), q) {
			matches = append(matches, j)
		}
	}
	switch len(matches) {
	case 0:
		return Job{}, ErrNotFound
	case 1:
		return matches[0], nil
	default:
		return Job{}, fmt.Errorf("ambiguous: %d jobs match %q", len(matches), idOrPrefix)
	}
}

// Delete dispatches to the matching Source. Sources that don't recognise the
// ID return ErrNotFound; we walk the chain until one accepts.
func (m *Manager) Delete(ctx context.Context, id string) error {
	for _, s := range m.sources {
		err := s.Delete(ctx, id)
		if err == nil {
			return nil
		}
		if !errors.Is(err, ErrNotFound) {
			return err
		}
	}
	return ErrNotFound
}

// ShortHash returns a stable 8-hex-char fingerprint of s. Sources use it for
// Job IDs that need to survive reordering of unrelated lines (crontab
// rewrites, cron.d drop-ins) — exported so every backend computes IDs the
// same way and the CLI/TUI can rely on shape.
func ShortHash(s string) string {
	sum := sha1.Sum([]byte(s))
	return hex.EncodeToString(sum[:4])
}

// CommandShortName returns a readable label for a shell command: the
// basename of the first non-assignment token. Sources use it to populate
// Job.Name when no native label exists (crontab lines, cron.d entries).
func CommandShortName(cmd string) string {
	for tok := range strings.FieldsSeq(cmd) {
		if strings.Contains(tok, "=") {
			continue
		}
		if i := strings.LastIndex(tok, "/"); i >= 0 {
			return tok[i+1:]
		}
		return tok
	}
	return cmd
}
