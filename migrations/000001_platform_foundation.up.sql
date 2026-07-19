CREATE SCHEMA IF NOT EXISTS platform;

CREATE TABLE platform.command_log (
  command_id UUID PRIMARY KEY,
  protocol_version TEXT NOT NULL,
  idempotency_key TEXT NOT NULL,
  command_type TEXT NOT NULL,
  actor_id TEXT NOT NULL,
  company_id UUID,
  expected_version BIGINT,
  status TEXT NOT NULL
    CHECK (status IN ('accepted', 'rejected', 'scheduled', 'committed', 'failed')),
  payload JSONB NOT NULL,
  result JSONB,
  received_at TIMESTAMPTZ NOT NULL,
  committed_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
  CONSTRAINT command_log_actor_idempotency_unique
    UNIQUE (actor_id, idempotency_key),
  CONSTRAINT command_log_commit_state_consistent
    CHECK (
      (status = 'committed' AND committed_at IS NOT NULL)
      OR (status <> 'committed')
    )
);

CREATE INDEX command_log_company_received_idx
  ON platform.command_log (company_id, received_at DESC)
  WHERE company_id IS NOT NULL;

CREATE TABLE platform.event_outbox (
  sequence BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  event_id UUID NOT NULL UNIQUE,
  protocol_version TEXT NOT NULL,
  command_id UUID REFERENCES platform.command_log (command_id),
  topic TEXT NOT NULL,
  event_type TEXT NOT NULL,
  occurred_at TIMESTAMPTZ NOT NULL,
  payload JSONB NOT NULL,
  published_at TIMESTAMPTZ,
  publish_attempts INTEGER NOT NULL DEFAULT 0
    CHECK (publish_attempts >= 0),
  created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);

CREATE INDEX event_outbox_unpublished_idx
  ON platform.event_outbox (sequence)
  WHERE published_at IS NULL;

COMMENT ON SCHEMA platform IS
  'Cross-domain command, idempotency, and committed-event infrastructure.';

COMMENT ON TABLE platform.command_log IS
  'Immutable command identity and visible result state; domain effects remain in owning tables.';

COMMENT ON TABLE platform.event_outbox IS
  'Ordered facts written in the same transaction as source state and published after commit.';
