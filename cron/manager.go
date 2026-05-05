package cron

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"golang.org/x/sync/errgroup"
)

// FanoutLimit caps the goroutines any single fan-out (Manager.List or a
// Source's internal parallelism) may spawn per call. Each fan-out layer
// uses its own errgroup with this limit, so a Manager.List of N Sources
// where one Source is itself fanning out internally does not nest-and-
// deadlock on a shared budget.
const FanoutLimit = 100

// Manager fans calls out across multiple Sources. Construct one with
// NewManager and treat it as the single entry point for any program that
// wants to read or mutate cron-style jobs.
type Manager struct {
	sources []Source
}

// NewManager bundles the given Sources into a Manager. Order matters: it
// determines the order of fan-out for List/Find/Delete, and it shows up in
// SourceNames() which the TUI displays.
func NewManager(sources ...Source) *Manager { return &Manager{sources: sources} }

// Sources exposes the underlying Sources for diagnostics and TUI labels.
// Returns a defensive copy so callers can't mutate Manager state by
// reassigning into the returned slice (the Source pointers themselves
// remain shared — that's the point of fan-out).
func (m *Manager) Sources() []Source { return slices.Clone(m.sources) }

// SourceNames returns one Name per Source, in registration order.
func (m *Manager) SourceNames() []string {
	out := make([]string, len(m.sources))
	for i, s := range m.sources {
		out[i] = s.Name()
	}
	return out
}

// List aggregates jobs from every Source. Sources are queried in
// parallel — on macOS with six launchd directories, sequential List
// would stack a few hundred plist reads end-to-end. Per-Source errors
// are returned alongside the jobs that did succeed: a broken crontab
// parser shouldn't hide healthy launchd entries.
func (m *Manager) List(ctx context.Context) ([]Job, []error) {
	type result struct {
		jobs []Job
		err  error
	}
	results := make([]result, len(m.sources))
	eg, ctx := errgroup.WithContext(ctx)
	eg.SetLimit(FanoutLimit)
	for i, s := range m.sources {
		eg.Go(func() error {
			jobs, err := s.List(ctx)
			if err != nil {
				results[i] = result{err: fmt.Errorf("%s: %w", s.Name(), err)}
				return nil // per-Source error is not fatal to the fan-out
			}
			scope := s.Scope()
			for j := range jobs {
				// Don't clobber a Scope the Source already set; this
				// lets test fakes return mixed-scope job sets without
				// subclassing the Source.
				if jobs[j].Scope == "" {
					jobs[j].Scope = scope
				}
			}
			results[i] = result{jobs: jobs}
			return nil
		})
	}
	_ = eg.Wait() // never returns — errors are captured per-source above

	var (
		all  []Job
		errs []error
	)
	for _, r := range results {
		if r.err != nil {
			errs = append(errs, r.err)
			continue
		}
		all = append(all, r.jobs...)
	}
	slices.SortFunc(all, func(a, b Job) int {
		if c := cmp.Compare(a.Kind, b.Kind); c != 0 {
			return c
		}
		return cmp.Compare(a.Name, b.Name)
	})
	return all, errs
}

// Find resolves a job by ID across all Sources. Exact ID match wins;
// otherwise a case-insensitive substring match on ID, Name, or Command
// must produce exactly one hit. An empty idOrPrefix is rejected —
// strings.Contains(_, "") is true for every Job, which would surface as a
// confusing "ambiguous" error rather than the obvious "you didn't pass an
// ID".
func (m *Manager) Find(ctx context.Context, idOrPrefix string) (Job, error) {
	if strings.TrimSpace(idOrPrefix) == "" {
		return Job{}, fmt.Errorf("id or unique prefix required")
	}
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

// Add creates a job in the Source whose Name matches sourceName. If
// sourceName is empty, the first writable Mutator Source wins — so users
// can run `eon add` without knowing the backend. Returns the created Job.
func (m *Manager) Add(ctx context.Context, sourceName string, spec JobSpec) (Job, error) {
	s, mut, err := m.pickWritable(sourceName)
	if err != nil {
		return Job{}, err
	}
	j, err := mut.Add(ctx, spec)
	if err != nil {
		return Job{}, err
	}
	if j.Scope == "" {
		j.Scope = s.Scope()
	}
	return j, nil
}

// pickWritable returns the Source we should target for an Add along with
// its Mutator view, so callers don't need to re-assert the interface. With
// a specific name we either return that source (if it's a Mutator) or a
// targeted error. With no name, the first writable Source wins.
func (m *Manager) pickWritable(sourceName string) (Source, Mutator, error) {
	if sourceName != "" {
		for _, s := range m.sources {
			if s.Name() != sourceName {
				continue
			}
			mut, ok := s.(Mutator)
			if !ok {
				return nil, nil, fmt.Errorf("%s: %w", s.Name(), ErrReadOnly)
			}
			return s, mut, nil
		}
		return nil, nil, fmt.Errorf("source %q not found", sourceName)
	}
	for _, s := range m.sources {
		if mut, ok := s.(Mutator); ok {
			return s, mut, nil
		}
	}
	return nil, nil, ErrReadOnly
}

// Edit replaces the schedule/command of an existing job. The owning Source
// is whichever one claims the ID — we walk the chain like Delete.
func (m *Manager) Edit(ctx context.Context, id string, spec JobSpec) (Job, error) {
	for _, s := range m.sources {
		mut, ok := s.(Mutator)
		if !ok {
			continue
		}
		j, err := mut.Edit(ctx, id, spec)
		if err == nil {
			if j.Scope == "" {
				j.Scope = s.Scope()
			}
			return j, nil
		}
		if !errors.Is(err, ErrNotFound) {
			return Job{}, err
		}
	}
	return Job{}, ErrNotFound
}

// Delete dispatches to the matching Source. Sources that don't recognise
// the ID return ErrNotFound; we walk the chain until one accepts.
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
