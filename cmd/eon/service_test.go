package main

import (
	"testing"
	"time"

	"github.com/rednafi/eon"
	"github.com/rednafi/eon/store"
)

func TestServiceEnableCronSchedulesFromNow(t *testing.T) {
	st, err := store.Open(t.Context(), t.TempDir())
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	createdAt := time.Now().Add(-3 * time.Hour).Truncate(time.Second)
	job, err := st.AddJob(t.Context(), eon.JobSpec{
		Name: "stale", Command: []string{"true"}, Cron: "@hourly",
	}, createdAt)
	if err != nil {
		t.Fatalf("AddJob: %v", err)
	}
	if err := st.SetJobStatus(t.Context(), job.ID, eon.StatusDisabled, createdAt.Add(time.Minute)); err != nil {
		t.Fatalf("SetJobStatus: %v", err)
	}

	beforeEnable := time.Now()
	if err := newService(st).Enable(t.Context(), job.ID); err != nil {
		t.Fatalf("Enable: %v", err)
	}
	got, err := st.Job(t.Context(), job.ID)
	if err != nil {
		t.Fatalf("Job: %v", err)
	}
	if !got.NextFireAt.After(beforeEnable) {
		t.Errorf("next_fire_at = %s, want a future fire after enable time %s",
			got.NextFireAt.Format(time.RFC3339Nano), beforeEnable.Format(time.RFC3339Nano))
	}
	if got.NextFireAt.After(beforeEnable.Add(time.Hour + 2*time.Second)) {
		t.Errorf("next_fire_at = %s, want next hourly fire near now",
			got.NextFireAt.Format(time.RFC3339Nano))
	}
}
