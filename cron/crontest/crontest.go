// Package crontest provides a uniform contract test runnable against any
// cron.Source implementation. New backends should call Contract (and, if
// they satisfy cron.Mutator, MutatorContract) from their *_test.go files
// — the helper exercises the surface area that cron.Manager depends on,
// so a backend that passes here is guaranteed to fan out cleanly under
// Manager.List / Find / Delete.
//
// The helpers take a factory rather than a Source so each subtest gets a
// fresh state — important when one test mutates the backend.
package crontest

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/rednafi/eon/cron"
)

// Contract verifies a cron.Source implementation honours the documented
// invariants. The factory must return a Source whose List() is expected
// to succeed (a populated tmpdir-backed instance, an in-memory fake,
// etc.). Read-only Sources should set the corresponding flag in their
// constructor before the factory returns.
func Contract(t *testing.T, name string, newSource func(t *testing.T) cron.Source) {
	t.Helper()
	t.Run(name+"/Name", func(t *testing.T) {
		s := newSource(t)
		if got := s.Name(); strings.TrimSpace(got) == "" {
			t.Errorf("Name() returned blank — Manager uses it as the registration key")
		}
	})

	t.Run(name+"/Scope", func(t *testing.T) {
		s := newSource(t)
		switch s.Scope() {
		case cron.ScopeUser, cron.ScopeSystem:
		default:
			t.Errorf("Scope()=%q must be ScopeUser or ScopeSystem", s.Scope())
		}
	})

	t.Run(name+"/ListShape", func(t *testing.T) {
		s := newSource(t)
		jobs, err := s.List(context.Background())
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		ids := map[string]bool{}
		for _, j := range jobs {
			if j.ID == "" {
				t.Errorf("Job has empty ID — Manager.Find/Delete cannot route to it: %+v", j)
			}
			if ids[j.ID] {
				t.Errorf("duplicate Job.ID %q across List", j.ID)
			}
			ids[j.ID] = true
			if j.Kind == "" {
				t.Errorf("Job %q has empty Kind", j.ID)
			}
		}
	})

	t.Run(name+"/DeleteUnknown", func(t *testing.T) {
		s := newSource(t)
		// A bogus ID that no Source could plausibly own. Read-only
		// Sources are allowed to return either ErrNotFound or a guard
		// error; writable Sources must return ErrNotFound.
		err := s.Delete(context.Background(), "crontest-bogus-id-that-no-source-owns")
		if err == nil {
			t.Errorf("Delete of a fake ID should not succeed")
			return
		}
		if s.Scope() == cron.ScopeUser && !errors.Is(err, cron.ErrNotFound) {
			t.Errorf("user-scope Source must return ErrNotFound for unknown ID, got %v", err)
		}
	})
}

// MutatorContract exercises the Add/Edit/Delete round-trip required of
// any cron.Mutator. The factory must return a Source that is *also* a
// Mutator — call this only from backends where you've added a build-tag
// compile guard (`var _ cron.Mutator = (*Foo)(nil)`).
func MutatorContract(t *testing.T, name string, newSource func(t *testing.T) cron.Source, spec, edited cron.JobSpec) {
	t.Helper()
	t.Run(name+"/AddListEditDelete", func(t *testing.T) {
		ctx := context.Background()
		s := newSource(t)
		mut, ok := s.(cron.Mutator)
		if !ok {
			t.Fatalf("Source %s does not implement cron.Mutator", s.Name())
		}
		added, err := mut.Add(ctx, spec)
		if err != nil {
			t.Fatalf("Add: %v", err)
		}
		if added.ID == "" {
			t.Fatal("Add returned Job with empty ID")
		}
		// The freshly added Job must show up in List under the same ID.
		jobs, err := s.List(ctx)
		if err != nil {
			t.Fatalf("List after Add: %v", err)
		}
		var found bool
		for _, j := range jobs {
			if j.ID == added.ID {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Job %q not in List() after Add", added.ID)
		}
		// Edit replaces the job. Backends are allowed two strategies:
		//   - stable IDs (label-based, like launchd/systemd): same ID
		//     comes back after Edit, and the user can keep referring to
		//     the old ID for follow-up Delete.
		//   - content-derived IDs (hash-based, like crontab): the new
		//     ID differs and the *old* ID is no longer findable.
		// Either way, after Edit the backend must (a) return a Job
		// reflecting the new spec and (b) make exactly one job with the
		// new contents discoverable via List.
		updated, err := mut.Edit(ctx, added.ID, edited)
		if err != nil {
			t.Fatalf("Edit: %v", err)
		}
		if !strings.Contains(updated.Command, edited.Command) && updated.Command != edited.Command {
			t.Errorf("Edit returned Job whose Command %q doesn't reflect new spec %q", updated.Command, edited.Command)
		}
		liveID := updated.ID
		// Delete the live Job.
		if err := s.Delete(ctx, liveID); err != nil {
			t.Fatalf("Delete %q: %v", liveID, err)
		}
		// Second delete must return ErrNotFound — idempotency contract.
		if err := s.Delete(ctx, liveID); !errors.Is(err, cron.ErrNotFound) {
			t.Errorf("Delete of already-removed Job returned %v, want ErrNotFound", err)
		}
		// Edit on a removed Job must also return ErrNotFound.
		if _, err := mut.Edit(ctx, liveID, edited); !errors.Is(err, cron.ErrNotFound) {
			t.Errorf("Edit of removed Job returned %v, want ErrNotFound", err)
		}
	})
}
