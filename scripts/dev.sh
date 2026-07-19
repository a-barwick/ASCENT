#!/bin/sh
set -eu

: "${ASCENT_DEV_IDENTITY:=1}"
: "${ASCENT_SEED_SCENARIO:=1}"
: "${ASCENT_DEV_SESSION_SECRET:=ascent-local-development-session-secret-only}"
export ASCENT_DEV_IDENTITY ASCENT_SEED_SCENARIO ASCENT_DEV_SESSION_SECRET

cleanup() {
  trap - INT TERM EXIT
  kill "$server_pid" "$web_pid" 2>/dev/null || true
  wait "$server_pid" "$web_pid" 2>/dev/null || true
}

trap cleanup INT TERM EXIT

go run ./apps/server serve &
server_pid=$!

pnpm --filter @ascent/web dev &
web_pid=$!

wait "$server_pid" "$web_pid"
