// Package origin defines the cron-origin abstraction and shared types.
//
// A "cron" in eon is any recurring local job: a crontab line, a launchd agent,
// a systemd timer, or anything else an Origin plugin understands. Each origin
// returns Jobs with a stable ID that downstream code (CLI, TUI) can use to
// show details, tail logs, or delete.
package origin

import (
	"context"
	"fmt"
	"sort"
	"time"
)

// Kind identifies the source backend a Job came from.
type Kind string

const (
	KindCrontab Kind = "crontab"
	KindLaunchd Kind = "launchd"
	KindSystemd Kind = "systemd"
)

// Job is a single scheduled task surfaced by a Origin.
type Job struct {
	// ID is unique across all sources (e.g. "launchd:com.foo.bar").
	ID string
	// Kind is the source backend.
	Kind Kind
	// Name is a short human label. For launchd this is the Label; for crontab
	// it is the first token of the command or a synthesized name.
	Name string
	// Schedule is the human-readable schedule expression (cron expr or
	// "every Ns" for launchd StartInterval).
	Schedule string
	// Command is the full command line that runs.
	Command string
	// NextRun is the next scheduled execution, if computable.
	NextRun *time.Time
	// LastRun is the last known execution time (best-effort: launchctl print
	// or stat of the StandardOutPath).
	LastRun *time.Time
	// Status is a short status string ("loaded", "running", "exited 1", ...).
	Status string
	// PID is the process ID if currently running, 0 otherwise.
	PID int
	// LastExitCode is the most recent exit code, 0 if unknown.
	LastExitCode int
	// StdoutPath is the file the job writes stdout to, if any.
	StdoutPath string
	// StderrPath is the file the job writes stderr to, if any.
	StderrPath string
	// Path is the on-disk file backing the job (plist path, crontab path).
	Path string
	// Raw is the verbatim source line/plist content for the "raw" tab.
	Raw string
}

// Origin enumerates and mutates jobs from a single backend.
type Origin interface {
	// Name returns a short, stable identifier ("crontab", "launchd-user").
	Name() string
	// List returns the current snapshot of jobs.
	List(ctx context.Context) ([]Job, error)
	// Delete removes a job by its ID. Implementations must be idempotent:
	// deleting an already-gone job should return ErrNotFound, not an error
	// for "not loaded" or "file already removed".
	Delete(ctx context.Context, id string) error
}

// ErrNotFound is returned by Origin.Delete when no matching job exists.
var ErrNotFound = fmt.Errorf("job not found")

// Manager fans out across multiple Origins.
type Manager struct {
	sources []Origin
}

// NewManager builds a Manager from the given sources.
func NewManager(sources ...Origin) *Manager {
	return &Manager{sources: sources}
}

// Origins returns the underlying sources (for diagnostics).
func (m *Manager) Origins() []Origin { return m.sources }

// List aggregates jobs from every source. Errors from individual sources are
// returned alongside whatever results the other sources produced — a broken
// crontab parser shouldn't hide healthy launchd entries.
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
		all = append(all, jobs...)
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].Kind != all[j].Kind {
			return all[i].Kind < all[j].Kind
		}
		return all[i].Name < all[j].Name
	})
	return all, errs
}

// Find resolves a job by ID across all sources. The match is exact on Job.ID,
// then falls back to a case-insensitive prefix match on ID or Name when
// exactly one job matches — that lets users type a short fragment like
// "stremio" instead of "launchd:com.stremio.service".
func (m *Manager) Find(ctx context.Context, idOrPrefix string) (Job, error) {
	jobs, _ := m.List(ctx)
	for _, j := range jobs {
		if j.ID == idOrPrefix {
			return j, nil
		}
	}
	var matches []Job
	lower := toLower(idOrPrefix)
	for _, j := range jobs {
		if containsFold(j.ID, lower) || containsFold(j.Name, lower) {
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

// Delete dispatches to the matching source.
func (m *Manager) Delete(ctx context.Context, id string) error {
	for _, s := range m.sources {
		if err := s.Delete(ctx, id); err == nil {
			return nil
		} else if err != ErrNotFound {
			return err
		}
	}
	return ErrNotFound
}
