#!/usr/bin/env bash
# Manual smoke test: build eon inside a Debian container, create a real
# crontab entry, exercise list/show/logs/delete via the CLI, and assert that
# the job is gone afterwards. Used both for the user-facing
# `make test-container` shortcut and for ad-hoc Linux verification.
set -euo pipefail

cd "$(dirname "$0")/.."

docker build -q -f Dockerfile.test -t eon-test . >/dev/null

docker run --rm eon-test bash -c '
  set -euo pipefail
  export PATH=/usr/local/go/bin:$PATH
  cd /home/tester/src
  go build -o /tmp/eon ./cmd/eon

  # Install a deterministic test crontab.
  cat <<EOF | crontab -
*/5 * * * * /bin/echo eon-smoke-test
@daily /bin/true
EOF

  echo "--- eon list ---"
  /tmp/eon list

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
'
