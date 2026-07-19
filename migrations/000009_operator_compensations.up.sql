CREATE SCHEMA IF NOT EXISTS operator_admin;

CREATE TABLE operator_admin.grants (
  player_id UUID NOT NULL
    REFERENCES identity.players (player_id),
  role TEXT NOT NULL
    CHECK (role IN ('auditor', 'operator', 'administrator')),
  granted_by_player_id UUID
    REFERENCES identity.players (player_id),
  granted_at TIMESTAMPTZ NOT NULL,
  revoked_at TIMESTAMPTZ,
  reason TEXT NOT NULL
    CHECK (btrim(reason) <> '' AND char_length(reason) <= 300),
  PRIMARY KEY (player_id, role),
  CONSTRAINT operator_grants_revocation_after_grant
    CHECK (revoked_at IS NULL OR revoked_at >= granted_at)
);

CREATE TABLE platform.command_relations (
  command_id UUID NOT NULL
    REFERENCES platform.command_log (command_id),
  related_command_id UUID NOT NULL
    REFERENCES platform.command_log (command_id),
  relation_type TEXT NOT NULL
    CHECK (relation_type IN ('compensates', 'caused_by', 'retry_of')),
  created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
  PRIMARY KEY (command_id, related_command_id, relation_type),
  CONSTRAINT command_relations_not_self
    CHECK (command_id <> related_command_id)
);

CREATE INDEX command_relations_related_idx
  ON platform.command_relations (related_command_id, relation_type, command_id);

CREATE TABLE operator_admin.compensations (
  compensation_id UUID PRIMARY KEY,
  command_id UUID NOT NULL
    REFERENCES platform.command_log (command_id),
  original_command_id UUID NOT NULL
    REFERENCES platform.command_log (command_id),
  operator_player_id UUID NOT NULL
    REFERENCES identity.players (player_id),
  reason TEXT NOT NULL
    CHECK (btrim(reason) <> '' AND char_length(reason) <= 1000),
  status TEXT NOT NULL DEFAULT 'pending'
    CHECK (status IN ('pending', 'committed', 'failed')),
  requested_at TIMESTAMPTZ NOT NULL,
  committed_at TIMESTAMPTZ,
  failure_code TEXT
    CHECK (failure_code IS NULL OR btrim(failure_code) <> ''),
  event_id UUID
    REFERENCES platform.event_outbox (event_id),
  created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
  CONSTRAINT operator_compensations_command_unique UNIQUE (command_id),
  CONSTRAINT operator_compensations_event_unique UNIQUE (event_id),
  CONSTRAINT operator_compensations_distinct_commands
    CHECK (command_id <> original_command_id),
  CONSTRAINT operator_compensations_state_consistent
    CHECK (
      (
        status = 'pending'
        AND committed_at IS NULL
        AND failure_code IS NULL
        AND event_id IS NULL
      )
      OR (
        status = 'committed'
        AND committed_at IS NOT NULL
        AND committed_at >= requested_at
        AND failure_code IS NULL
        AND event_id IS NOT NULL
      )
      OR (
        status = 'failed'
        AND committed_at IS NULL
        AND failure_code IS NOT NULL
        AND event_id IS NULL
      )
    )
);

CREATE TABLE operator_admin.compensation_journals (
  compensation_id UUID NOT NULL
    REFERENCES operator_admin.compensations (compensation_id),
  journal_id UUID NOT NULL
    REFERENCES ledger.journals (journal_id),
  PRIMARY KEY (compensation_id, journal_id)
);

CREATE TABLE operator_admin.compensation_inventory_movements (
  compensation_id UUID NOT NULL
    REFERENCES operator_admin.compensations (compensation_id),
  movement_id UUID NOT NULL
    REFERENCES inventory.movements (movement_id),
  PRIMARY KEY (compensation_id, movement_id)
);

CREATE OR REPLACE FUNCTION operator_admin.assert_compensation_contract()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
  target_compensation_id UUID;
  compensation_status TEXT;
  compensation_command_id UUID;
  compensation_original_command_id UUID;
  effect_count BIGINT;
BEGIN
  target_compensation_id := COALESCE(NEW.compensation_id, OLD.compensation_id);

  SELECT status, command_id, original_command_id
  INTO
    compensation_status,
    compensation_command_id,
    compensation_original_command_id
  FROM operator_admin.compensations
  WHERE compensation_id = target_compensation_id;

  IF NOT FOUND THEN
    RETURN NULL;
  END IF;

  IF NOT EXISTS (
    SELECT 1
    FROM platform.command_relations
    WHERE command_id = compensation_command_id
      AND related_command_id = compensation_original_command_id
      AND relation_type = 'compensates'
  ) THEN
    RAISE EXCEPTION
      'operator compensation % must relate its command to the original',
      target_compensation_id
      USING ERRCODE = '23514';
  END IF;

  IF EXISTS (
    SELECT 1
    FROM operator_admin.compensation_journals AS link
    JOIN ledger.journals AS journal
      ON journal.journal_id = link.journal_id
    WHERE link.compensation_id = target_compensation_id
      AND (
        journal.source_type <> 'correction'
        OR journal.source_id <> target_compensation_id
      )
  ) THEN
    RAISE EXCEPTION
      'operator compensation % has an incompatible journal',
      target_compensation_id
      USING ERRCODE = '23514';
  END IF;

  IF EXISTS (
    SELECT 1
    FROM operator_admin.compensation_inventory_movements AS link
    JOIN inventory.movements AS movement
      ON movement.movement_id = link.movement_id
    WHERE link.compensation_id = target_compensation_id
      AND (
        movement.movement_kind <> 'correction'
        OR movement.source_id <> target_compensation_id
      )
  ) THEN
    RAISE EXCEPTION
      'operator compensation % has an incompatible inventory movement',
      target_compensation_id
      USING ERRCODE = '23514';
  END IF;

  SELECT
    (
      SELECT count(*)
      FROM operator_admin.compensation_journals
      WHERE compensation_id = target_compensation_id
    )
    +
    (
      SELECT count(*)
      FROM operator_admin.compensation_inventory_movements
      WHERE compensation_id = target_compensation_id
    )
  INTO effect_count;

  IF compensation_status = 'committed' AND effect_count = 0 THEN
    RAISE EXCEPTION
      'committed operator compensation % must have a compensating effect',
      target_compensation_id
      USING ERRCODE = '23514';
  END IF;

  RETURN NULL;
END;
$$;

CREATE CONSTRAINT TRIGGER operator_compensations_require_effects
AFTER INSERT OR UPDATE ON operator_admin.compensations
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW
EXECUTE FUNCTION operator_admin.assert_compensation_contract();

CREATE CONSTRAINT TRIGGER operator_compensation_journals_match
AFTER INSERT OR UPDATE OR DELETE ON operator_admin.compensation_journals
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW
EXECUTE FUNCTION operator_admin.assert_compensation_contract();

CREATE CONSTRAINT TRIGGER operator_compensation_movements_match
AFTER INSERT OR UPDATE OR DELETE ON operator_admin.compensation_inventory_movements
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW
EXECUTE FUNCTION operator_admin.assert_compensation_contract();

CREATE OR REPLACE FUNCTION operator_admin.guard_compensation_update()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
  IF NEW.compensation_id IS DISTINCT FROM OLD.compensation_id
    OR NEW.command_id IS DISTINCT FROM OLD.command_id
    OR NEW.original_command_id IS DISTINCT FROM OLD.original_command_id
    OR NEW.operator_player_id IS DISTINCT FROM OLD.operator_player_id
    OR NEW.reason IS DISTINCT FROM OLD.reason
    OR NEW.requested_at IS DISTINCT FROM OLD.requested_at
    OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
    RAISE EXCEPTION 'operator compensation request is immutable'
      USING ERRCODE = '55000';
  END IF;

  IF OLD.status IN ('committed', 'failed') THEN
    RAISE EXCEPTION 'terminal operator compensation is immutable'
      USING ERRCODE = '55000';
  END IF;

  IF OLD.status <> 'pending' OR NEW.status NOT IN ('committed', 'failed') THEN
    RAISE EXCEPTION 'invalid operator compensation status transition'
      USING ERRCODE = '23514';
  END IF;

  RETURN NEW;
END;
$$;

CREATE TRIGGER operator_compensations_guard_update
BEFORE UPDATE ON operator_admin.compensations
FOR EACH ROW
EXECUTE FUNCTION operator_admin.guard_compensation_update();

COMMENT ON SCHEMA operator_admin IS
  'Elevated grants and auditable compensating corrections; direct balance edits are not represented.';
