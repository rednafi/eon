# eon

A local cron monitor. One-stop view of every recurring job your machine is
running — crontab entries, launchd agents (macOS), systemd user timers (Linux)
— with a k9s-style TUI for drilling into a job's schedule, raw definition, and
log tail.

eon is a *monitor*, not a scheduler. It reads what's already there. Creation
is intentionally out of scope, because the crons it cares about are written
by other tools (Claude Code, Codex, custom scripts, package managers) that
each have their own scheduling UX.

## Install

```sh
go install github.com/rednafi/eon/cmd/eon@latest
```

## Use

```sh
eon                       # launch the TUI (default)
eon list                  # table of every known cron
eon list --json           # machine-readable
eon show <id>             # full detail for one cron
eon logs <id> [-n 100]    # tail stdout/stderr (when configured)
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
d                 delete (with confirmation)
r                 refresh
esc               back
q                 quit
```

## License

MIT
