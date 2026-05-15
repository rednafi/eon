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

// jobCols is the canonical job-row column list ordering. Every
// SELECT that goes through [scanJob] must use this list in this
// order, or the scan will silently misalign fields.
const jobCols = `id, kind, name, command_json, env_json, cron_expr, fire_at,
	status, last_run_at, last_status, next_fire_at, created_at, updated_at`

// runCols is the canonical run-row column list ordering for
// [scanRun]. Same alignment invariant as [jobCols].
const runCols = `id, job_id, started_at, finished_at, exit_code, status`

// Retention axes applied by [Store.GC]:
//   - RetentionPerJob: keep up to this many most-recent runs per job.
//   - RetentionMaxAge: drop runs older than this regardless of job.
//   - RetentionMaxTotal: hard ceiling on the runs table; oldest rows are
//     trimmed across all jobs until the total fits.
const (
	RetentionPerJob   = 100
	RetentionMaxAge   = 100 * 24 * time.Hour
	RetentionMaxTotal = 144_000
)

// DefaultListLimit caps [Store.ListJobs] output when no Limit is set.
// 100 rows is the largest a human terminal renders cleanly and
// matches the run-history retention so the numbers feel consistent.
const DefaultListLimit = 100

// ListOpts filters [Store.ListJobs] results. Zero value returns
// every job ordered by created_at descending. Set Limit > 0 to cap
// the page; Limit < 0 disables the cap.
type ListOpts struct {
	Kind   eon.JobKind   // empty = both kinds
	Status eon.JobStatus // empty = all statuses
	Limit  int           // 0 = no cap at the store layer; negative = no cap
	Offset int           // rows to skip
}

// MaxOutputBytes caps the captured stdout+stderr per run. Output
// beyond this point is dropped and a truncation marker appended by
// the scheduler.
const MaxOutputBytes = 100 * 1024

// Store holds the SQLite-backed jobs + run history. Concurrent
// callers against the same data dir are safe: SQLite's WAL mode +
// busy_timeout(30s) serialises writers transparently.
type Store struct {
	db      *sql.DB
	dataDir string
	dbPath  string
}

// Open returns a [Store] writing to dataDir/eon.db with the schema
// applied. Pass an empty dataDir to use an in-memory database; this
// is intended for tests. ctx scopes the schema-apply and migration
// queries; it does not bound the lifetime of the returned Store.
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
		// busy_timeout MUST be first: pragmas are applied in DSN order,
		// and journal_mode(WAL) on a fresh database needs an exclusive
		// lock. Without busy_timeout already armed, concurrent openers
		// (multiple CLI invocations racing against a brand-new file)
		// get SQLITE_BUSY immediately instead of waiting their turn.
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

// applySchema applies the schema DDL and runs additive migrations
// against any pre-existing database. CREATE TABLE / CREATE INDEX are
// IF NOT EXISTS so the base DDL is idempotent. Column additions are
// done as ALTER TABLE … ADD COLUMN with IF NOT EXISTS via a
// pragma_table_info check, so older databases pick up new columns on
// startup without forcing the user to seppuku.
//
// Writes here can race with concurrent openers against the same data
// dir (e.g. many CLI invocations launched in parallel). DSN-level
// busy_timeout doesn't cover the very first journal_mode/WAL setup
// reliably, so the DDL steps are wrapped in [retryOnBusy].
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
			// Another process may have raced us to the same migration.
			// Treat that as success: the column we wanted is now present.
			if has2, err2 := columnExists(ctx, db, m.table, m.column); err2 == nil && has2 {
				continue
			}
			return fmt.Errorf("add column %s.%s: %w", m.table, m.column, err)
		}
	}
	return nil
}

// sqliteBusy is SQLITE_BUSY — the primary result code for write
// contention. Extended codes (SQLITE_BUSY_RECOVERY, _SNAPSHOT,
// _TIMEOUT) share its low byte. Stable across SQLite versions; part
// of SQLite's public C API. See https://www.sqlite.org/rescode.html.
const sqliteBusy = 5

// isBusy detects SQLITE_BUSY using modernc's typed error. Used by
// [retryOnBusy] to bound the schema-apply retry loop.
func isBusy(err error) bool {
	se, ok := errors.AsType[*sqlite.Error](err)
	return ok && se.Code()&0xff == sqliteBusy
}

// retryOnBusy invokes fn with exponential backoff while it returns
// SQLITE_BUSY. The DSN-level busy_timeout covers most cases, but
// schema DDL against a freshly-created database can race the
// journal_mode(WAL) transition; this is the safety net for that
// window. Caps at ~6s total wait, which is well within the implicit
// time budget of a CLI command.
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

func (s *Store) DataDir() string { return s.dataDir }
func (s *Store) DBPath() string  { return s.dbPath }
func (s *Store) Close() error    { return s.db.Close() }

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
	// Compute next_fire_at up-front so the scheduler's index lookup
	// sees the new row's deadline without any post-insert fixup.
	// Build a temporary Job so we can reuse the canonical NextFire.
	var nextFire int64
	if t := eon.NextFire(eon.Job{
		Kind: kind, Cron: spec.Cron, FireAt: spec.FireAt, Status: eon.StatusEnabled,
	}, now); !t.IsZero() {
		nextFire = t.UnixNano()
	}

	// Generate a 5-char alphanumeric ID; retry on the (vanishingly
	// rare) PK collision against an existing row. After 8 misses the
	// table is effectively full and we surface a real error.
	const q = `INSERT INTO jobs
		(id, kind, name, command_json, env_json, cron_expr, fire_at, status,
		 next_fire_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, 'enabled', ?, ?, ?)`
	for range 8 {
		id := newJobID()
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

// sqliteConstraint is SQLITE_CONSTRAINT — the primary result code for
// all integrity-constraint violations. Extended codes (e.g.
// SQLITE_CONSTRAINT_PRIMARYKEY=1555, SQLITE_CONSTRAINT_UNIQUE=2067)
// share its low byte. Stable across SQLite versions; part of SQLite's
// public C API. See https://www.sqlite.org/rescode.html.
const sqliteConstraint = 19

// isUniqueViolation detects SQLite UNIQUE / PK constraint conflicts
// using modernc's typed error. Used by [Store.AddJob] to retry on PK
// collisions during 5-char ID minting.
func isUniqueViolation(err error) bool {
	se, ok := errors.AsType[*sqlite.Error](err)
	return ok && se.Code()&0xff == sqliteConstraint
}

func (s *Store) Job(ctx context.Context, id eon.JobID) (eon.Job, error) {
	q := `SELECT ` + jobCols + ` FROM jobs WHERE id = ?`
	row := s.db.QueryRowContext(ctx, q, string(id))
	job, err := scanJob(row)
	if errors.Is(err, sql.ErrNoRows) {
		return eon.Job{}, fmt.Errorf("%w: job %q", eon.ErrNotFound, id)
	}
	return job, err
}

// JobByName returns the job whose name exactly matches. Names are not
// unique in the schema, so on collision the lowest-id match wins; the
// CLI uses this as a fallback when the user passes a name instead of
// a 5-char ID.
func (s *Store) JobByName(ctx context.Context, name string) (eon.Job, error) {
	q := `SELECT ` + jobCols + ` FROM jobs WHERE name = ? ORDER BY id ASC LIMIT 1`
	row := s.db.QueryRowContext(ctx, q, name)
	job, err := scanJob(row)
	if errors.Is(err, sql.ErrNoRows) {
		return eon.Job{}, fmt.Errorf("%w: job %q", eon.ErrNotFound, name)
	}
	return job, err
}

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
	// Random IDs make id-order meaningless; ordering by created_at DESC
	// surfaces the user's most-recent additions first, which is what
	// they almost always want to see in `eon ls`.
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

func (s *Store) DeleteJob(ctx context.Context, id eon.JobID) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM jobs WHERE id = ?`, string(id))
	if err != nil {
		return fmt.Errorf("delete job: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("%w: job %q", eon.ErrNotFound, id)
	}
	return nil
}

// SetJobStatus updates the status and updated_at. As a state-machine
// invariant, transitioning to 'done' zeros next_fire_at so the scheduler
// stops considering the row entirely; other transitions leave the
// column alone (re-enabling a job is paired with [AdvanceNextFire]).
func (s *Store) SetJobStatus(ctx context.Context, id eon.JobID, status eon.JobStatus, now time.Time) error {
	q := `UPDATE jobs SET status = ?, updated_at = ? WHERE id = ?`
	if status == eon.StatusDone {
		q = `UPDATE jobs SET status = ?, updated_at = ?, next_fire_at = 0 WHERE id = ?`
	}
	res, err := s.db.ExecContext(ctx, q, string(status), now.UnixNano(), string(id))
	if err != nil {
		return fmt.Errorf("set status: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("%w: job %q", eon.ErrNotFound, id)
	}
	return nil
}

// AdvanceNextFire sets the row's next_fire_at to the given instant.
// Pass the zero Time to mean "never fires again" (stored as 0 nanos).
// Used by the scheduler at fire-claim time to step the schedule forward
// before exec'ing, and by the CLI after re-enabling.
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
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("%w: job %q", eon.ErrNotFound, id)
	}
	return nil
}

// DueJobs returns enabled jobs whose scheduled fire is at or before
// now, ordered by deadline ascending. Backed by the
// (status, next_fire_at) index — an O(log N) range scan regardless
// of total job count.
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

// SoonestDeadline returns the earliest next_fire_at strictly after
// now among enabled jobs. Returns the zero time when no future fire
// is scheduled (the scheduler then sleeps until interrupted by SIGHUP).
// Index lookup, microsecond-scale even at large job counts.
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

func (s *Store) MarkJobRan(ctx context.Context, id eon.JobID, status eon.RunStatus, at time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE jobs SET last_run_at = ?, last_status = ?, updated_at = ? WHERE id = ?`,
		at.UnixNano(), string(status), at.UnixNano(), string(id))
	if err != nil {
		return fmt.Errorf("mark ran: %w", err)
	}
	return nil
}

// RecordRun inserts a completed run in one statement. There is no
// "running" intermediate state — if the daemon dies mid-execution,
// no row exists for the lost work, so there is nothing to clean up
// on the next startup.
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

func (s *Store) RecordOverlap(ctx context.Context, jobID eon.JobID, at time.Time) error {
	const q = `INSERT INTO runs (job_id, started_at, finished_at, exit_code, status)
		VALUES (?, ?, ?, 0, ?)`
	_, err := s.db.ExecContext(ctx, q, string(jobID), at.UnixNano(), at.UnixNano(), string(eon.RunSkippedOverlap))
	if err != nil {
		return fmt.Errorf("record overlap: %w", err)
	}
	return nil
}

func (s *Store) ListRuns(ctx context.Context, jobID eon.JobID, limit int) ([]eon.Run, error) {
	if limit <= 0 {
		limit = 100
	}
	q := `SELECT ` + runCols + ` FROM runs WHERE job_id = ? ORDER BY started_at DESC LIMIT ?`
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
	q := `SELECT ` + runCols + ` FROM runs WHERE job_id = ? AND started_at >= ? ORDER BY started_at ASC`
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

func (s *Store) LatestRun(ctx context.Context, jobID eon.JobID) (eon.Run, error) {
	q := `SELECT ` + runCols + ` FROM runs WHERE job_id = ? ORDER BY started_at DESC LIMIT 1`
	row := s.db.QueryRowContext(ctx, q, string(jobID))
	run, err := scanRun(row)
	if errors.Is(err, sql.ErrNoRows) {
		return eon.Run{}, fmt.Errorf("%w: no runs for job %q", eon.ErrNotFound, jobID)
	}
	return run, err
}

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

// GC enforces retention along three axes, in order: drop runs older
// than maxAge; trim each job's run history to perJob most-recent
// rows; cap the total runs table at maxTotal rows by dropping the
// oldest across all jobs. A non-positive maxTotal disables the
// global cap. With the schema's ON DELETE CASCADE there is no
// out-of-band cleanup work; deleting rows is enough. The scheduler
// calls GC at startup and periodically; the package-level defaults
// are [RetentionPerJob], [RetentionMaxAge], and [RetentionMaxTotal].
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
	const trimQ = `
		DELETE FROM runs
		WHERE id IN (
			SELECT id FROM runs r1
			WHERE (
				SELECT COUNT(*) FROM runs r2
				WHERE r2.job_id = r1.job_id AND r2.started_at >= r1.started_at
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

// Counts returns the per-kind / per-state aggregate used by
// `eon status`. One round-trip.
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
