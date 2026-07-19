# ADR-0001: PostgreSQL is the economic authority

- Status: Accepted
- Date: 2026-07-18

## Context

ASCENT requires atomic settlement, double-entry accounting, idempotency,
traceability, and recoverability. Those guarantees must survive client retries,
consumer restarts, and concurrent market activity.

## Decision

PostgreSQL owns committed economic truth. Every economic mutation executes in a
database transaction with constraints and immutable source references. Events
are published from committed facts and are never a substitute for the
transactional record.

## Consequences

- Browser balances and projections are disposable.
- Caches and event systems may improve delivery but never become authoritative.
- Corrections use compensating records instead of hidden row edits.
- Migration and recovery procedures are part of gameplay correctness.
