package sched

import (
	"sync"

	"github.com/rednafi/eon"
)

// runningSet tracks which jobs currently have a worker goroutine in
// flight, so a fast-cycling cron whose previous run hasn't completed
// gets recorded as an overlap instead of double-firing. A plain mutex
// + map beats sync.Map here for readability: there are only a few
// keys (≤ MaxConcurrent), and contention is negligible.
type runningSet struct {
	mu sync.Mutex
	m  map[eon.JobID]struct{}
}

func newRunningSet() *runningSet { return &runningSet{m: make(map[eon.JobID]struct{})} }

// reserve marks id as in-flight. Returns false if the slot was
// already taken (i.e. the caller should record an overlap).
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
