# ADR-0002: Begin as a Go modular monolith

- Status: Accepted
- Date: 2026-07-18

## Context

The economic kernel crosses companies, permissions, ledger, inventory, markets,
production, freight, and contracts. Premature network boundaries would make
atomic changes and local reasoning harder before scaling needs are known.

## Decision

Implement one Go application with domain modules under `internal/`. Modules own
their data and expose narrow public contracts. Split a service only when
measured scaling, reliability, or team ownership demands an independent
boundary.

## Consequences

- Cross-module economic work can share one database transaction.
- Process deployment stays simple during the first playable economy.
- Module boundaries remain explicit and are tested without assuming future
  services.
- NATS may be introduced behind a committed-event interface when durable,
  multi-instance fanout is required.
