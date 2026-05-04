package cron

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

// stubOrigin lets manager tests focus on the Manager logic — fanout, sort,
// resolve-by-prefix — without depending on a real backend.
type stubOrigin struct {
	name string
	jobs []Job
	del  func(string) error
}

func (s *stubOrigin) Name() string { return s.name }
func (s *stubOrigin) Scope() Scope { return ScopeUser }
func (s *stubOrigin) List(_ context.Context) ([]Job, error) {
	return slices.Clone(s.jobs), nil
}
func (s *stubOrigin) Delete(_ context.Context, id string) error {
	if s.del != nil {
		return s.del(id)
	}
	for i, j := range s.jobs {
		if j.ID == id {
			s.jobs = append(s.jobs[:i], s.jobs[i+1:]...)
			return nil
		}
	}
	return ErrNotFound
}

func TestManagerListAggregatesAndSorts(t *testing.T) {
	t.Parallel()
	a := &stubOrigin{name: "a", jobs: []Job{{ID: "z", Kind: KindCrontab, Name: "zeta"}}}
	b := &stubOrigin{name: "b", jobs: []Job{{ID: "y", Kind: KindCrontab, Name: "alpha"}}}
	mgr := NewManager(a, b)

	got, errs := mgr.List(t.Context())
	if len(errs) != 0 {
		t.Fatalf("unexpected errs: %v", errs)
	}
	if len(got) != 2 || got[0].Name != "alpha" || got[1].Name != "zeta" {
		t.Errorf("sort by name failed: %+v", got)
	}
}

func TestManagerListPropagatesErrors(t *testing.T) {
	t.Parallel()
	good := &stubOrigin{name: "good", jobs: []Job{{ID: "good:1"}}}
	bad := &errOrigin{name: "broken", err: errors.New("boom")}
	mgr := NewManager(bad, good)

	jobs, errs := mgr.List(t.Context())
	if len(jobs) != 1 || jobs[0].ID != "good:1" {
		t.Errorf("good origin should still appear: %+v", jobs)
	}
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "boom") {
		t.Errorf("bad origin error not surfaced: %v", errs)
	}
}

type errOrigin struct {
	name string
	err  error
}

func (e *errOrigin) Name() string                             { return e.name }
func (e *errOrigin) Scope() Scope                             { return ScopeUser }
func (e *errOrigin) List(_ context.Context) ([]Job, error)    { return nil, e.err }
func (e *errOrigin) Delete(_ context.Context, _ string) error { return ErrNotFound }

func TestManagerFindExactThenPrefix(t *testing.T) {
	t.Parallel()
	mgr := NewManager(&stubOrigin{
		name: "x",
		jobs: []Job{
			{ID: "launchd:com.foo.alpha", Name: "alpha"},
			{ID: "launchd:com.foo.beta", Name: "beta"},
		},
	})
	// Exact ID wins.
	j, err := mgr.Find(t.Context(), "launchd:com.foo.alpha")
	if err != nil || j.Name != "alpha" {
		t.Errorf("exact match failed: %v %v", j, err)
	}
	// Substring match.
	j, err = mgr.Find(t.Context(), "beta")
	if err != nil || j.Name != "beta" {
		t.Errorf("substring match failed: %v %v", j, err)
	}
	// Ambiguous → error.
	if _, err := mgr.Find(t.Context(), "foo"); err == nil {
		t.Errorf("expected ambiguous error")
	}
	// Unknown → ErrNotFound.
	if _, err := mgr.Find(t.Context(), "missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

func TestManagerDeleteRoutesToOwningOrigin(t *testing.T) {
	t.Parallel()
	a := &stubOrigin{name: "a", jobs: []Job{{ID: "owned:1"}}}
	b := &stubOrigin{name: "b", jobs: []Job{{ID: "other:1"}}}
	mgr := NewManager(a, b)
	if err := mgr.Delete(t.Context(), "owned:1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if len(a.jobs) != 0 {
		t.Errorf("owning origin not mutated: %v", a.jobs)
	}
	if len(b.jobs) != 1 {
		t.Errorf("non-owning origin mutated: %v", b.jobs)
	}
	if err := mgr.Delete(t.Context(), "ghost"); !errors.Is(err, ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

// A non-ErrNotFound error from any Source must short-circuit the fan-out.
// Otherwise a real failure (permission denied, malformed crontab) would
// silently fall through and look like "not found" to the caller.
func TestManagerDeleteShortCircuitsOnRealError(t *testing.T) {
	t.Parallel()
	boom := errors.New("permission denied")
	a := &stubOrigin{name: "a", del: func(string) error { return boom }}
	b := &stubOrigin{name: "b", jobs: []Job{{ID: "stub:b"}}}
	mgr := NewManager(a, b)
	if err := mgr.Delete(t.Context(), "stub:b"); !errors.Is(err, boom) {
		t.Errorf("expected to surface boom, got %v", err)
	}
	if len(b.jobs) != 1 {
		t.Errorf("downstream origin must not be touched after upstream real error: %v", b.jobs)
	}
}

// Manager.List must stamp Source.Scope() onto Jobs that don't already carry
// one — but must not overwrite a scope a Source has set explicitly. The
// EtcCron source uses this to mark its jobs as system-scope without depending
// on the Manager.
func TestManagerListStampsScopeButPreservesExplicit(t *testing.T) {
	t.Parallel()
	mixed := &scopedOrigin{
		scope: ScopeUser,
		jobs: []Job{
			{ID: "mixed:1", Name: "stamped"},
			{ID: "mixed:2", Name: "kept", Scope: ScopeSystem},
		},
	}
	mgr := NewManager(mixed)
	jobs, _ := mgr.List(t.Context())
	if len(jobs) != 2 {
		t.Fatalf("want 2 jobs, got %d", len(jobs))
	}
	for _, j := range jobs {
		switch j.Name {
		case "stamped":
			if j.Scope != ScopeUser {
				t.Errorf("stamped job: want %v, got %v", ScopeUser, j.Scope)
			}
		case "kept":
			if j.Scope != ScopeSystem {
				t.Errorf("explicit-scope job overwritten: %v", j.Scope)
			}
		}
	}
}

type scopedOrigin struct {
	scope Scope
	jobs  []Job
}

func (s *scopedOrigin) Name() string                             { return "scoped" }
func (s *scopedOrigin) Scope() Scope                             { return s.scope }
func (s *scopedOrigin) List(_ context.Context) ([]Job, error)    { return slices.Clone(s.jobs), nil }
func (s *scopedOrigin) Delete(_ context.Context, _ string) error { return ErrNotFound }

func TestSourceNamesPreservesRegistrationOrder(t *testing.T) {
	t.Parallel()
	mgr := NewManager(
		&stubOrigin{name: "first"},
		&stubOrigin{name: "second"},
		&stubOrigin{name: "third"},
	)
	got := mgr.SourceNames()
	want := []string{"first", "second", "third"}
	if !slices.Equal(got, want) {
		t.Errorf("source names = %v, want %v", got, want)
	}
}

func TestShortHashIsStableAndShort(t *testing.T) {
	t.Parallel()
	const input = "*/5 * * * * /usr/bin/echo hi"
	got := ShortHash(input)
	if len(got) != 8 {
		t.Errorf("ShortHash length = %d, want 8", len(got))
	}
	if again := ShortHash(input); again != got {
		t.Errorf("ShortHash not stable: %q vs %q", got, again)
	}
	if same := ShortHash(input + " "); same == got {
		t.Errorf("ShortHash collided on near-identical input: %q", same)
	}
}

func TestCommandShortNameSkipsEnvAssignments(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in, want string
	}{
		{"/usr/bin/echo hi", "echo"},
		{"PATH=/x:/y FOO=bar /opt/run", "run"},
		{"FOO=bar", "FOO=bar"},
		{"", ""},
		{"   ", "   "},
		{"singleword", "singleword"},
	}
	for _, tc := range cases {
		if got := CommandShortName(tc.in); got != tc.want {
			t.Errorf("CommandShortName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// The default Manager.List sort is (Kind, Name). Two jobs that share a Kind
// must order by Name regardless of which Source produced them, so the user
// can scan a crontab list alphabetically.
func TestManagerListSortsByKindThenName(t *testing.T) {
	t.Parallel()
	a := &stubOrigin{name: "a", jobs: []Job{
		{ID: "1", Kind: KindLaunchd, Name: "zeta"},
		{ID: "2", Kind: KindCrontab, Name: "alpha"},
	}}
	b := &stubOrigin{name: "b", jobs: []Job{
		{ID: "3", Kind: KindCrontab, Name: "beta"},
		{ID: "4", Kind: KindLaunchd, Name: "alpha"},
	}}
	mgr := NewManager(a, b)
	got, _ := mgr.List(t.Context())
	wantOrder := []string{"alpha", "beta", "alpha", "zeta"}
	wantKinds := []Kind{KindCrontab, KindCrontab, KindLaunchd, KindLaunchd}
	for i, g := range got {
		if g.Name != wantOrder[i] || g.Kind != wantKinds[i] {
			t.Errorf("position %d: got (%v,%v), want (%v,%v)", i, g.Kind, g.Name, wantKinds[i], wantOrder[i])
		}
	}
}
