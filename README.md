# eon

A tiny job scheduler. It does two things:

- recurring (cron-style) jobs
- one-shot jobs at a wall-clock time

Both run inside its own daemon, so behaviour is the same on macOS and Linux. The system cron
isn't involved.

State lives in a single SQLite file under the platform's data directory. Captured output for
the last 100 runs per job is retained for 100 days.

## Why

I've been using LLM agents to schedule both one-off and recurring jobs. The agents I use
keep their job ticker inside their own process and there's little visibility into it. For
some tasks that's adequate, but for others I'd like to see all the scheduled jobs in one
place.

The system cron works, but it takes some shell-fu to check state and tail the output.
One-off scheduling is also inconsistent across platforms: on macOS, the `at` daemon is
usually off by default. So I wanted a small, self-documenting CLI that an LLM can drive and
that behaves the same on macOS and Linux.

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

The script detects amd64/arm64, resolves the latest GitHub release, verifies the SHA-256
sum, and installs to `/usr/local/bin/eon`. Override with `EON_PREFIX` or pin a release
with `EON_VERSION`.

From source:

```sh
go install github.com/rednafi/eon/cmd/eon@latest
```

### Use

Register the daemon with launchd (macOS) or systemd --user (Linux) so it restarts across
logins and crashes:

```sh
# Register the supervisor unit once.
eon install
```

Add recurring jobs. Everything after `--` is the command eon will run:

```sh
# Record weekday disk space.
eon add --cron '0 9 * * 1-5' --name disk-space -- sh -c 'date; df -h "$HOME"'

# Check a website every 15 minutes.
eon add --cron '*/15 * * * *' --name homepage-check -- curl -fsS https://example.com
```

Add one-shot jobs:

```sh
# Run after a relative delay.
eon add --at '+30m' --name stretch -- sh -c 'printf "stand up and stretch\n"'

# Run at a wall-clock time.
eon add --at 'tomorrow 9am' --name morning-note -- sh -c 'printf "review calendar\n"'
```

List and inspect:

```sh
# List jobs in a compact table.
eon ls

# Emit JSON for scripts.
eon ls --json

# Show one job's schedule and state.
eon show disk-space

# Read captured output after a run has completed.
eon logs disk-space --lines 50

# Stream future completed runs.
eon logs disk-space --follow

# Check daemon state, supervisor state, and job counts.
eon status
```

Control the lifecycle:

```sh
# Pause a job without deleting it.
eon disable disk-space

# Re-enable it.
eon enable disk-space

# Delete a job and its run history.
eon rm stretch

# Ask the daemon to exit.
eon stop

# Purge done one-shots and disabled jobs.
eon prune

# Remove the supervisor unit.
eon uninstall
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

Tagged `v*` pushes trigger a goreleaser build via `.github/workflows/release.yml`. Output is
binaries for linux and darwin on amd64 and arm64, plus a checksums file.
