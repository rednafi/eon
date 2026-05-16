// Package eon is an in-process scheduler for cron and one-shot jobs.
package eon

import "time"

// JobID is a 5-character alphanumeric handle assigned at insert time.
type (
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
	ID      JobID    `json:"id"`
	Kind    JobKind  `json:"kind"`
	Name    string   `json:"name"`
	Command []string `json:"command"`
	// Env is captured at `eon add` time.
	//
	// It is kept so daemon-run jobs see the user's PATH.
	// It is omitted from JSON because envs often contain secrets.
	Env        []string  `json:"-"`
	Cron       string    `json:"cron,omitempty"`   // non-empty when Kind == KindCron
	FireAt     time.Time `json:"fire_at,omitzero"` // non-zero when Kind == KindOneshot
	Status     JobStatus `json:"status"`
	LastRunAt  time.Time `json:"last_run_at,omitzero"`
	LastStatus RunStatus `json:"last_status,omitempty"`
	// NextFireAt is the next wall-clock fire time.
	//
	// The store maintains it on insert, enable, and claim.
	// Zero means the job never fires again.
	// The scheduler sleeps on this value and does not recompute it.
	NextFireAt time.Time `json:"next_fire_at,omitzero"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// JobSpec is the user-supplied subset used to create a job.
//
// The store fills in ID and timestamps.
// Exactly one of Cron or FireAt must be set.
// A non-empty Cron creates a cron job.
// Otherwise the spec creates a one-shot job.
type JobSpec struct {
	Name    string
	Command []string
	Env     []string
	Cron    string
	FireAt  time.Time
}

// Run is a recorded execution of a job.
//
// The captured stdout and stderr blob is kept separately.
// Fetch it via Store.OpenRunLog.
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
	Supervised bool      `json:"supervised"` // true when a supervisor unit is installed
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
