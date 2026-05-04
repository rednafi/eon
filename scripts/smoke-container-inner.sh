#!/usr/bin/env bash
# Runs inside the eon-test container as root, drops a system cron, then
# re-execs as `tester` to exercise eon end-to-end.
set -euo pipefail

export PATH=/usr/local/go/bin:$PATH
cd /home/tester/src
chown -R tester:tester /home/tester

# /etc/cron.d entry: only visible with `eon list --all`.
echo "*/15 * * * * root /bin/echo eon-smoke-system" > /etc/cron.d/eon-smoke

runuser -u tester -- bash <<'INNER'
set -euo pipefail
export PATH=/usr/local/go/bin:$PATH
cd /home/tester/src
go build -o /tmp/eon .

# User-scope crontab: visible by default.
crontab - <<'CRON'
*/5 * * * * /bin/echo eon-smoke-test
@daily /bin/true
CRON

echo "--- eon list (user only) ---"
/tmp/eon list
if /tmp/eon list | grep -q crontab-system; then
  echo "FAIL: system entry leaked into default list" >&2
  exit 1
fi

echo "--- eon list --all (with system) ---"
/tmp/eon list --all | head -25
if ! /tmp/eon list --all | grep -qE 'crontab-system.*eon-smoke'; then
  echo "FAIL: --all did not surface /etc/cron.d/eon-smoke" >&2
  exit 1
fi

echo "--- eon show eon-smoke-test ---"
/tmp/eon show eon-smoke-test

echo "--- eon delete eon-smoke-test --yes ---"
/tmp/eon delete eon-smoke-test --yes

echo "--- post-delete list ---"
/tmp/eon list
if /tmp/eon list | grep -q eon-smoke-test; then
  echo "FAIL: eon-smoke-test still present" >&2
  exit 1
fi
echo "OK"
INNER
