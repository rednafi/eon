package main

import (
	"bytes"
	"context"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rednafi/eon"
	"github.com/rednafi/eon/store"
)

func TestStreamLogsFollowEmitsEveryRunBetweenPolls(t *testing.T) {
	st, err := store.Open(t.Context(), t.TempDir())
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	now := time.Now()
	job, err := st.AddJob(t.Context(), eon.JobSpec{
		Name: "fast", Command: []string{"true"}, Cron: "@every 1s",
	}, now)
	if err != nil {
		t.Fatalf("AddJob: %v", err)
	}
	seed, err := st.RecordRun(t.Context(), job.ID, now.Add(-time.Second), now.Add(-time.Second), 0, eon.RunOK, []byte("seed\n"))
	if err != nil {
		t.Fatalf("RecordRun seed: %v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var out lockedBuffer
	done := make(chan error, 1)
	go func() {
		done <- streamLogs(ctx, st, job.ID, logOpts{Follow: true}, &out)
	}()

	if !waitForCmdTest(2*time.Second, func() bool {
		return strings.Contains(out.String(), "run #"+strconv.FormatInt(seed.ID, 10))
	}) {
		t.Fatalf("follow did not emit initial run:\n%s", out.String())
	}

	first, err := st.RecordRun(t.Context(), job.ID, now.Add(time.Second), now.Add(time.Second), 0, eon.RunOK, []byte("first\n"))
	if err != nil {
		t.Fatalf("RecordRun first: %v", err)
	}
	second, err := st.RecordRun(t.Context(), job.ID, now.Add(2*time.Second), now.Add(2*time.Second), 0, eon.RunOK, []byte("second\n"))
	if err != nil {
		t.Fatalf("RecordRun second: %v", err)
	}

	if !waitForCmdTest(2*time.Second, func() bool {
		got := out.String()
		return strings.Contains(got, "run #"+strconv.FormatInt(first.ID, 10)) &&
			strings.Contains(got, "run #"+strconv.FormatInt(second.ID, 10)) &&
			strings.Contains(got, "first\n") &&
			strings.Contains(got, "second\n")
	}) {
		t.Fatalf("follow output skipped a run:\n%s", out.String())
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("streamLogs: %v", err)
	}
}

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

func waitForCmdTest(d time.Duration, pred func() bool) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if pred() {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return pred()
}
