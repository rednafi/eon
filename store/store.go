// Package store persists jobs and run history in SQLite.
package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rednafi/eon"
	"modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS jobs (
    id            TEXT    PRIMARY KEY,
    kind          TEXT    NOT NULL CHECK(kind IN ('cron','oneshot')),
    name          TEXT    NOT NULL,
    command_json  TEXT    NOT NULL,
    env_json      TEXT    NOT NULL DEFAULT '[]',
    cron_expr     TEXT    NOT NULL DEFAULT '',
    fire_at       INTEGER NOT NULL DEFAULT 0,
    status        TEXT    NOT NULL CHECK(status IN ('enabled','disabled','done')),
    last_run_at   INTEGER NOT NULL DEFAULT 0,
    last_status   TEXT    NOT NULL DEFAULT '',
    next_fire_at  INTEGER NOT NULL DEFAULT 0,
    created_at    INTEGER NOT NULL,
    updated_at    INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS jobs_status_kind ON jobs(status, kind);
CREATE INDEX IF NOT EXISTS jobs_next_fire   ON jobs(status, next_fire_at);

CREATE TABLE IF NOT EXISTS runs (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    job_id       TEXT    NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
    started_at   INTEGER NOT NULL,
    finished_at  INTEGER NOT NULL DEFAULT 0,
    exit_code    INTEGER NOT NULL DEFAULT 0,
    status       TEXT    NOT NULL,
    output       BLOB    NOT NULL DEFAULT x''
);
CREATE INDEX IF NOT EXISTS runs_job_started ON runs(job_id, started_at DESC);
`

// jobCols is the canonical job-row column order.
// Every SELECT passed to scanJob must use this order.
// Otherwise fields silently misalign.
const jobCols = `id, kind, name, command_json, env_json, cron_expr, fire_at,
	status, last_run_at, last_status, next_fire_at, created_at, updated_at`

// runCols is the canonical run-row column order for scanRun.
// It has the same alignment invariant as jobCols.
const runCols = `id, job_id, started_at, finished_at, exit_code, status`

// Retention axes applied by Store.GC:
//   - RetentionPerJob: keep up to this many most-recent runs per job.
//   - RetentionMaxAge: drop runs older than this regardless of job.
//   - RetentionMaxTotal: cap the whole runs table.
//     Oldest rows are trimmed across all jobs until the total fits.
const (
	// RetentionPerJob is the default number of most-recent runs to keep per job.
	RetentionPerJob = 100

	// RetentionMaxAge is the default maximum age for retained run history.
	RetentionMaxAge = 100 * 24 * time.Hour

	// RetentionMaxTotal is the default cap for all retained run rows.
	RetentionMaxTotal = 144_000
)

// DefaultListLimit caps Store.ListJobs output when no Limit is set.
// 100 rows is the largest a human terminal renders cleanly and
// matches the run-history retention so the numbers feel consistent.
const DefaultListLimit = 100

// ListOpts filters Store.ListJobs results.
// Zero value returns every job ordered by created_at descending.
// Limit > 0 caps the page.
// Limit <= 0 disables the store-level cap.
type ListOpts struct {
	Kind   eon.JobKind   // empty = both kinds
	Status eon.JobStatus // empty = all statuses
	Limit  int           // <=0 means no cap at the store layer
	Offset int           // rows to skip
}

// MaxOutputBytes caps the captured stdout+stderr per run. Output
// beyond this point is dropped and a truncation marker appended by
// the scheduler.
const MaxOutputBytes = 100 * 1024

// Store holds SQLite-backed jobs and run history.
// Concurrent callers against the same data dir are safe.
// SQLite WAL mode and busy_timeout serialize writers.
type Store struct {
	db      *sql.DB
	dataDir string
	dbPath  string
}

// Open returns a Store with its schema applied.
//
// Behavior:
//   - Non-empty dataDir writes to dataDir/eon.db.
//   - Empty dataDir uses an in-memory database for tests.
//   - ctx scopes schema and migration queries.
//   - ctx does not bound the returned Store's lifetime.
func Open(ctx context.Context, dataDir string) (*Store, error) {
	var dbPath, dsn string
	switch dataDir {
	case "":
		dbPath = ":memory:"
		dsn = "file::memory:?cache=shared&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
	default:
		if err := os.MkdirAll(dataDir, 0o755); err != nil {
			return nil, fmt.Errorf("mkdir data dir: %w", err)
		}
		dbPath = filepath.Join(dataDir, "eon.db")
		// busy_timeout must be first.
		// Pragmas are applied in DSN order.
		// journal_mode(WAL) on a fresh database needs an exclusive lock.
		// Concurrent first openers should wait, not fail with SQLITE_BUSY.
		dsn = "file:" + url.PathEscape(dbPath) +
			"?_pragma=busy_timeout(30000)" +
			"&_pragma=journal_mode(WAL)" +
			"&_pragma=foreign_keys(1)" +
			"&_pragma=synchronous(NORMAL)"
	}

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)

	if err := applySchema(ctx, db); err != nil {
		_ = db.Close()
		return nil, err
	}

	return &Store{db: db, dataDir: dataDir, dbPath: dbPath}, nil
}

// applySchema applies schema DDL and additive migrations.
//
// Invariants:
//   - Base DDL is idempotent.
//   - Older databases migrate on startup.
//   - There is no separate upgrade command.
//
// Concurrent first opens can race WAL setup.
// DSN busy_timeout does not cover that window reliably.
// retryOnBusy handles it.
func applySchema(ctx context.Context, db *sql.DB) error {
	if err := retryOnBusy(ctx, func() error {
		_, err := db.ExecContext(ctx, schema)
		return err
	}); err != nil {
		return fmt.Errorf("apply schema: %w", err)
	}
	for _, m := range migrations {
		has, err := columnExists(ctx, db, m.table, m.column)
		if err != nil {
			return fmt.Errorf("check column %s.%s: %w", m.table, m.column, err)
		}
		if has {
			continue
		}
		if err := retryOnBusy(ctx, func() error {
			_, err := db.ExecContext(ctx, m.alter)
			return err
		}); err != nil {
			// Another process may have applied the same migration.
			// The desired column exists, so this opener is done.
			if has2, err2 := columnExists(ctx, db, m.table, m.column); err2 == nil && has2 {
				continue
			}
			return fmt.Errorf("add column %s.%s: %w", m.table, m.column, err)
		}
	}
	return nil
}

// sqliteBusy is SQLITE_BUSY.
// Extended busy codes share its low byte.
const sqliteBusy = 5

// isBusy detects SQLITE_BUSY using modernc's typed error. Used by
// retryOnBusy to bound the schema-apply retry loop.
func isBusy(err error) bool {
	se, ok := errors.AsType[*sqlite.Error](err)
	return ok && se.Code()&0xff == sqliteBusy
}

// retryOnBusy invokes fn with exponential backoff on SQLITE_BUSY.
//
// Why this exists:
//   - DSN busy_timeout covers most contention.
//   - Fresh schema DDL can still race journal_mode(WAL).
//   - The retry budget is roughly 6 seconds.
func retryOnBusy(ctx context.Context, fn func() error) error {
	const maxAttempts = 8
	delay := 25 * time.Millisecond
	var lastErr error
	for range maxAttempts {
		err := fn()
		if err == nil {
			return nil
		}
		if !isBusy(err) {
			return err
		}
		lastErr = err
		select {
		case <-ctx.Done():
			return context.Cause(ctx)
		case <-time.After(delay):
		}
		if delay < time.Second {
			delay *= 2
		}
	}
	return lastErr
}

// migrations is the additive set of columns added after the initial
// schema was shipped. Each entry is keyed by (table, column) so the
// check is idempotent against any database state.
var migrations = []struct {
	table, column, alter string
}{
	{"jobs", "env_json", `ALTER TABLE jobs ADD COLUMN env_json TEXT NOT NULL DEFAULT '[]'`},
}

func columnExists(ctx context.Context, db *sql.DB, table, column string) (bool, error) {
	rows, err := db.QueryContext(ctx, `SELECT 1 FROM pragma_table_info(?) WHERE name = ?`, table, column)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	return rows.Next(), rows.Err()
}

// DataDir returns the directory that contains the store database.
func (s *Store) DataDir() string { return s.dataDir }

// DBPath returns the SQLite database path, or :memory: for test stores.
func (s *Store) DBPath() string { return s.dbPath }

// Close releases the underlying SQLite handle.
func (s *Store) Close() error { return s.db.Close() }

// AddJob validates and inserts a new enabled job.
func (s *Store) AddJob(ctx context.Context, spec eon.JobSpec, now time.Time) (eon.Job, error) {
	cmdJSON, err := json.Marshal(spec.Command)
	if err != nil {
		return eon.Job{}, fmt.Errorf("marshal command: %w", err)
	}
	envJSON, err := json.Marshal(spec.Env)
	if err != nil {
		return eon.Job{}, fmt.Errorf("marshal env: %w", err)
	}
	if spec.Env == nil {
		envJSON = []byte("[]")
	}
	kind := eon.KindOneshot
	fireAt := int64(0)
	if spec.Cron != "" {
		kind = eon.KindCron
	} else {
		fireAt = spec.FireAt.UnixNano()
	}
	// Compute next_fire_at before insert.
	// The scheduler can then find the new job through its indexed due scan.
	// Use a temporary Job so NextFire remains the canonical calculation.
	var nextFire int64
	if t := eon.NextFire(eon.Job{
		Kind: kind, Cron: spec.Cron, FireAt: spec.FireAt, Status: eon.StatusEnabled,
	}, now); !t.IsZero() {
		nextFire = t.UnixNano()
	}

	const q = `INSERT INTO jobs
		(id, kind, name, command_json, env_json, cron_expr, fire_at, status,
		 next_fire_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, 'enabled', ?, ?, ?)`
	for range 8 {
		id := newJobID()
		// Random ID collisions are rare.
		// Retry a few times before surfacing a real insert error.
		_, err := s.db.ExecContext(ctx, q, string(id), kind, spec.Name, cmdJSON, envJSON, spec.Cron, fireAt,
			nextFire, now.UnixNano(), now.UnixNano())
		if err == nil {
			return s.Job(ctx, id)
		}
		if isUniqueViolation(err) {
			continue
		}
		return eon.Job{}, fmt.Errorf("insert job: %w", err)
	}
	return eon.Job{}, fmt.Errorf("insert job: exhausted ID retries")
}

const idAlphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

func newJobID() eon.JobID {
	var buf [5]byte
	for i := range buf {
		buf[i] = idAlphabet[rand.IntN(len(idAlphabet))]
	}
	return eon.JobID(buf[:])
}

// sqliteConstraint is SQLITE_CONSTRAINT.
// Extended constraint codes share its low byte.
const sqliteConstraint = 19

// isUniqueViolation detects SQLite UNIQUE / PK constraint conflicts
// using modernc's typed error. Used by Store.AddJob to retry on PK
// collisions during 5-char ID minting.
func isUniqueViolation(err error) bool {
	se, ok := errors.AsType[*sqlite.Error](err)
	return ok && se.Code()&0xff == sqliteConstraint
}

// Job returns the job with id.
func (s *Store) Job(ctx context.Context, id eon.JobID) (eon.Job, error) {
	q := `SELECT ` + jobCols + ` FROM jobs WHERE id = ?`
	row := s.db.QueryRowContext(ctx, q, string(id))
	job, err := scanJob(row)
	if errors.Is(err, sql.ErrNoRows) {
		return eon.Job{}, fmt.Errorf("%w: job %q", eon.ErrNotFound, id)
	}
	return job, err
}

// JobByName returns the lowest-ID exact-name match.
//
// Names are not unique.
// The CLI uses this when the user passes a name instead of a 5-char ID.
func (s *Store) JobByName(ctx context.Context, name string) (eon.Job, error) {
	q := `SELECT ` + jobCols + ` FROM jobs WHERE name = ? ORDER BY id ASC LIMIT 1`
	row := s.db.QueryRowContext(ctx, q, name)
	job, err := scanJob(row)
	if errors.Is(err, sql.ErrNoRows) {
		return eon.Job{}, fmt.Errorf("%w: job %q", eon.ErrNotFound, name)
	}
	return job, err
}

// ListJobs returns jobs matching opts.
func (s *Store) ListJobs(ctx context.Context, opts ListOpts) ([]eon.Job, error) {
	q := `SELECT ` + jobCols + ` FROM jobs`
	var (
		clauses []string
		args    []any
	)
	if opts.Kind != "" {
		clauses = append(clauses, "kind = ?")
		args = append(args, string(opts.Kind))
	}
	if opts.Status != "" {
		clauses = append(clauses, "status = ?")
		args = append(args, string(opts.Status))
	}
	if len(clauses) > 0 {
		q += " WHERE " + strings.Join(clauses, " AND ")
	}
	// Random IDs make ID ordering meaningless.
	// Users expect `eon ls` to show recent additions first.
	q += " ORDER BY created_at DESC, id ASC"
	if opts.Limit > 0 {
		q += " LIMIT ?"
		args = append(args, opts.Limit)
		if opts.Offset > 0 {
			q += " OFFSET ?"
			args = append(args, opts.Offset)
		}
	}

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list jobs: %w", err)
	}
	defer rows.Close()

	var out []eon.Job
	for rows.Next() {
		job, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, job)
	}
	return out, rows.Err()
}

// DeleteJob removes the job with id.
func (s *Store) DeleteJob(ctx context.Context, id eon.JobID) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM jobs WHERE id = ?`, string(id))
	if err != nil {
		return fmt.Errorf("delete job: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete job rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("%w: job %q", eon.ErrNotFound, id)
	}
	return nil
}

// SetJobStatus updates status and updated_at.
//
// State-machine invariant:
//   - done clears next_fire_at.
//   - other statuses leave next_fire_at alone.
//   - re-enable must call AdvanceNextFire.
func (s *Store) SetJobStatus(ctx context.Context, id eon.JobID, status eon.JobStatus, now time.Time) error {
	q := `UPDATE jobs SET status = ?, updated_at = ? WHERE id = ?`
	if status == eon.StatusDone {
		q = `UPDATE jobs SET status = ?, updated_at = ?, next_fire_at = 0 WHERE id = ?`
	}
	res, err := s.db.ExecContext(ctx, q, string(status), now.UnixNano(), string(id))
	if err != nil {
		return fmt.Errorf("set status: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("set status rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("%w: job %q", eon.ErrNotFound, id)
	}
	return nil
}

// AdvanceNextFire sets next_fire_at.
//
// Uses:
//   - Zero time means "never fires again".
//   - Scheduler calls this before exec at fire-claim time.
//   - CLI calls this after re-enabling a job.
func (s *Store) AdvanceNextFire(ctx context.Context, id eon.JobID, next time.Time) error {
	var nano int64
	if !next.IsZero() {
		nano = next.UnixNano()
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE jobs SET next_fire_at = ? WHERE id = ?`, nano, string(id))
	if err != nil {
		return fmt.Errorf("advance next_fire_at: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("advance next_fire_at rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("%w: job %q", eon.ErrNotFound, id)
	}
	return nil
}

// DueJobs returns enabled jobs due at or before now.
// Results are ordered by deadline ascending.
// The (status, next_fire_at) index keeps this as a range scan.
func (s *Store) DueJobs(ctx context.Context, now time.Time) ([]eon.Job, error) {
	q := `SELECT ` + jobCols + `
		FROM jobs
		WHERE status = 'enabled' AND next_fire_at > 0 AND next_fire_at <= ?
		ORDER BY next_fire_at ASC`
	rows, err := s.db.QueryContext(ctx, q, now.UnixNano())
	if err != nil {
		return nil, fmt.Errorf("due jobs: %w", err)
	}
	defer rows.Close()
	var out []eon.Job
	for rows.Next() {
		job, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, job)
	}
	return out, rows.Err()
}

// SoonestDeadline returns the next enabled deadline after now.
// Zero means there is no future fire.
// The scheduler then sleeps until Wake or MaxSleep.
func (s *Store) SoonestDeadline(ctx context.Context, now time.Time) (time.Time, error) {
	const q = `SELECT next_fire_at FROM jobs
		WHERE status = 'enabled' AND next_fire_at > ?
		ORDER BY next_fire_at ASC LIMIT 1`
	var nano int64
	err := s.db.QueryRowContext(ctx, q, now.UnixNano()).Scan(&nano)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, fmt.Errorf("soonest deadline: %w", err)
	}
	return time.Unix(0, nano).UTC(), nil
}

// MarkJobRan updates the denormalized last-run fields on a job.
func (s *Store) MarkJobRan(ctx context.Context, id eon.JobID, status eon.RunStatus, at time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE jobs SET last_run_at = ?, last_status = ?, updated_at = ? WHERE id = ?`,
		at.UnixNano(), string(status), at.UnixNano(), string(id))
	if err != nil {
		return fmt.Errorf("mark ran: %w", err)
	}
	return nil
}

// RecordRun inserts a completed run in one statement.
//
// There is no durable "running" row.
// If the daemon dies mid-execution, lost work is absent from history.
// Startup has no half-written run to repair.
func (s *Store) RecordRun(ctx context.Context, jobID eon.JobID, startedAt, finishedAt time.Time, exitCode int, status eon.RunStatus, output []byte) (eon.Run, error) {
	if output == nil {
		output = []byte{}
	}
	const q = `INSERT INTO runs (job_id, started_at, finished_at, exit_code, status, output)
		VALUES (?, ?, ?, ?, ?, ?)`
	res, err := s.db.ExecContext(ctx, q,
		string(jobID), startedAt.UnixNano(), finishedAt.UnixNano(), exitCode, string(status), output)
	if err != nil {
		return eon.Run{}, fmt.Errorf("insert run: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return eon.Run{}, fmt.Errorf("last run id: %w", err)
	}
	return eon.Run{
		ID:         id,
		JobID:      jobID,
		StartedAt:  startedAt,
		FinishedAt: finishedAt,
		ExitCode:   exitCode,
		Status:     status,
	}, nil
}

// RecordOverlap records a skipped recurring fire for jobID.
func (s *Store) RecordOverlap(ctx context.Context, jobID eon.JobID, at time.Time) error {
	const q = `INSERT INTO runs (job_id, started_at, finished_at, exit_code, status)
		VALUES (?, ?, ?, 0, ?)`
	_, err := s.db.ExecContext(ctx, q, string(jobID), at.UnixNano(), at.UnixNano(), string(eon.RunSkippedOverlap))
	if err != nil {
		return fmt.Errorf("record overlap: %w", err)
	}
	return nil
}

// ListRuns returns the newest runs for jobID.
func (s *Store) ListRuns(ctx context.Context, jobID eon.JobID, limit int) ([]eon.Run, error) {
	if limit <= 0 {
		limit = 100
	}
	q := `SELECT ` + runCols + ` FROM runs WHERE job_id = ? ORDER BY started_at DESC, id DESC LIMIT ?`
	rows, err := s.db.QueryContext(ctx, q, string(jobID), limit)
	if err != nil {
		return nil, fmt.Errorf("list runs: %w", err)
	}
	defer rows.Close()

	var out []eon.Run
	for rows.Next() {
		run, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, run)
	}
	return out, rows.Err()
}

// ListRunsSince returns runs for jobID with started_at >= since,
// ordered oldest-first so the caller can replay history in
// chronological order.
func (s *Store) ListRunsSince(ctx context.Context, jobID eon.JobID, since time.Time) ([]eon.Run, error) {
	q := `SELECT ` + runCols + ` FROM runs WHERE job_id = ? AND started_at >= ? ORDER BY started_at ASC, id ASC`
	rows, err := s.db.QueryContext(ctx, q, string(jobID), since.UnixNano())
	if err != nil {
		return nil, fmt.Errorf("list runs since: %w", err)
	}
	defer rows.Close()
	var out []eon.Run
	for rows.Next() {
		run, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, run)
	}
	return out, rows.Err()
}

// ListRunsAfter returns runs inserted after afterID, oldest first.
func (s *Store) ListRunsAfter(ctx context.Context, jobID eon.JobID, afterID int64) ([]eon.Run, error) {
	q := `SELECT ` + runCols + ` FROM runs WHERE job_id = ? AND id > ? ORDER BY id ASC`
	rows, err := s.db.QueryContext(ctx, q, string(jobID), afterID)
	if err != nil {
		return nil, fmt.Errorf("list runs after: %w", err)
	}
	defer rows.Close()
	var out []eon.Run
	for rows.Next() {
		run, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, run)
	}
	return out, rows.Err()
}

// LatestRun returns the newest run for jobID.
func (s *Store) LatestRun(ctx context.Context, jobID eon.JobID) (eon.Run, error) {
	q := `SELECT ` + runCols + ` FROM runs WHERE job_id = ? ORDER BY started_at DESC, id DESC LIMIT 1`
	row := s.db.QueryRowContext(ctx, q, string(jobID))
	run, err := scanRun(row)
	if errors.Is(err, sql.ErrNoRows) {
		return eon.Run{}, fmt.Errorf("%w: no runs for job %q", eon.ErrNotFound, jobID)
	}
	return run, err
}

// OpenRunLog returns the captured output for runID.
func (s *Store) OpenRunLog(ctx context.Context, runID int64) (io.ReadCloser, error) {
	var out []byte
	err := s.db.QueryRowContext(ctx, `SELECT output FROM runs WHERE id = ?`, runID).Scan(&out)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: run %d", eon.ErrNotFound, runID)
	}
	if err != nil {
		return nil, fmt.Errorf("read output: %w", err)
	}
	return io.NopCloser(bytes.NewReader(out)), nil
}

// GC enforces retention in this order:
//   - Drop runs older than maxAge.
//   - Keep perJob most-recent runs per job.
//   - If maxTotal is positive, cap the whole runs table.
//
// Deleting run rows is enough.
// Job deletion cascades handle related cleanup.
// The scheduler calls GC at startup and periodically.
func (s *Store) GC(ctx context.Context, now time.Time, perJob int, maxAge time.Duration, maxTotal int) error {
	cutoff := now.Add(-maxAge).UnixNano()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("gc tx: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM runs WHERE started_at < ?`, cutoff); err != nil {
		return fmt.Errorf("gc by age: %w", err)
	}
	// Break timestamp ties by run ID so GC keeps the newest rows
	// deterministically even when several runs share started_at.
	const trimQ = `
		DELETE FROM runs
		WHERE id IN (
			SELECT id FROM runs r1
			WHERE (
				SELECT COUNT(*) FROM runs r2
				WHERE r2.job_id = r1.job_id
				  AND (r2.started_at > r1.started_at
				       OR (r2.started_at = r1.started_at AND r2.id >= r1.id))
			) > ?
		)`
	if _, err := tx.ExecContext(ctx, trimQ, perJob); err != nil {
		return fmt.Errorf("gc by count: %w", err)
	}
	if maxTotal > 0 {
		const capQ = `
			DELETE FROM runs
			WHERE id IN (
				SELECT id FROM runs
				ORDER BY started_at DESC, id DESC
				LIMIT -1 OFFSET ?
			)`
		if _, err := tx.ExecContext(ctx, capQ, maxTotal); err != nil {
			return fmt.Errorf("gc by total: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("gc commit: %w", err)
	}
	return nil
}

// Counts returns the per-kind / per-state aggregate used by eon status.
// One round-trip.
func (s *Store) Counts(ctx context.Context) (eon.JobCounts, error) {
	var c eon.JobCounts
	row := s.db.QueryRowContext(ctx, `
		SELECT
			COUNT(*),
			COUNT(*) FILTER (WHERE kind = 'cron'),
			COUNT(*) FILTER (WHERE kind = 'oneshot' AND status = 'enabled'),
			COUNT(*) FILTER (WHERE kind = 'oneshot' AND status = 'done')
		FROM jobs`)
	if err := row.Scan(&c.Total, &c.Cron, &c.OneshotPending, &c.OneshotDone); err != nil {
		return eon.JobCounts{}, fmt.Errorf("counts: %w", err)
	}
	return c, nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scanJob(s scanner) (eon.Job, error) {
	var (
		j            eon.Job
		kind         string
		cmdJSON      string
		envJSON      string
		status       string
		fireAtNano   int64
		lastRunNano  int64
		lastStatus   string
		nextFireNano int64
		createdNano  int64
		updatedNano  int64
	)
	err := s.Scan(&j.ID, &kind, &j.Name, &cmdJSON, &envJSON, &j.Cron, &fireAtNano,
		&status, &lastRunNano, &lastStatus, &nextFireNano, &createdNano, &updatedNano)
	if err != nil {
		return eon.Job{}, err
	}
	j.Kind = eon.JobKind(kind)
	j.Status = eon.JobStatus(status)
	j.LastStatus = eon.RunStatus(lastStatus)
	if fireAtNano > 0 {
		j.FireAt = time.Unix(0, fireAtNano).UTC()
	}
	if lastRunNano > 0 {
		j.LastRunAt = time.Unix(0, lastRunNano).UTC()
	}
	if nextFireNano > 0 {
		j.NextFireAt = time.Unix(0, nextFireNano).UTC()
	}
	j.CreatedAt = time.Unix(0, createdNano).UTC()
	j.UpdatedAt = time.Unix(0, updatedNano).UTC()
	if err := json.Unmarshal([]byte(cmdJSON), &j.Command); err != nil {
		return eon.Job{}, fmt.Errorf("decode command: %w", err)
	}
	if err := json.Unmarshal([]byte(envJSON), &j.Env); err != nil {
		return eon.Job{}, fmt.Errorf("decode env: %w", err)
	}
	return j, nil
}

func scanRun(s scanner) (eon.Run, error) {
	var (
		r            eon.Run
		startedNano  int64
		finishedNano int64
		status       string
	)
	err := s.Scan(&r.ID, &r.JobID, &startedNano, &finishedNano, &r.ExitCode, &status)
	if err != nil {
		return eon.Run{}, err
	}
	r.StartedAt = time.Unix(0, startedNano).UTC()
	if finishedNano > 0 {
		r.FinishedAt = time.Unix(0, finishedNano).UTC()
	}
	r.Status = eon.RunStatus(status)
	return r, nil
}
