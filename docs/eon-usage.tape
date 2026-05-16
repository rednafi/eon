Output docs/eon-usage.gif
Output docs/eon-usage.mp4

Set Shell "bash"
Set Theme "GitHub Dark"
Set FontFamily "JetBrains Mono"
Set FontSize 26
Set Width 1920
Set Height 1080
Set Padding 24
Set Framerate 12
Set TypingSpeed 32ms
Set WindowBar Colorful
Set BorderRadius 8

Hide
Type `export PS1=$'\n\[\033[1;32m\]eon-demo\[\033[0m\] $ '` Enter
Type `section() { printf '\033[1A\033[2K\r\033[1;36m%s\033[0m\n' "$1"; }` Enter
Type "clear" Enter
Type "make build >/dev/null" Enter
Type "export EON_DATA=$(mktemp -d)" Enter
Type `printf '%s\n' '#!/bin/sh' "printf '\033[35moneshot fired\033[0m\n'" 'date +%T' > "$EON_DATA/once.sh"` Enter
Type `printf '%s\n' '#!/bin/sh' "printf '\033[33mcron tick\033[0m\n'" 'date +%T' > "$EON_DATA/tick.sh"` Enter
Type `chmod +x "$EON_DATA/once.sh" "$EON_DATA/tick.sh"` Enter
Type `eon() { ./bin/eon --data-dir "$EON_DATA" --quiet "$@"; }` Enter
Type `./bin/eon --data-dir "$EON_DATA" --quiet daemon >/tmp/eon-usage-demo-daemon.log 2>&1 &` Enter
Type "disown" Enter
Type "sleep 0.5" Enter
Type `printf '\033c'` Enter
Sleep 500ms
Type `section "1. Check the daemon"` Enter
Show

Type "eon status" Enter
Sleep 1600ms

Hide
Type `section "2. Schedule a one-shot and a recurring job"` Enter
Show

Type `eon add --at +5s --name once-demo -- sh "$EON_DATA/once.sh"` Enter
Sleep 1600ms

Type `eon add --cron '@every 2s' --name heartbeat -- sh "$EON_DATA/tick.sh"` Enter
Sleep 1600ms

Hide
Type `section "3. List scheduled work"` Enter
Show

Type "eon ls" Enter
Sleep 1600ms

Hide
Type `section "4. Follow logs to prove the cron job is firing"` Enter
Show

Type "eon logs heartbeat --follow" Enter
Sleep 6s
Ctrl+C
Sleep 900ms

Hide
Type `section "5. Inspect final state and the one-shot log"` Enter
Show

Type "eon ls" Enter
Sleep 1600ms

Type "eon logs once-demo" Enter
Sleep 1600ms

Hide
Type `section "6. Disable the cron job, then prune inactive jobs"` Enter
Show

Type "eon disable heartbeat" Enter
Sleep 1600ms

Type "eon prune" Enter
Sleep 1800ms

Type "eon prune --force" Enter
Sleep 1600ms

Type "eon ls" Enter
Sleep 1600ms

Type "eon stop" Enter
Sleep 1600ms

Hide
Type `rm -rf "$EON_DATA"` Enter
Show
