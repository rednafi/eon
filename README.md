# eon

A personal job scheduler. It runs cron-style recurring jobs and one-shot
jobs at a wall-clock time inside its own daemon, so it behaves the same
on macOS and Linux without touching the system cron.

State is a single SQLite file under the platform's data directory:

- macOS: `~/Library/Application Support/eon/eon.db`
- Linux: `$XDG_DATA_HOME/eon/eon.db` (falls back to `~/.local/share/eon`)

Captured output for the last 100 runs per job is retained for 100 days.

## Quickstart

Install with Go:

```sh
go install github.com/rednafi/eon/cmd/eon@latest
```

Register the daemon with launchd (macOS) or systemd --user (Linux) so it
restarts across logins and crashes:

```sh
eon install
```

Add a job:

```sh
# A cron job that runs every hour:
eon add --cron '@hourly' --name backup -- /usr/local/bin/backup.sh

# A one-shot at a wall-clock time:
eon add --at 'tomorrow 9am' --name reminder -- say "stand up"

# A one-shot with a relative offset:
eon add --at '+30m' -- ping -c 1 example.com
```

List and inspect:

```sh
eon ls                       # all jobs
eon ls --json                # JSON for scripting
eon show backup              # one job's details
eon logs backup --lines 50   # last 50 lines of captured output
eon logs backup --follow     # stream new output as runs complete
eon status                   # daemon state and counts
```

Control the lifecycle:

```sh
eon disable backup           # stop a job firing without deleting it
eon enable backup
eon rm backup                # delete the job and its run history
eon stop                     # ask the daemon to exit
eon prune                    # purge done one-shots and disabled jobs
eon uninstall                # remove the supervisor unit
```

Exit codes:

| Code | Meaning                                       |
| ---: | --------------------------------------------- |
|    0 | success                                       |
|    1 | unexpected (panic, I/O, generic)              |
|    2 | usage (bad flag, missing arg)                 |
|    3 | not found                                     |
|    4 | conflict (daemon already running)             |
|    5 | precondition (invalid cron/time/spec)         |

## Development

Requires Go 1.26 or newer.

```sh
make build   # build ./bin/eon
make test    # race-enabled, every package
make vet     # go vet
make lint    # golangci-lint
make tidy    # go mod tidy
make clean   # remove ./bin
```

Run a single package's tests:

```sh
go test -race -count=1 ./sched/...
```

Run the testscript-based CLI scenarios:

```sh
go test -race -run TestScripts ./cmd/eon/
```

Each `.txtar` file under `cmd/eon/testdata/script/` is one scenario;
drop a new file there to add coverage.

### Project layout

```
cmd/eon/   CLI entrypoint and command tree (cobra)
sched/     Scheduler loop. Fires due jobs from the SQLite schedule.
store/     SQLite persistence. The schedule lives here, not in memory.
daemon/    Per-user data dir, plus the flock-based single-instance lock.
tests/     Native end-to-end coverage per platform.
```

### Releases

Tagged `v*` pushes trigger a goreleaser build via
`.github/workflows/release.yml`. Output is binaries for linux and darwin
on amd64 and arm64, plus a checksums file.
