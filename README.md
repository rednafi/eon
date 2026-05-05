# eon

A local cron monitor. One-stop view of every recurring job your machine is
running — crontab entries, launchd agents (macOS), systemd user timers (Linux)
— with a k9s-style TUI for drilling into a job's schedule, raw definition, and
log tail.

eon also supports lightweight authoring on writable backends. `eon add`
and `eon edit` accept a portable schedule DSL — `@every <Go duration>`
or `@hourly|daily|weekly|monthly|yearly` — that maps cleanly to
crontab, launchd plists, and systemd timers. 5-field cron expressions
work on the user crontab; on launchd/systemd they're rejected with a
suggestion to target the crontab source instead. System backends
(`/etc/crontab`, `/etc/cron.d`, `/Library/Launch*`,
`/etc/systemd/system`) remain strictly read-only.

## Install

```sh
go install github.com/rednafi/eon@latest
```

## Use

```sh
eon                       # launch the TUI (default)
eon list                  # table of every known cron
eon list --json           # machine-readable
eon show <id>             # full detail for one cron
eon logs <id> [-n 100]    # tail stdout/stderr (when configured)
eon add --schedule '@daily' --command '/bin/echo hi'
                          # add to first writable backend (or --source <name>)
eon edit <id> --schedule '@hourly'   # change schedule, command, or both
eon delete <id> [--yes]   # stop and remove a cron
```

`<id>` is either the full ID (`launchd-user:com.foo.bar`, `systemd-user:foo`,
`crontab:abcd1234`) or any unique case-insensitive substring.

## Sources

| Platform | Reads from | Removes by |
| --- | --- | --- |
| macOS | user crontab + `~/Library/LaunchAgents/*.plist` | rewriting crontab / `launchctl unload` + delete plist |
| Linux | user crontab + `~/.config/systemd/user/*.timer` | rewriting crontab / `systemctl --user stop`+`disable` + delete units |

System-level locations (`/etc/crontab`, `/Library/LaunchAgents`, etc.) are
intentionally not surfaced — eon focuses on *your* crons, not the OS's.

## Keys (TUI)

```
↑/k ↓/j           move
g G               top / bottom
/                 filter
enter             open detail
tab / shift+tab   switch detail tabs (Overview / Raw / Logs)
n                 new cron (form)
e                 edit cron (form)
d                 delete (with confirmation)
a                 toggle system-scope rows
r                 refresh
esc               back
q                 quit
```

## Architecture

The `cron/` package is the domain core: a `Source` interface, an optional
`Mutator` interface for backends that can write, and a `Manager` that
fans calls out across them. Per-backend subpackages (`crontab/`,
`launchd/`, `systemd/`, `etccron/`) each split into:

- `parser.go` — pure functional core. No syscalls, no Runner. Compiles
  on every platform; tested without containers.
- `<name>_<os>.go` — imperative shell that drives the actual binary
  (`crontab`, `launchctl`, `systemctl`) through an injectable Runner.
  Build-tagged when the binary only exists on one OS.

The CLI (`cli/`) and TUI (`tui/`) packages depend only on `cron.Source`
and `cron.Manager` — they never name a concrete backend. Adding a new
source means writing a subpackage and listing it in `factory_<os>.go`
at the repo root.

## Testing

```sh
make test                 # unit + integration suite, race detector on
make test-container       # full Linux suite inside a container
make smoke-container      # end-to-end CLI smoke (build + add + list + delete)
make lint                 # golangci-lint
go test -fuzz=Fuzz... ./cron/<sub>/...   # 6 fuzz targets across the parsers
```

## License

MIT
