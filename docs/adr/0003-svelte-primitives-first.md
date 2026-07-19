# ADR-0003: Build the mission-control workspace with Svelte primitives

- Status: Accepted
- Date: 2026-07-19

## Context

ASCENT's mission-control workspace must make time pressure, incomplete evidence,
readiness, risk, and accountability legible at a glance. Generic card systems
and heavy dashboards can flatten those relationships before the interaction
model has been proven.

## Decision

Use Svelte 5, SvelteKit, semantic HTML, and plain CSS design tokens. Domain state
lives in small TypeScript modules, while focused Svelte components render the
phase rail, evidence, readiness state, decision controls, and consequences. Add
charting, visualization, virtualization, or component dependencies only when a
named requirement cannot be met cleanly with these primitives.

## Consequences

- Desktop, tablet, and phone layouts may diverge while sharing the same program
  state and decisions.
- Evidence, controls, and status remain semantic and keyboard accessible.
- Raw time, cost, risk, and readiness values stay separate from display
  formatting.
- Gameplay transitions remain testable without rendering the workspace.
- New dependencies must name the interaction or scale problem they solve.
