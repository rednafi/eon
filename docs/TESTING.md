# Testing eon

eon hosts production-critical schedules. The test suite is built to
match that bar: every command, every flag, every adverse input, every
documented exit code, and the multi-process daemon interactions.

This doc lists what is covered, where the coverage lives, and the
specific failure modes each test is designed to catch.

## Layers

```
eon/                  pure & store/engine/daemon/svc unit tests
cmd/eon/cli_test.go   in-process Cobra invocation tests (programmatic)
cmd/eon/script_test.go + testdata/script/*.txtar
                      end-to-end shell-shaped scenarios via testscript
                      (rogpeppe/go-internal); the binary is registered
                      as an in-process command so each script runs the
                      real `main` code path against a hermetic $WORK
                      directory.
tests/integration/    Linux container integration test (build tag
                      `integration`). Cross-compiles to linux/amd64,
                      mounts the binary into ubuntu:24.04, exercises
                      the full CLI from inside Linux to confirm
                      behaviour matches macOS exactly. Skips when
                      docker is not on PATH.
```

Run everything:

```
make test            # race-enabled, every package (no docker)
make test-int        # the Linux container test (requires docker)
```

Run just the script suite:

```
go test -run TestScripts ./cmd/eon/
```

Each script is one `.txtar` file in `cmd/eon/testdata/script/`. To add
a new scenario, drop a file in that directory; the test driver picks
it up automatically.

## What the suite asserts (the adversarial matrix)

### Time and schedule parsing (`eon/parse.go`, `eon/parse_test.go`)

- RFC3339 accepted; future required.
- Offsets `+30m`, `+2h`, `+45s`, `+3d` accepted.
- `today HH:MM`, `tomorrow 9am`, `tomorrow 5:30pm`, `tomorrow 23:59`,
  `tomorrow 12am`, `tomorrow 12pm` accepted.
- Rejected: `""`, whitespace, `"yesterday"`, `+0s`, `+-1h`, `+5y`,
  `today 25:00`, `tomorrow abc`, `today HH:MM` already past, past
  RFC3339.
- Cron expressions: standard 5-field, `@hourly`/`@daily`/`@every Xs`
  accepted; `""`, `"garbage"`, `"0 0 * *"`, `"@nope"` rejected.
- `JobSpec.Validate`: rejects missing name, missing command, empty
  argv[0], both/neither schedule, bad cron, past one-shot.

### Run scheduling (`eon/engine`)

- Cron `@every 1s` fires at least twice within a 2.5 s window.
- A second firing while the previous run is still in flight is
  recorded as `skipped_overlap` rather than running in parallel.
- A disabled job never fires while disabled and resumes after enable.
- The store reload counter is polled; a CLI-side `Add` is picked up
  within `PollInterval`.
- One-shot is marked `done` exactly once.
- **A one-shot whose `fire_at` is in the past at startup fires
  immediately** (was a silent-drop bug). Done one-shots are not
  re-fired.
- `ExecRunner` returns the child's exit code for non-zero exits and
  surfaces a real error only when the program fails to start.
- Captured output is capped at `store.MaxOutputBytes` (100 KiB) and
  carries an explicit truncation marker when overflow occurred.
- Engine startup runs `HealStaleRuns`, rewriting any rows left in
  `running` state by a previous crash to `fail` with exit code `-1`.

### Persistence (`eon/store`)

- Job CRUD round-trips: insert, fetch, update, delete.
- List filters: `kind`, `status`, no filter.
- Run lifecycle: `StartRun` returns the row with `status='running'`;
  `FinishRun` writes the captured blob and final status; `nil`
  output is coerced to `[]byte{}` to satisfy NOT NULL.
- `RecordOverlap` writes a `skipped_overlap` row with `started_at`
  and `finished_at` equal.
- `ListRuns` newest-first; `ListRunsSince` oldest-first.
- `GC`: retention by count (last N per job) and by age both enforced;
  cascade deletes rows whose parent job is deleted.
- `Counts`: total / cron / oneshot pending / oneshot done agree with
  `ListJobs`.
- `BumpVersion` monotonically increases.
- **Daemon claim**:
  - First `ClaimDaemonSlot` succeeds; subsequent claim with a live
    `liveProcess` returns `false` without error.
  - `ClaimDaemonSlot` succeeds again when the recorded pid is dead
    (takeover after crash).
  - `ReleaseDaemonSlot` only clears when the expected pid matches —
    a late-exiting predecessor cannot clobber a successor's claim.

### Daemon lifecycle (`eon/daemon`)

- `Probe` on an empty dir returns `Running=false`.
- `Probe` cleans stale PID files (impossible PID `999_999_999`).
- `WritePID` / `RemovePID` round-trip; `RemovePID` is idempotent.
- `Stop` on no-daemon returns `ErrDaemonDown`.

### CLI surface (`cmd/eon`)

In-process Cobra tests (`cli_test.go`) and the `.txtar` scripts cover:

#### add
- `--cron` and `--at` mutually exclusive (exit 2 when both, exit 2
  when neither).
- Missing command after `--` rejected (exit 2).
- Invalid cron → exit 5.
- Past `--at` → exit 5.
- Default name = joined argv.
- JSON spec on stdin: valid succeeds; malformed JSON → exit 2; past
  `fire_at` → exit 5.

#### ls
- Default lists all jobs.
- `--kind cron|oneshot` filters correctly; invalid value → exit 2.
- `--status enabled|disabled|done` filters correctly; invalid → exit 2.
- `--json` emits a parseable array (empty `[]` when nothing matches).

#### show / get
- Numeric ID required; non-numeric and `0`/negative rejected (exit 2).
- Unknown ID → exit 3.
- `--json` parseable.

#### edit
- Flag-driven edits: schedule, name, command (with shell-style
  quoting via `splitCommand`).
- `--cron` and `--at` mutually exclusive (exit 2).
- Unmatched quote in `--command` → exit 2.
- JSON spec on stdin replaces wholesale.
- Unknown ID → exit 3.

#### rm / delete
- Deletes; second `rm` → exit 3.
- Cascade: deletes a job's run history.

#### enable / disable
- Status transitions persist.
- Unknown ID → exit 3.
- Disable-while-disabled and enable-while-enabled are accepted
  (idempotent from the user's perspective).

#### logs
- No runs yet → exit 3.
- Bad ID → exit 2.
- Unknown job → exit 3.
- `--lines N` returns the last N lines.
- `--since DUR` emits each completed run in the window with a
  `==> run #ID status exit=N finished=TIME` header.
- `--follow` dumps the latest and streams new completed runs as
  headers.

#### status
- Text and `--json` both work without a daemon.
- After adds, counts reflect totals (`total`, `cron`, `oneshot_pending`).
- With a running daemon (in lifecycle script), `running=true`,
  `pid=N`, `started_at` populated.

#### install / uninstall
- Tested at the unit level only — invoking these in scripts would
  touch the developer's real launchd/systemd. Coverage lives in
  manual smoke tests.

#### stop
- With no daemon: exit 0, "no daemon running" — idempotent.
- With a running daemon: graceful SIGTERM, slot released, alongside
  PID-file cleanup.

#### daemon
- Single-daemon claim: a second `eon daemon` against the same data
  dir returns exit 4 with `ErrDaemonUp`.
- Graceful shutdown on SIGTERM (signal.NotifyContext).
- Defer-release uses a fresh `context.Background()` so the claim is
  cleared even when the parent context has already been cancelled.

#### exit-code contract (pinned)

| Code | Meaning                                                | Source                          |
| ---: | ------------------------------------------------------ | ------------------------------- |
|    0 | success                                                | implicit                        |
|    1 | unexpected (panic, I/O, generic)                       | `cmd/eon/root.go` default       |
|    2 | usage (bad flag, missing arg, mutually exclusive flags)| `errUsage` sentinel             |
|    3 | not found                                              | `eon.ErrNotFound`               |
|    4 | conflict (daemon already running)                      | `eon.ErrConflict`, `ErrDaemonUp`|
|    5 | precondition (invalid cron/time/spec, daemon down)     | `ErrInvalid*`, `ErrDaemonDown`  |

Each row is hit by at least one test case in `cli_test.go` or
`testdata/script/exit_codes.txt`.

### JSON stability

- `--json` on read commands is the LLM-driving contract.
- Zero `time.Time` fields are omitted (`omitzero` tag). Test:
  `testdata/script/json_no_zero_times.txt`.
- The empty list case emits `[]`, not `null`. Test:
  `testdata/script/rm_lifecycle.txt`.
- All fields in `eon.Job`, `eon.Run`, `eon.Status` are tagged; the
  field set is the wire contract.

### Concurrency

- `testdata/script/concurrent_adds.txt`: 8 parallel `eon add` against
  the same data dir all succeed. The bootstrap path is serialised by
  an OS-level flock on `.eon.openlock` held only while applying the
  schema; once Open returns, the flock is released and steady-state
  contention is handled by SQLite's own writer lock + `busy_timeout`.
  This pattern was added after the stress test surfaced
  `SQLITE_BUSY` during open-time pragma application.
- Daemon claim is exercised under deliberate races by the unit test
  `TestRepoDaemonClaim` (`eon/store/sqlite_test.go`).
- The Linux container integration test re-runs the 8-way concurrent
  add inside Ubuntu to confirm the flock behaves identically there.

### Crash safety

- `HealStaleRuns` rewrites zombie `running` rows on engine startup.
  Test: `TestRepoHealStaleRuns` and the engine's `Start` call site.
- The daemon claim cross-checks the recorded pid with `kill -0`; a
  daemon SIGKILL'd before its defer could fire is detected and the
  claim is takeable by the next daemon.
- The PID file is best-effort and informational only — the claim in
  the meta table is authoritative. A missing or stale PID file does
  not impede daemon startup.

### Output capture

- 100 KiB cap per run.
- `[... output truncated ...]` marker appended exactly once when the
  cap is hit.
- Concurrent writers to the same `cappedBuf` are protected by a
  mutex (see `eon/engine/buf.go`).

## Adding tests

For a CLI behaviour change, prefer a `.txtar` script — it is the
medium that exercises Fang's flag parsing, the cobra command tree,
the service wiring, and the SQLite store together. The driver lives
in `cmd/eon/script_test.go`.

Scripts use these commands beyond stdlib testscript:
- `eon ARGS...` — runs the in-process eon main.
- `sleep`, `timeout` — system binaries (assumed present on mac+linux
  CI runners).

For a pure-core behaviour change, prefer a table-driven test in the
relevant package. For storage invariants, prefer a test against a
real `store.Repo` (tests use `t.TempDir()` and the real driver — no
mocks for SQLite).

## Non-goals

- Fuzzing of the CLI argument parser. Cobra+fang are upstream-tested.
- Property-based testing of cron expression evaluation. `robfig/cron`
  is upstream-tested.
- Stress tests beyond the 8-way concurrent add. If/when we run real
  multi-host workloads, add load tests then.
