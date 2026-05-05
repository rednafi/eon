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

| Platform | User scope (writable) | System scope (read-only) |
| --- | --- | --- |
| macOS | user crontab + `~/Library/LaunchAgents/*.plist` | `/Library/LaunchAgents`, `/Library/LaunchDaemons`, `/System/Library/Launch*` |
| Linux | user crontab + `~/.config/systemd/user/*.timer` | `/etc/crontab`, `/etc/cron.d/*`, `/etc/systemd/system`, `/usr/lib/systemd/system` |

Press `a` in the TUI to toggle system-scope rows; `eon list` accepts
`--all` to include them.

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

The `cron/` package is the domain core: a `Source` interface, an
optional `Mutator` interface for backends that can write, and a
`Manager` that fans calls out across them. Per-backend subpackages
(`crontab/`, `launchd/`, `systemd/`, `etccron/`) each split into:

- `parser.go` — pure functional core. No syscalls, no Runner. Compiles
  on every platform; tested without containers.
- `<name>_<os>.go` — imperative shell that drives the actual binary
  (`crontab`, `launchctl`, `systemctl`) through an injectable Runner.
  Build-tagged when the binary only exists on one OS.

`Manager.List` queries every Source in parallel; on macOS where six
launchd directories sit behind it, that turns sum-of-source-latencies
into max-of-source-latencies. `launchd.List` further parallelises plist
decode with a small worker pool.

The CLI (`cli/`) and TUI (`tui/`) packages depend only on `cron.Source`
and `cron.Manager` — they never name a concrete backend. The
`tests/architecture_test.go` import-graph guard fails CI if anyone
breaks that rule.

### Format parsers — what we delegate

| Format | Parser | Status |
| --- | --- | --- |
| Cron expressions (`*/5 * * * *`, `@every 5m`) | [`github.com/robfig/cron/v3`](https://github.com/robfig/cron) | The same parser Kubernetes uses |
| launchd plist (XML & binary) | [`howett.net/plist`](https://github.com/DHowett/go-plist) | Used for both decode and encode — encoder/decoder can't drift |
| systemd unit files | [`github.com/coreos/go-systemd/v22/unit`](https://github.com/coreos/go-systemd) | Used by Kubernetes node tooling and Docker for unit-file I/O |

Hand-rolled bits are limited to crontab line splitting (`<schedule>
<command>`) and the portable `@every <duration>` schedule DSL — both
trivial, both fuzz-tested. The repo carries 8 `FuzzXxx` targets; run
them with `go test -fuzz=Fuzz... -fuzztime=30s ./cron/...`.

### Adding a new backend

1. Implement `cron.Source`. Add `cron.Mutator` if the backend can write.
2. Add a `var _ cron.Source = (*Foo)(nil)` compile-time guard.
3. Add a contract test:
   ```go
   crontest.Contract(t, "foo", newSource)
   crontest.MutatorContract(t, "foo", newSource, addSpec, editSpec)
   ```
   The contract enforces ID shape, idempotent Delete, and ErrNotFound
   semantics — same checks every built-in backend passes.
4. Wire it into `factory_<os>.go`.

## Testing

```sh
make test                 # unit + integration suite, race detector on
make test-container       # full Linux suite inside a container
make smoke-container      # end-to-end CLI smoke (build + add + list + delete)
make lint                 # golangci-lint
go test -fuzz=Fuzz... ./cron/<sub>/...   # 8 fuzz targets across the parsers
```

## License

MIT
