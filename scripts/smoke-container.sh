#!/usr/bin/env bash
# Manual smoke test: build eon inside a Debian container, drop a system cron
# in /etc/cron.d, write a per-user crontab, and exercise list/show/delete +
# the --all flag.
set -euo pipefail

cd "$(dirname "$0")/.."

docker build -q -f Dockerfile.test -t eon-test . >/dev/null

# We bind-mount the inner script so we can write a normal multi-line bash
# file instead of triple-nested heredocs.
docker run --rm --user root \
  -v "$(pwd)/scripts/smoke-container-inner.sh:/inner.sh:ro" \
  eon-test bash /inner.sh
