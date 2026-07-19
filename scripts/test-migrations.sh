#!/bin/sh
set -eu

database=ascent_migration_test
database_url="postgres://ascent:ascent@postgres:5432/$database?sslmode=disable"

cleanup() {
  ./scripts/compose.sh exec -T postgres psql -v ON_ERROR_STOP=1 -U ascent -d postgres \
    -c "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = '$database'" \
    -c "DROP DATABASE IF EXISTS $database" >/dev/null 2>&1 || true
}
trap cleanup EXIT INT TERM

./scripts/compose.sh up -d --wait postgres
cleanup
./scripts/compose.sh exec -T postgres createdb -U ascent "$database"

./scripts/compose.sh --profile tools run --rm -e DATABASE_URL="$database_url" migrate up

initial_versions=$(./scripts/compose.sh exec -T postgres psql -At -v ON_ERROR_STOP=1 \
  -U ascent -d "$database" \
  -c "SELECT string_agg(version, ',' ORDER BY version) FROM public.schema_migrations")
initial_count=$(./scripts/compose.sh exec -T postgres psql -At -v ON_ERROR_STOP=1 \
  -U ascent -d "$database" \
  -c "SELECT count(*) FROM public.schema_migrations")
latest_version=$(./scripts/compose.sh exec -T postgres psql -At -v ON_ERROR_STOP=1 \
  -U ascent -d "$database" \
  -c "SELECT version FROM public.schema_migrations ORDER BY version DESC LIMIT 1")

if [ -z "$latest_version" ]; then
  echo "migration stack applied no versions" >&2
  exit 1
fi

expected_tables=36
table_count=$(./scripts/compose.sh exec -T postgres psql -At -v ON_ERROR_STOP=1 \
  -U ascent -d "$database" \
  -c "SELECT count(*) FROM unnest(ARRAY[
    'platform.command_log',
    'platform.event_outbox',
    'platform.event_global_sequence',
    'platform.event_topic_sequences',
    'identity.players',
    'identity.sessions',
    'companies.companies',
    'companies.memberships',
    'ledger.accounts',
    'ledger.journals',
    'ledger.entries',
    'inventory.locations',
    'inventory.commodities',
    'inventory.holdings',
    'inventory.movements',
    'markets.markets',
    'markets.orders',
    'markets.order_reservations',
    'markets.trades',
    'production.facility_types',
    'production.recipes',
    'production.facilities',
    'production.jobs',
    'freight.routes',
    'freight.contracts',
    'freight.deliveries',
    'workspace.devices',
    'workspace.views',
    'workspace.panel_deliveries',
    'chat.channels',
    'chat.messages',
    'alerts.rules',
    'alerts.notifications',
    'operator_admin.grants',
    'operator_admin.compensations',
    'platform.command_relations'
  ]) AS expected(name)
  WHERE to_regclass(expected.name) IS NOT NULL")
if [ "$table_count" != "$expected_tables" ]; then
  echo "expected $expected_tables first-playable tables, found $table_count" >&2
  exit 1
fi

event_sequences=$(./scripts/compose.sh exec -T postgres psql -At -v ON_ERROR_STOP=1 \
  -U ascent -d "$database" \
  -c "WITH inserted AS (
    INSERT INTO platform.event_outbox (
      event_id,
      protocol_version,
      topic,
      event_type,
      occurred_at,
      payload
    )
    VALUES
      (
        '00000000-0000-4000-8000-000000000001',
        '1.1.0',
        'migration-test:one',
        'MIGRATION_TEST_ONE',
        '2077-05-24T14:38:04.182Z',
        '{}'::jsonb
      ),
      (
        '00000000-0000-4000-8000-000000000002',
        '1.1.0',
        'migration-test:two',
        'MIGRATION_TEST_TWO',
        '2077-05-24T14:38:04.183Z',
        '{}'::jsonb
      )
    RETURNING sequence, topic_sequence
  )
  SELECT string_agg(
    sequence::text || ':' || topic_sequence::text,
    ',' ORDER BY sequence
  )
  FROM inserted")
if [ "$event_sequences" != "1:1,2:1" ]; then
  echo "unexpected global/topic event allocation: $event_sequences" >&2
  exit 1
fi

global_event_cursor=$(./scripts/compose.sh exec -T postgres psql -At -v ON_ERROR_STOP=1 \
  -U ascent -d "$database" \
  -c "SELECT last_sequence
  FROM platform.event_global_sequence
  WHERE singleton")
if [ "$global_event_cursor" != "2" ]; then
  echo "unexpected global event cursor: $global_event_cursor" >&2
  exit 1
fi

./scripts/compose.sh --profile tools run --rm -e DATABASE_URL="$database_url" migrate down

latest_still_applied=$(./scripts/compose.sh exec -T postgres psql -At -v ON_ERROR_STOP=1 \
  -U ascent -d "$database" \
  -c "SELECT EXISTS (
    SELECT 1
    FROM public.schema_migrations
    WHERE version = '$latest_version'
  )")
rolled_back_count=$(./scripts/compose.sh exec -T postgres psql -At -v ON_ERROR_STOP=1 \
  -U ascent -d "$database" \
  -c "SELECT count(*) FROM public.schema_migrations")
if [ "$latest_still_applied" != "f" ] \
  || [ "$rolled_back_count" -ne $((initial_count - 1)) ]; then
  echo "latest rollback did not remove exactly $latest_version" >&2
  exit 1
fi

./scripts/compose.sh --profile tools run --rm -e DATABASE_URL="$database_url" migrate up

reapplied_versions=$(./scripts/compose.sh exec -T postgres psql -At -v ON_ERROR_STOP=1 \
  -U ascent -d "$database" \
  -c "SELECT string_agg(version, ',' ORDER BY version) FROM public.schema_migrations")
if [ "$reapplied_versions" != "$initial_versions" ]; then
  echo "latest migration did not reapply to the original version set" >&2
  exit 1
fi

while :; do
  remaining=$(./scripts/compose.sh exec -T postgres psql -At -v ON_ERROR_STOP=1 \
    -U ascent -d "$database" \
    -c "SELECT count(*) FROM public.schema_migrations")
  if [ "$remaining" -eq 0 ]; then
    break
  fi
  ./scripts/compose.sh --profile tools run --rm -e DATABASE_URL="$database_url" migrate down
done

domain_schema_count=$(./scripts/compose.sh exec -T postgres psql -At -v ON_ERROR_STOP=1 \
  -U ascent -d "$database" \
  -c "SELECT count(*)
  FROM pg_namespace
  WHERE nspname IN (
    'platform',
    'identity',
    'companies',
    'ledger',
    'inventory',
    'markets',
    'production',
    'freight',
    'workspace',
    'chat',
    'alerts',
    'operator_admin'
  )")
if [ "$domain_schema_count" -ne 0 ]; then
  echo "full rollback left $domain_schema_count ASCENT schemas behind" >&2
  exit 1
fi

./scripts/compose.sh --profile tools run --rm -e DATABASE_URL="$database_url" migrate up

final_versions=$(./scripts/compose.sh exec -T postgres psql -At -v ON_ERROR_STOP=1 \
  -U ascent -d "$database" \
  -c "SELECT string_agg(version, ',' ORDER BY version) FROM public.schema_migrations")
if [ "$final_versions" != "$initial_versions" ]; then
  echo "full reapply did not restore the original migration set" >&2
  exit 1
fi

echo "migration test passed: latest up/down/up and full reverse rollback/reapply"
