SHELL := /bin/sh

.DEFAULT_GOAL := help

.PHONY: help bootstrap dev format format-check typecheck test build check clean

help:
	@awk 'BEGIN {FS = ":.*## "}; /^[a-zA-Z_-]+:.*## / {printf "%-22s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

bootstrap: ## Install workspace dependencies.
	pnpm install --frozen-lockfile

dev: ## Run the SvelteKit development server.
	pnpm --filter @ascent/web dev

format: ## Format Svelte, TypeScript, JSON, CSS, Markdown, and YAML.
	pnpm format

format-check: ## Check formatting without changing files.
	pnpm format:check

typecheck: ## Check Svelte and TypeScript contracts.
	pnpm check

test: ## Run unit tests.
	pnpm test

build: ## Build the web application.
	pnpm build

check: format-check typecheck test build ## Run the local equivalent of CI.

clean: ## Remove generated local build output.
	rm -rf apps/web/.svelte-kit apps/web/build apps/web/coverage
