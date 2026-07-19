# Development guide

## Current architecture

ASCENT is currently a UI-only SvelteKit proof of concept. The ORPHEUS-1 scenario,
its transition rules, and its outcome model all run in the browser. There is no
application server, database, authentication flow, external service, or
persistent state in the current MVP.

That boundary is intentional. The present work is validating the player fantasy,
information hierarchy, and commitment loop before introducing platform
infrastructure.

## Toolchain

- Node 24
- pnpm 11
- Svelte 5 and SvelteKit
- TypeScript and Vitest

Install the workspace dependencies once:

```sh
pnpm install --frozen-lockfile
```

## Run locally

Start the web application:

```sh
pnpm --filter @ascent/web dev
```

Open `http://localhost:5173`. The scenario begins at the ORPHEUS-1 readiness
gate. Reloading the page or using the replay control creates a fresh local
scenario.

## Validate a change

Run the web checks before review:

```sh
pnpm --filter @ascent/web test
pnpm --filter @ascent/web check
pnpm --filter @ascent/web build
pnpm format:check
```

The tests cover the decision transitions, commitment outcomes, and countdown
expiry. The release build is the final check that the Svelte application can
be packaged without development-only assumptions.

## Code map

- `apps/web/src/routes/+page.svelte` composes the mission-control workspace and
  owns the local countdown.
- `apps/web/src/lib/domain/program.ts` defines the ORPHEUS-1 state, evidence,
  choices, readiness effects, sign-offs, outcomes, and pure transition
  functions.
- `apps/web/src/lib/domain/program.test.ts` verifies the consequential decision
  paths and commitment rules.
- `apps/web/src/lib/components/program/` contains the program-specific decision,
  phase, readiness, and risk views.
- `apps/web/src/lib/components/ui/` contains small reusable presentation
  primitives.
- `apps/web/src/lib/styles/` contains the design tokens and responsive
  mission-control layout.

## Scenario state model

`createInitialProgramState()` is the single starting point for a playthrough.
The page then applies a short sequence of one-way transitions:

1. `advanceProgramClock()` reduces the remaining launch window and creates a
   stand-down outcome if the corridor closes.
2. `applyDecision()` records one anomaly disposition and updates contingency,
   committed spend, mission risk, confidence, readiness, sign-offs, rival
   pressure, and the event trail.
3. `resolveCommitment()` accepts a final `go` or `scrub` directive. A GO compares
   an uncertainty draw with the risk the player shaped; a SCRUB preserves the
   vehicle while conceding the current opportunity.

The transition functions return new state rather than mutating their input.
They also reject invalid repeats, such as a second disposition or a commitment
before the active hold is resolved. Keep consequential rules in this domain
module so the UI displays results instead of inventing them.

The optional uncertainty value accepted by `resolveCommitment()` is deliberate.
Tests pass a fixed value for deterministic assertions; the interactive scenario
uses a browser-generated draw.

## Interaction rules

- Show the evidence and the material cost, schedule, risk, confidence, and rival
  effects before authorization.
- Treat authorization as a locked, accountable decision.
- Do not enable GO or SCRUB until the active readiness hold has a disposition.
- Preserve objections and waivers in the visible sign-off and event record.
- Make SCRUB a legitimate strategic choice, not a failure-state button.
- Keep countdown pressure legible without hiding the consequences of an action.
- Do not add network calls or implied persistence to a control that only changes
  local state.

## Responsive and accessible UI

The desktop view may remain dense, but the hierarchy must survive at tablet and
phone widths. Prefer semantic sections, lists, buttons, fieldsets, and status
text over interaction that depends on color or pointer hover. Program state,
evidence, and commitment controls must remain usable from the keyboard.

Shared tokens belong in `tokens.css`; cross-panel shell behavior belongs in
`program-control.css`; route-specific layout should stay close to the route until a
second scenario proves that it is reusable.

## Extending the proof of concept

The next product slice should make earlier campaign phases playable: mandate
selection, planning, supplier commitments, testing strategy, manufacturing and
integration decisions, and the causal buildup to a readiness anomaly. Keep that
work local and scenario-driven while the loop is still being discovered.

Persistence, accounts, multiplayer coordination, and an authoritative service
are later platform boundaries. Add them only when a validated campaign requires
them, and update this guide when they become real.
