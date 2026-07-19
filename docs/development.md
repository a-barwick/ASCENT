# Development guide

## Toolchain

- Go 1.26
- Node 24
- pnpm 11
- PostgreSQL 17 through Docker Compose or a compatible standalone Compose CLI

Run `make bootstrap` once, then `make check` before opening a change for review.

## Local processes

`make dev` runs the Go API and SvelteKit development server in one terminal and
shuts both down together. It also starts PostgreSQL, applies pending migrations,
and enables the deterministic signed development session and seed scenario.
Run either process independently with `make dev-server` or `make dev-web`; when
running the server directly, configure the explicit variables in `.env.example`.

The local commands select `docker compose` when Docker Desktop is installed and
fall back to `docker-compose` for compatible runtimes such as OrbStack. If
neither command is available, install a Compose-compatible container runtime
before running database-backed targets.

The first playable exposes:

- `GET /healthz`
- `GET /api/v1/system`
- `GET /api/v1/fixtures/market`
- `POST /api/v1/dev/session`
- `GET /api/v1/game`
- `POST /api/v1/commands`
- `GET /api/v1/events?after=<sequence>`

The development session is HMAC signed, short-lived, and disabled unless
`ASCENT_DEV_IDENTITY=1`. Company authority is still resolved from PostgreSQL for
every command; the session token never carries economic permissions.

## Protocol workflow

`protocol/schema/envelopes.schema.json` is the only hand-edited envelope source.

```sh
make generate
make generate-check
```

The generator creates a Go package under `protocol/gen/go` and a TypeScript
workspace package under `protocol/gen/ts`. CI fails if either is stale.

## Database workflow

Create paired migration files:

```text
migrations/000002_description.up.sql
migrations/000002_description.down.sql
```

Apply normal development migrations with `make db-migrate`. Use
`make db-migration-test` before review; it creates an isolated database, applies
all migrations, rolls back the latest migration, performs a full reverse
rollback/reapply, and removes the test database.

Use `make db-seed` to reconcile the deterministic 50-company scenario without
starting the web processes. Use `make integration-test` for clean
PostgreSQL-backed acceptance testing; it never mutates the normal `ascent`
development database.

Production corrections to committed economic state must use compensating
transactions. Migration down files are for schema recovery and local/CI
verification, not for erasing economic history.

## Fixtures

`make fixtures` regenerates `fixtures/market.json` from deterministic Go source.
Do not hand-edit generated fixtures. Tests verify byte-for-byte repeatability.

The JSON fixture is a read-only degraded UI fallback. The playable scenario is
seeded relationally by `go run ./apps/server seed-db`, and repeated seeding is
idempotent.

## Economic command workflow

Commands use the generated protocol envelope and a raw UUID `commandId`.
Monetary values are exact integer minor units inside Go/PostgreSQL; decimal UI
values are parsed without floating-point rounding. An actor-scoped
`idempotencyKey` returns the original semantic result on an identical retry and
rejects changed reuse.

Committed effects, command results, and outbox facts share one serializable
transaction. Rejections are recorded only after rolling back staged domain
effects. Global and per-topic event sequences support snapshot-plus-event
recovery; process-local wakeups are disposable.
