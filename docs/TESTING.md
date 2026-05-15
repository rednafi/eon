# Testing eon

The test suite covers every command, every flag, every documented exit
code, and the multi-process daemon interactions.

## Layout

```
parse_test.go            Pure parser (cron, time shortcuts, JobSpec.Validate).
sched/sched_test.go      Scheduler loop with a fake Runner plus a real
                         ExecRunner for the output-capture path.
store/store_test.go      SQLite persistence against a real DB in t.TempDir().
daemon/daemon_test.go    Per-user data dir + flock-based single-instance lock.
cmd/eon/cli_test.go      Programmatic Cobra invocations of the CLI.
cmd/eon/script_test.go   Driver for the .txtar scenarios under
                         testdata/script/. Each file is one scenario; the
                         driver picks them up automatically.
tests/darwin_test.go     Native macOS end-to-end. Builds the binary and
                         runs it against a temp data dir.
tests/linux_test.go      Same shape, native Linux.
```

Run everything:

```sh
make test                       # race-enabled, every package
go test -race -count=1 ./...    # same thing without the Makefile
```

Run just the script scenarios:

```sh
go test -race -run TestScripts ./cmd/eon/
```

## What the suite asserts

### Time and schedule parsing (`parse.go`)

- RFC3339 accepted; future required.
- Offsets `+30m`, `+2h`, `+45s`, `+3d` accepted.
- `today HH:MM`, `tomorrow 9am`, `tomorrow 5:30pm`, `tomorrow 23:59`,
  `tomorrow 12am`, `tomorrow 12pm` accepted.
- Rejected: empty string, whitespace, `yesterday`, `+0s`, `+-1h`, `+5y`,
  `today 25:00`, `tomorrow abc`, `today HH:MM` already past, past RFC3339.
- Cron expressions: standard 5-field, `@hourly`/`@daily`/`@every Xs`.
  Rejected: empty, garbage, partial fields, unknown descriptor.
- Pathological `@every` durations like `0s`/`-1s` rejected upstream, so
  the scheduler can't tight-loop.
- `JobSpec.Validate` rejects missing name, missing command, empty
  `argv[0]`, both/neither schedule fields, bad cron, past one-shot.

### Run scheduling (`sched/`)

- A cron `@every 1s` fires at least twice within a 2.5s window.
- A second firing while the previous run is still in flight is recorded
  as `skipped_overlap` rather than running in parallel.
- A disabled job never fires while disabled.
- `Scheduler.Wake()` interrupts the current sleep so a freshly-added
  job is picked up without waiting out the sleep.
- A one-shot whose `FireAt` is in the past at startup fires once on
  startup. Once it runs, the job is marked `done` and is never re-fired.
- `ExecRunner` returns the child's exit code for non-zero exits, and
  surfaces a real error only when the program fails to start.
- Captured output is capped at `store.MaxOutputBytes` (100 KiB) with an
  explicit truncation marker when overflow occurred.
- A start-time failure (e.g. argv[0] doesn't exist) is written to the
  captured-output writer so `eon logs JOB` shows the reason instead of
  an empty log.

### Persistence (`store/`)

- Job CRUD round-trips.
- `ListJobs` filters: kind, status, no filter.
- `RecordRun` writes a single row when the runner has finished. There
  is no half-written `running` row, so a crashed daemon leaves nothing
  to heal.
- `RecordOverlap` writes a `skipped_overlap` row with `started_at` and
  `finished_at` equal.
- `ListRuns` is newest-first; `ListRunsSince` is oldest-first.
- `GC` enforces retention by count (last N per job) and by age, and
  cascades to runs when a job is deleted.
- `Counts` (total / cron / oneshot pending / oneshot done) agrees with
  `ListJobs`.
- The `next_fire_at` column invariant: `AddJob` writes it, status
  transitions update it, `AdvanceNextFire` is reflected by `DueJobs`
  and `SoonestDeadline`. Regression here would either drop firings or
  fire too often.

### Daemon lifecycle (`daemon/`)

- `DataDir` resolves correctly on macOS (Application Support) and Linux
  (`$XDG_DATA_HOME` with `~/.local/share/eon` fallback).
- `AcquireRunLock` takes the flock on `$dataDir/eon.lock` and writes
  `pid\nunixnano\n`. The release closure unlocks and closes the fd.
- A second `AcquireRunLock` against the same dir returns `(nil, nil)`
  while the first holds the lock.
- `ProbeRunLock` on an empty dir reports no daemon. After release, it
  reports no daemon again.
- `SignalDaemon` returns `(false, nil)` when no daemon is running.

### CLI surface (`cmd/eon/`)

The programmatic Cobra tests in `cli_test.go` cover add / ls / show /
rm round-trips, enable+disable, idempotent stop, exit codes for bad
input, and status JSON.

The `.txtar` scripts under `testdata/script/` cover:

- `add_basic.txt` / `add_validation.txt`: `--cron` / `--at` mutual
  exclusion, missing command, invalid cron, past `--at`.
- `list_filters.txt` / `list_pagination.txt`: `--kind`, `--status`,
  `--json`, empty `[]` output.
- `show_get.txt`: lookup by ID or name; unknown ID returns exit 3.
- `enable_disable.txt`: status transitions persist; idempotent.
- `rm_lifecycle.txt`: cascade deletes runs; second `rm` is exit 3.
- `logs_no_runs.txt` / `logs_since_and_lines.txt` / `logs_follow.txt`:
  `--lines N`, `--since DUR` with run headers, `--follow` streaming.
- `status.txt`: text and JSON outputs without a daemon.
- `stop_idempotent.txt`: with no daemon, exit 0.
- `daemon_conflict.txt` / `daemon_lifecycle.txt`: single-daemon claim
  via flock; second daemon exits 4; SIGTERM is graceful.
- `concurrent_adds.txt`: 8 parallel `eon add` against the same data
  dir all succeed (SQLite WAL + `busy_timeout` plus `SetMaxOpenConns(1)`
  serialise writes).
- `exit_codes.txt`: pins the exit-code table below.
- `json_no_zero_times.txt`: zero `time.Time` fields are omitted under
  the `omitzero` tag.
- `time_shortcuts.txt`: `+30m`, `tomorrow 9am`, etc.
- `prune.txt`: deletes done one-shots and disabled jobs.
- `seppuku.txt`: removes every trace of eon from the host.
- `help_text.txt` / `missing_command_warning.txt`: help output and
  arg-parsing edge cases.

### Exit-code contract

| Code | Meaning                                       | Source                          |
| ---: | --------------------------------------------- | ------------------------------- |
|    0 | success                                       | implicit                        |
|    1 | unexpected (panic, I/O, generic)              | `cmd/eon/root.go` default       |
|    2 | usage (bad flag, missing arg, mutex flags)    | `errUsage` sentinel             |
|    3 | not found                                     | `eon.ErrNotFound`               |
|    4 | conflict (daemon already running)             | `eon.ErrConflict`, `ErrDaemonUp`|
|    5 | precondition (invalid cron/time/spec)         | `ErrInvalidCron` etc.           |

Each row is hit by at least one test case.

### Native end-to-end (`tests/`)

`tests/linux_test.go` and `tests/darwin_test.go` each build a fresh
binary into `t.TempDir()` and exercise the user-visible CLI surface:
status on empty, add then ls, exit codes, 8-way concurrent adds, and
the full daemon lifecycle (start, status reports running, second
daemon conflict, log capture). Each platform also has one supervisor
test that checks the install-discovery path against a fake HOME or
fake `XDG_CONFIG_HOME`.

## Adding tests

For a CLI behaviour change, prefer a `.txtar` script. The driver lives
in `cmd/eon/script_test.go` and runs the in-process eon main against a
hermetic `$WORK` directory, so the script exercises fang's flag
parsing, the cobra command tree, and the SQLite store together.

For a pure-core behaviour change, prefer a table-driven test in the
relevant package. For storage invariants, write against a real
`store.Store` in `t.TempDir()`. No SQLite mocks.

## Non-goals

- Fuzzing the CLI argument parser. Cobra and fang are upstream-tested.
- Property-based testing of cron expression evaluation. `robfig/cron`
  is upstream-tested.
- Load tests. If eon ever runs real multi-host workloads, add them then.
