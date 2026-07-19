SHELL := /bin/sh

.DEFAULT_GOAL := help

.PHONY: help bootstrap dev dev-server dev-web generate generate-check fixtures \
	format format-check typecheck test build check db-up db-down db-logs db-migrate \
	db-rollback db-migration-test db-seed integration-test clean

help:
	@awk 'BEGIN {FS = ":.*## "}; /^[a-zA-Z_-]+:.*## / {printf "%-22s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

bootstrap: ## Install workspace dependencies.
	pnpm install --frozen-lockfile

dev: db-migrate ## Run PostgreSQL, the seeded Go API, and SvelteKit app together.
	./scripts/dev.sh

dev-server: ## Run the Go API on ASCENT_HTTP_ADDR (default :8080).
	go run ./apps/server serve

dev-web: ## Run the SvelteKit development server.
	pnpm --filter @ascent/web dev

generate: ## Regenerate protocol bindings from the source schema.
	go run ./protocol/cmd/protocolgen

generate-check: ## Fail when generated protocol bindings are stale.
	go run ./protocol/cmd/protocolgen --check

fixtures: ## Regenerate deterministic development fixtures.
	go run ./apps/server seed --output fixtures/market.json

format: ## Format Go, Svelte, TypeScript, JSON, CSS, Markdown, and YAML.
	gofmt -w apps internal protocol/cmd protocol/gen
	pnpm format

format-check: ## Check formatting without changing files.
	./scripts/check-go-format.sh
	pnpm format:check

typecheck: ## Check Svelte and TypeScript contracts.
	pnpm check

test: ## Run unit and protocol tests.
	go test ./...
	pnpm test

build: ## Build the server and web application.
	go build ./...
	pnpm build

check: generate-check format-check typecheck test build ## Run the local equivalent of CI.

db-up: ## Start local PostgreSQL and wait until it is healthy.
	./scripts/compose.sh up -d --wait postgres

db-down: ## Stop local infrastructure.
	./scripts/compose.sh down

db-logs: ## Follow PostgreSQL logs.
	./scripts/compose.sh logs -f postgres

db-migrate: db-up ## Apply pending PostgreSQL migrations.
	./scripts/compose.sh --profile tools run --rm migrate up

db-rollback: db-up ## Roll back the most recently applied migration.
	./scripts/compose.sh --profile tools run --rm migrate down

db-migration-test: ## Exercise migrations up, down, and up in an isolated database.
	./scripts/test-migrations.sh

db-seed: db-migrate ## Install or reconcile the deterministic first-playable scenario.
	go run ./apps/server seed-db

integration-test: ## Run first-playable acceptance tests in an isolated PostgreSQL database.
	./scripts/test-integration.sh

clean: ## Remove generated local build output.
	rm -rf apps/web/.svelte-kit apps/web/build bin coverage
