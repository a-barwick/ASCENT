#!/bin/sh
set -eu

database_url=${DATABASE_URL:?DATABASE_URL is required}
action=${1:-up}
migrations_dir=${MIGRATIONS_DIR:-/workspace/migrations}

psql "$database_url" -v ON_ERROR_STOP=1 <<'SQL'
CREATE TABLE IF NOT EXISTS public.schema_migrations (
  version TEXT PRIMARY KEY,
  applied_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
SQL

apply_up() {
  for file in "$migrations_dir"/*.up.sql; do
    [ -e "$file" ] || continue
    version=$(basename "$file" .up.sql)
    applied=$(psql "$database_url" -At -v ON_ERROR_STOP=1 \
      -c "SELECT EXISTS (SELECT 1 FROM public.schema_migrations WHERE version = '$version')")
    if [ "$applied" = "t" ]; then
      echo "skip $version"
      continue
    fi
    echo "apply $version"
    psql "$database_url" -v ON_ERROR_STOP=1 \
      -c "BEGIN" \
      -f "$file" \
      -c "INSERT INTO public.schema_migrations (version) VALUES ('$version')" \
      -c "COMMIT"
  done
}

apply_down() {
  version=$(psql "$database_url" -At -v ON_ERROR_STOP=1 \
    -c "SELECT version FROM public.schema_migrations ORDER BY version DESC LIMIT 1")
  if [ -z "$version" ]; then
    echo "no migration to roll back"
    return
  fi
  file="$migrations_dir/$version.down.sql"
  if [ ! -f "$file" ]; then
    echo "missing rollback migration: $file" >&2
    exit 1
  fi
  echo "rollback $version"
  psql "$database_url" -v ON_ERROR_STOP=1 \
    -c "BEGIN" \
    -f "$file" \
    -c "DELETE FROM public.schema_migrations WHERE version = '$version'" \
    -c "COMMIT"
}

case "$action" in
  up) apply_up ;;
  down) apply_down ;;
  *)
    echo "usage: migrate.sh [up|down]" >&2
    exit 2
    ;;
esac
