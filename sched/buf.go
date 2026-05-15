package sched

import (
	"slices"
	"sync"
)

// cappedBuf is a thread-safe writer that retains at most cap bytes of
// the data piped into it. Excess output is dropped and a truncation
// marker is appended exactly once when Bytes() is read. exec.Cmd
// writes stdout and stderr concurrently to the same writer, so this
// type owns the synchronisation.
type cappedBuf struct {
	mu        sync.Mutex
	data      []byte // len <= cap, never reallocated
	cap       int
	truncated bool
}

func newCappedBuf(capBytes int) *cappedBuf {
	return &cappedBuf{data: make([]byte, 0, capBytes), cap: capBytes}
}

func (c *cappedBuf) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := len(p)
	if room := c.cap - len(c.data); room < n {
		c.truncated = true
		if room <= 0 {
			// Pretend we accepted everything: the runner shouldn't
			// see a short-write as an error and abort the job over
			// log size.
			return n, nil
		}
		p = p[:room]
	}
	c.data = append(c.data, p...)
	return n, nil
}

func (c *cappedBuf) Bytes() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.truncated {
		return slices.Clone(c.data)
	}
	out := make([]byte, 0, len(c.data)+len(truncMarker))
	out = append(out, c.data...)
	out = append(out, truncMarker...)
	return out
}

var truncMarker = []byte("\n[... output truncated ...]\n")
