package cron

import (
	"context"
	"errors"
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
func (s *stubOrigin) List(_ context.Context) ([]Job, error) {
	return append([]Job(nil), s.jobs...), nil
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
	a := &stubOrigin{name: "a", jobs: []Job{{ID: "z", Kind: KindCrontab, Name: "zeta"}}}
	b := &stubOrigin{name: "b", jobs: []Job{{ID: "y", Kind: KindCrontab, Name: "alpha"}}}
	mgr := NewManager(a, b)

	got, errs := mgr.List(context.Background())
	if len(errs) != 0 {
		t.Fatalf("unexpected errs: %v", errs)
	}
	if len(got) != 2 || got[0].Name != "alpha" || got[1].Name != "zeta" {
		t.Errorf("sort by name failed: %+v", got)
	}
}

func TestManagerListPropagatesErrors(t *testing.T) {
	good := &stubOrigin{name: "good", jobs: []Job{{ID: "good:1"}}}
	bad := &errOrigin{name: "broken", err: errors.New("boom")}
	mgr := NewManager(bad, good)

	jobs, errs := mgr.List(context.Background())
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

func (e *errOrigin) Name() string                                { return e.name }
func (e *errOrigin) List(_ context.Context) ([]Job, error)       { return nil, e.err }
func (e *errOrigin) Delete(_ context.Context, _ string) error    { return ErrNotFound }

func TestManagerFindExactThenPrefix(t *testing.T) {
	mgr := NewManager(&stubOrigin{
		name: "x",
		jobs: []Job{
			{ID: "launchd:com.foo.alpha", Name: "alpha"},
			{ID: "launchd:com.foo.beta", Name: "beta"},
		},
	})
	// Exact ID wins.
	j, err := mgr.Find(context.Background(), "launchd:com.foo.alpha")
	if err != nil || j.Name != "alpha" {
		t.Errorf("exact match failed: %v %v", j, err)
	}
	// Substring match.
	j, err = mgr.Find(context.Background(), "beta")
	if err != nil || j.Name != "beta" {
		t.Errorf("substring match failed: %v %v", j, err)
	}
	// Ambiguous → error.
	if _, err := mgr.Find(context.Background(), "foo"); err == nil {
		t.Errorf("expected ambiguous error")
	}
	// Unknown → ErrNotFound.
	if _, err := mgr.Find(context.Background(), "missing"); err != ErrNotFound {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

func TestManagerDeleteRoutesToOwningOrigin(t *testing.T) {
	a := &stubOrigin{name: "a", jobs: []Job{{ID: "owned:1"}}}
	b := &stubOrigin{name: "b", jobs: []Job{{ID: "other:1"}}}
	mgr := NewManager(a, b)
	if err := mgr.Delete(context.Background(), "owned:1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if len(a.jobs) != 0 {
		t.Errorf("owning origin not mutated: %v", a.jobs)
	}
	if len(b.jobs) != 1 {
		t.Errorf("non-owning origin mutated: %v", b.jobs)
	}
	if err := mgr.Delete(context.Background(), "ghost"); err != ErrNotFound {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}
