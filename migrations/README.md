# PostgreSQL migrations

Migrations are paired, ordered SQL files. The migration harness records applied
versions in `public.schema_migrations`. Deployment automation must run one
migration job at a time.

```sh
make db-migrate
make db-rollback
make db-migration-test
```

The baseline creates only cross-domain platform infrastructure:

- `platform.command_log` for command identity, idempotency, and result state;
- `platform.event_outbox` for ordered committed facts.

Economic domain tables begin in their owning work packages. Never use a down
migration or ad hoc update to erase committed economic history; operator
corrections use compensating transactions.

## First-playable schema

The first-playable migrations are ordered by dependency:

| Version  | Boundary                                                         |
| -------- | ---------------------------------------------------------------- |
| `000002` | External identity subjects and hashed, revocable sessions        |
| `000003` | Companies and explicit player memberships                        |
| `000004` | Command/outbox hardening and immutable double-entry accounting   |
| `000005` | Locations, commodities, holdings, and inventory movement history |
| `000006` | Price-time-priority orders, reservations, and settled trades     |
| `000007` | Versioned production jobs and simple freight contracts           |
| `000008` | Devices, views, panel delivery, chat, moderation, and alerts     |
| `000009` | Operator grants and auditable compensating corrections           |

Application code supplies UUIDs. Currency values are signed-safe `BIGINT`
minor-unit amounts; commodity quantities are `BIGINT` values interpreted using
`inventory.commodities.quantity_scale`. API payloads should serialize these
integers as decimal strings when JavaScript precision could be exceeded.

Each market declares `lot_size_minor`; order and trade quantities must be exact
multiples of that fixed-scale quantity. For an active buy order, base cash
exposure in currency minor units is:

```text
limit_price_minor × remaining_quantity_minor
─────────────────────────────────────────────
          10 ^ commodity.quantity_scale
```

The numerator is evaluated as arbitrary-precision `NUMERIC`, must divide
exactly, and the active cash reservation must be at least that exposure. A
larger reservation may cover fees. Active sell reservations continue to equal
the remaining commodity quantity exactly.

The database enforces the most important commit-time invariants:

- every posted journal has at least two entries and balances by company and
  currency;
- journal entries, inventory movements, trades, and committed event facts are
  immutable;
- order fills equal their immutable trade history;
- active sell reservations equal both the order remainder and the holding's
  reserved quantity;
- production executions are unique by facility, due time, and rule version;
- delivered freight quantity agrees with delivery history;
- operator corrections relate to the original command and contain only
  compensating journals or inventory movements.

Deferred constraints mean an economic command must update all related rows in
one PostgreSQL transaction. For example, settlement updates the order,
reservation, and holding, inserts the trade, inventory movement, balanced
journals, and outbox event, then commits once.

`platform.command_log.request_hash` is a required 32-byte canonical request
digest. Reusing `(actor_id, idempotency_key)` returns the stored result only
when this digest matches; a different digest is an idempotency conflict.

`platform.event_outbox.sequence` remains the global outbox cursor. It is
allocated by a singleton row lock so concurrent writers cannot commit a later
event below an already-visible after-cursor. `topic_sequence` is allocated
transactionally per topic by
`platform.next_topic_sequence(text)` and is the topic-local gap-detection
cursor exposed by protocol 1.1.

The migration test applies every migration, rolls the latest migration down and
up, then rolls the complete stack down in reverse order and reapplies it.
