# eon

A tiny job scheduler. It does two things:

- recurring (cron-style) jobs
- one-shot jobs at a wall-clock time

Both run inside its own daemon, so behaviour is the same on macOS
and Linux. The system cron isn't involved.

State lives in a single SQLite file under the platform's data
directory. Captured output for the last 100 runs per job is retained
for 100 days.

## Why

I've been using LLM agents to schedule both one-off and recurring
jobs. The agents I use keep their job ticker inside their own process
and there's little visibility into it. For some tasks that's
adequate, but for others I'd like to see all the scheduled jobs in
one place.

The system cron works, but it takes some shell-fu to check state and
tail the output. One-off scheduling is also inconsistent across
platforms: on macOS, the `at` daemon is usually off by default. So I
wanted a small, self-documenting CLI that an LLM can drive and that
behaves the same on macOS and Linux.

## Quickstart

### Install

macOS (Homebrew):

```sh
brew tap rednafi/eon https://github.com/rednafi/eon
brew install eon
```

Linux (curl):

```sh
curl -fsSL https://raw.githubusercontent.com/rednafi/eon/main/install.sh | sh
```

The script detects amd64/arm64, verifies the SHA-256 sum, and installs to
`/usr/local/bin/eon`. Override with `EON_PREFIX` or `EON_VERSION`.

From source:

```sh
go install github.com/rednafi/eon/cmd/eon@latest
```

### Run

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

## Development

Requires Go 1.26 or newer.

```sh
make build
make test
make vet
make lint
make tidy
make clean   
```

### Releases

Tagged `v*` pushes trigger a goreleaser build via
`.github/workflows/release.yml`. Output is binaries for linux and darwin
on amd64 and arm64, plus a checksums file.
