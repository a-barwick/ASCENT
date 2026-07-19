#!/bin/sh
set -eu

database=ascent_integration_test
container_database_url="postgres://ascent:ascent@postgres:5432/$database?sslmode=disable"
host_database_url="postgres://ascent:ascent@localhost:5432/$database?sslmode=disable"

cleanup() {
  ./scripts/compose.sh exec -T postgres psql -v ON_ERROR_STOP=1 -U ascent -d postgres \
    -c "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = '$database'" \
    -c "DROP DATABASE IF EXISTS $database" >/dev/null 2>&1 || true
}
trap cleanup EXIT INT TERM

./scripts/compose.sh up -d --wait postgres
cleanup
./scripts/compose.sh exec -T postgres createdb -U ascent "$database"
./scripts/compose.sh --profile tools run --rm -e DATABASE_URL="$container_database_url" migrate up

DATABASE_URL="$host_database_url" \
  go test -count=1 ./internal/platform/postgres/game -run Integration -v

echo "integration test passed: seeded snapshot, commands, rollback, and recovery"
