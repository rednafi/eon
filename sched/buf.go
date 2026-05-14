package sched

import (
	"bytes"
	"sync"
)

// cappedBuf is a thread-safe writer that retains at most cap bytes of
// the data piped into it. Excess output is dropped and a truncation
// notice is appended exactly once when Bytes() is read.
type cappedBuf struct {
	mu        sync.Mutex
	buf       bytes.Buffer
	cap       int
	truncated bool
}

func newCappedBuf(capBytes int) *cappedBuf { return &cappedBuf{cap: capBytes} }

func (c *cappedBuf) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	remaining := c.cap - c.buf.Len()
	if remaining <= 0 {
		c.truncated = true
		// Pretend we accepted everything: the runner shouldn't see a
		// short-write as an error and abort the job over log size.
		return len(p), nil
	}
	if len(p) > remaining {
		c.buf.Write(p[:remaining])
		c.truncated = true
		return len(p), nil
	}
	return c.buf.Write(p)
}

func (c *cappedBuf) Bytes() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.truncated {
		return append([]byte(nil), c.buf.Bytes()...)
	}
	out := make([]byte, 0, c.buf.Len()+len(truncMarker))
	out = append(out, c.buf.Bytes()...)
	out = append(out, truncMarker...)
	return out
}

var truncMarker = []byte("\n[... output truncated ...]\n")
