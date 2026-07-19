# ADR-0003: Use Svelte primitives first

- Status: Accepted
- Date: 2026-07-18

## Context

ASCENT's terminal is dense, keyboard-oriented, and device-specific. Generic card
systems and heavy data grids can obscure semantics and lock the product into
patterns before interaction requirements are proven.

## Decision

Use Svelte 5, SvelteKit, semantic HTML, and plain CSS design tokens. Domain state
lives in small `.svelte.ts` modules. Add charting, virtualization, or component
dependencies only when a named requirement cannot be met cleanly with these
primitives.

## Consequences

- Desktop, tablet, and phone information architectures may diverge over shared
  data contracts.
- Tables remain semantic and accessible until measured scale proves otherwise.
- Raw numeric values stay separate from formatted display strings.
- Dependencies must document the missing capability they provide.
