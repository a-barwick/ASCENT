# ADR-0001: Keep MVP program state in the UI

- Status: Accepted
- Date: 2026-07-19

## Context

The current ASCENT proof of concept exists to validate the mission-program
fantasy: investigate incomplete evidence, choose how to resolve risk, pass a
readiness gate, and make a consequential commitment decision. Fast iteration on
that loop matters more than persistence, multiplayer coordination, or service
infrastructure at this stage.

The MVP state is small, explicit, and fully represented by the program domain
model in the web application. A page reload may reset the scenario without
violating the proof of concept.

## Decision

The web application owns the MVP's program state and decision transitions. The
scenario initializes locally, player actions produce new local state, and tests
may inject outcome rolls so the consequences remain deterministic under test.

ASCENT will add an authoritative service only when the game requires durable
saves, shared sessions, competitive interaction, or trusted outcomes. That
future authority must preserve an accountable record of mandates, supplier and
capacity choices, inspections, tests, anomalies, waivers, readiness sign-offs,
commitment directives, and mission outcomes. User-facing projections may be
rebuilt from that record.

## Consequences

- The proof of concept has no server, database, session, or transport contract.
- Gameplay changes can be evaluated without coordinating schema or API work.
- Reloading the page resets progress, and multiplayer is out of scope.
- A future authoritative implementation starts from the validated program
  model and introduces persistence as a deliberate architectural change.
- Consequential decisions must remain explainable from recorded evidence and
  player choices when authority is introduced.
