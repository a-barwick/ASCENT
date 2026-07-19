# ADR-0002: Organize future authority around mission-program boundaries

- Status: Accepted
- Date: 2026-07-19

## Context

The mission-program loop crosses mandates, budgets, suppliers, hardware,
integration, logistics, assurance, readiness, stakeholders, and mission
outcomes. A single decision can affect several of those concerns at once. The
current proof of concept is intentionally client-only, so no service boundary
needs to be implemented yet.

## Decision

When ASCENT needs an authoritative application, begin with one deployable
system divided into explicit mission-program modules. Expected boundaries
include:

- programs and mandates;
- procurement, suppliers, contracts, and scarce capacity;
- hardware, manufacturing, integration, and configuration control;
- logistics for components, crews, propellant, and mission assets;
- assurance for inspections, tests, anomalies, waivers, and investigations;
- readiness gates, sign-offs, commitment decisions, and outcomes;
- finance, stakeholders, rivals, alerts, and coordination.

Each module owns its state and exposes narrow use cases. Cross-module commands
are coordinated atomically inside the application. A module becomes an
independent service only when measured scale, reliability, security, or team
ownership requires it.

## Consequences

- Readiness and commitment workflows can remain consistent without distributed
  transactions.
- Domain language in code and storage follows the mission-program fantasy.
- Module contracts can be tested before any network boundary exists.
- Deployment and operations stay simple while the game loop is still evolving.
- The eventual implementation language and persistence technology remain open
  until authority is actually required.
