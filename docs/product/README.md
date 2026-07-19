# Product principles

ASCENT is a persistent multiplayer economic simulation of off-world industry.
The public repository carries the implementation principles that shape each
release:

- economic correctness first, useful terminal second, breadth third;
- PostgreSQL is authoritative and Go validates every command;
- the client is a disposable projection over snapshots and ordered events;
- begin with a modular monolith and Svelte primitives;
- every economic mutation is balanced, idempotent, authorized, auditable, and
  safe on rollback;
- deterministic fixtures let frontend and backend work proceed independently;
- work is delivered as small, testable vertical slices with explicit public
  contracts and recovery behavior.

The specification's immediate build instruction is represented by the
foundation work packages in `docs/agent-tasks/`.
