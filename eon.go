// Package eon is an in-process scheduler for cron and one-shot jobs.
//
// This root package holds the public data types (Job, JobSpec, Run,
// Status), pure-function parsers (ParseCron, ParseAt, NextFire),
// sentinel errors, and the daemon-lifecycle helpers used to enforce
// single-instance and register a launchd / systemd supervisor unit.
//
// Persistence lives in [github.com/rednafi/eon/store]; the scheduler
// loop lives in [github.com/rednafi/eon/sched]. The CLI binary at
// cmd/eon composes the three.
package eon

import "time"

type (
	// JobID is a 5-character alphanumeric handle ([0-9A-Za-z]) assigned
	// at insert time. The 62^5 ≈ 916M space makes collisions vanishingly
	// rare for a personal scheduler, and short strings stay visually
	// distinct in lists (unlike sequential 1, 2, 3).
	JobID     string
	JobKind   string
	JobStatus string
	RunStatus string
)

const (
	KindCron    JobKind = "cron"
	KindOneshot JobKind = "oneshot"
)

const (
	StatusEnabled  JobStatus = "enabled"
	StatusDisabled JobStatus = "disabled"
	StatusDone     JobStatus = "done" // one-shot only, after firing
)

const (
	RunOK             RunStatus = "ok"
	RunFail           RunStatus = "fail"
	RunSkippedOverlap RunStatus = "skipped_overlap"
)

// Job is a stored, addressable schedule entry.
type Job struct {
	ID         JobID     `json:"id"`
	Kind       JobKind   `json:"kind"`
	Name       string    `json:"name"`
	Command    []string  `json:"command"`          // argv; [0] is the program
	Cron       string    `json:"cron,omitempty"`   // non-empty when Kind == KindCron
	FireAt     time.Time `json:"fire_at,omitzero"` // non-zero when Kind == KindOneshot
	Status     JobStatus `json:"status"`
	LastRunAt  time.Time `json:"last_run_at,omitzero"`
	LastStatus RunStatus `json:"last_status,omitempty"`
	// NextFireAt is the wall-clock time at which the scheduler will next
	// fire this job. Maintained by the store on insert / enable /
	// claim. Zero means "never fires again" (disabled, done, or a
	// past-due one-shot that has already been claimed). The scheduler
	// uses this column directly to drive its sleep; nothing in the
	// scheduler recomputes it.
	NextFireAt time.Time `json:"next_fire_at,omitzero"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// JobSpec is the user-supplied subset used by [Store.AddJob]. The
// store fills in ID and timestamps. Exactly one of Cron or FireAt
// must be set; this is checked by [JobSpec.Validate]. A non-empty
// Cron means the spec is [KindCron], otherwise it is [KindOneshot].
type JobSpec struct {
	Name    string
	Command []string
	Cron    string
	FireAt  time.Time
}

// Run is a recorded execution of a job. The captured stdout+stderr
// blob is kept separately in the store; fetch it via
// [github.com/rednafi/eon/store.Store.OpenRunLog].
type Run struct {
	ID         int64     `json:"id"`
	JobID      JobID     `json:"job_id"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at,omitzero"`
	ExitCode   int       `json:"exit_code"`
	Status     RunStatus `json:"status"`
}

// DaemonStatus reports the supervisor state observed at probe time.
type DaemonStatus struct {
	Running    bool      `json:"running"`
	PID        int       `json:"pid,omitempty"`
	StartedAt  time.Time `json:"started_at,omitzero"`
	Supervised bool      `json:"supervised"` // true when a launchd plist / systemd unit is installed
}

// JobCounts is the per-kind/per-state aggregate used by `eon status`.
type JobCounts struct {
	Total          int `json:"total"`
	Cron           int `json:"cron"`
	OneshotPending int `json:"oneshot_pending"`
	OneshotDone    int `json:"oneshot_done"`
}

// Status aggregates daemon + storage info for `eon status`.
type Status struct {
	Daemon  DaemonStatus `json:"daemon"`
	DataDir string       `json:"data_dir"`
	DBPath  string       `json:"db_path"`
	Jobs    JobCounts    `json:"jobs"`
}
