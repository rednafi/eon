# eon

A local cron monitor. One-stop view of every recurring job your machine is
running — crontab entries, launchd agents (macOS), systemd user timers (Linux)
— with a k9s-style TUI for drilling into a job's schedule, raw definition, and
log tail.

eon also supports lightweight authoring on writable backends: `eon add`
appends a line to your user crontab, `eon edit` rewrites a single entry in
place. System backends (`/etc/crontab`, `/etc/cron.d`, `/Library/Launch*`)
remain strictly read-only.

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

## License

MIT
