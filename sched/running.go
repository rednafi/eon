package sched

import (
	"sync"

	"github.com/rednafi/eon"
)

// runningSet tracks jobs with worker goroutines in flight.
//
// A fast cron can tick before its previous run completes.
// In that case we record an overlap instead of double-firing.
//
// A mutex and map are clearer than sync.Map here.
// The set is capped by MaxConcurrent and contention is negligible.
type runningSet struct {
	mu sync.Mutex
	m  map[eon.JobID]struct{}
}

func newRunningSet() *runningSet { return &runningSet{m: make(map[eon.JobID]struct{})} }

// reserve marks id as in-flight.
//
// It returns false when the caller should record an overlap.
func (r *runningSet) reserve(id eon.JobID) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, taken := r.m[id]; taken {
		return false
	}
	r.m[id] = struct{}{}
	return true
}

func (r *runningSet) release(id eon.JobID) {
	r.mu.Lock()
	delete(r.m, id)
	r.mu.Unlock()
}
