ALTER TABLE platform.command_log
  ADD COLUMN request_hash BYTEA,
  ADD COLUMN error_code TEXT,
  ADD COLUMN updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp();

UPDATE platform.command_log
SET request_hash = decode(
  md5(
    concat_ws(
      '|',
      command_id::text,
      protocol_version,
      actor_id,
      company_id::text,
      expected_version::text,
      command_type,
      payload::text
    )
  )
  || md5(
    'ascent-request-v1|'
    || concat_ws(
      '|',
      actor_id,
      company_id::text,
      command_type,
      idempotency_key,
      payload::text
    )
  ),
  'hex'
)
WHERE request_hash IS NULL;

UPDATE platform.command_log
SET
  result = COALESCE(result, '{}'::jsonb),
  committed_at = COALESCE(committed_at, received_at)
WHERE status = 'committed';

UPDATE platform.command_log
SET
  committed_at = NULL,
  error_code = COALESCE(NULLIF(error_code, ''), 'LEGACY_COMMAND_FAILURE')
WHERE status IN ('rejected', 'failed');

UPDATE platform.command_log
SET committed_at = NULL
WHERE status IN ('accepted', 'scheduled');

ALTER TABLE platform.command_log
  ALTER COLUMN request_hash SET NOT NULL,
  DROP CONSTRAINT command_log_commit_state_consistent,
  ADD CONSTRAINT command_log_request_hash_length
    CHECK (octet_length(request_hash) = 32),
  ADD CONSTRAINT command_log_idempotency_key_valid
    CHECK (
      btrim(idempotency_key) <> ''
      AND char_length(idempotency_key) <= 200
    ),
  ADD CONSTRAINT command_log_type_valid
    CHECK (btrim(command_type) <> '' AND char_length(command_type) <= 120),
  ADD CONSTRAINT command_log_result_state_consistent
    CHECK (
      (
        status = 'committed'
        AND committed_at IS NOT NULL
        AND committed_at >= received_at
        AND result IS NOT NULL
        AND error_code IS NULL
      )
      OR (
        status IN ('rejected', 'failed')
        AND committed_at IS NULL
        AND error_code IS NOT NULL
        AND btrim(error_code) <> ''
      )
      OR (
        status IN ('accepted', 'scheduled')
        AND committed_at IS NULL
        AND error_code IS NULL
      )
    );

CREATE OR REPLACE FUNCTION platform.enforce_command_log_update()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
  IF NEW.command_id IS DISTINCT FROM OLD.command_id
    OR NEW.protocol_version IS DISTINCT FROM OLD.protocol_version
    OR NEW.idempotency_key IS DISTINCT FROM OLD.idempotency_key
    OR NEW.command_type IS DISTINCT FROM OLD.command_type
    OR NEW.actor_id IS DISTINCT FROM OLD.actor_id
    OR NEW.company_id IS DISTINCT FROM OLD.company_id
    OR NEW.expected_version IS DISTINCT FROM OLD.expected_version
    OR NEW.payload IS DISTINCT FROM OLD.payload
    OR NEW.request_hash IS DISTINCT FROM OLD.request_hash
    OR NEW.received_at IS DISTINCT FROM OLD.received_at
    OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
    RAISE EXCEPTION 'command identity and request fields are immutable'
      USING ERRCODE = '55000';
  END IF;

  IF OLD.status IN ('rejected', 'committed', 'failed')
    AND (
      NEW.status IS DISTINCT FROM OLD.status
      OR NEW.result IS DISTINCT FROM OLD.result
      OR NEW.error_code IS DISTINCT FROM OLD.error_code
      OR NEW.committed_at IS DISTINCT FROM OLD.committed_at
    ) THEN
    RAISE EXCEPTION 'terminal command result is immutable'
      USING ERRCODE = '55000';
  END IF;

  IF NOT (
    NEW.status = OLD.status
    OR (OLD.status = 'accepted' AND NEW.status IN ('rejected', 'scheduled', 'committed', 'failed'))
    OR (OLD.status = 'scheduled' AND NEW.status IN ('committed', 'failed'))
  ) THEN
    RAISE EXCEPTION 'invalid command status transition: % -> %', OLD.status, NEW.status
      USING ERRCODE = '23514';
  END IF;

  NEW.updated_at := clock_timestamp();
  RETURN NEW;
END;
$$;

CREATE TRIGGER command_log_guard_update
BEFORE UPDATE ON platform.command_log
FOR EACH ROW
EXECUTE FUNCTION platform.enforce_command_log_update();

CREATE TABLE platform.event_global_sequence (
  singleton BOOLEAN PRIMARY KEY DEFAULT TRUE
    CHECK (singleton),
  last_sequence BIGINT NOT NULL DEFAULT 0
    CHECK (last_sequence >= 0),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);

INSERT INTO platform.event_global_sequence (singleton, last_sequence)
SELECT TRUE, COALESCE(max(sequence), 0)
FROM platform.event_outbox;

CREATE TABLE platform.event_topic_sequences (
  topic TEXT PRIMARY KEY
    CHECK (btrim(topic) <> '' AND char_length(topic) <= 240),
  last_sequence BIGINT NOT NULL DEFAULT 0
    CHECK (last_sequence >= 0),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);

ALTER TABLE platform.event_outbox
  ADD COLUMN topic_sequence BIGINT,
  ADD COLUMN causation_event_id UUID;

WITH ranked AS (
  SELECT
    sequence,
    row_number() OVER (PARTITION BY topic ORDER BY sequence) AS topic_sequence
  FROM platform.event_outbox
)
UPDATE platform.event_outbox AS event
SET topic_sequence = ranked.topic_sequence
FROM ranked
WHERE ranked.sequence = event.sequence;

INSERT INTO platform.event_topic_sequences (topic, last_sequence)
SELECT topic, max(topic_sequence)
FROM platform.event_outbox
GROUP BY topic
ON CONFLICT (topic) DO UPDATE
SET
  last_sequence = EXCLUDED.last_sequence,
  updated_at = clock_timestamp();

ALTER TABLE platform.event_outbox
  ALTER COLUMN topic_sequence SET NOT NULL,
  ADD CONSTRAINT event_outbox_topic_sequence_positive
    CHECK (topic_sequence > 0),
  ADD CONSTRAINT event_outbox_topic_sequence_unique
    UNIQUE (topic, topic_sequence),
  ADD CONSTRAINT event_outbox_causation_not_self
    CHECK (causation_event_id IS NULL OR causation_event_id <> event_id);

ALTER TABLE platform.event_outbox
  ADD CONSTRAINT event_outbox_causation_event_fk
  FOREIGN KEY (causation_event_id)
  REFERENCES platform.event_outbox (event_id)
  DEFERRABLE INITIALLY DEFERRED;

CREATE OR REPLACE FUNCTION platform.next_topic_sequence(target_topic TEXT)
RETURNS BIGINT
LANGUAGE plpgsql
AS $$
DECLARE
  allocated BIGINT;
BEGIN
  IF target_topic IS NULL OR btrim(target_topic) = '' THEN
    RAISE EXCEPTION 'event topic is required'
      USING ERRCODE = '23514';
  END IF;

  INSERT INTO platform.event_topic_sequences AS sequences (
    topic,
    last_sequence
  )
  VALUES (target_topic, 1)
  ON CONFLICT (topic) DO UPDATE
  SET
    last_sequence = sequences.last_sequence + 1,
    updated_at = clock_timestamp()
  RETURNING last_sequence INTO allocated;

  RETURN allocated;
END;
$$;

CREATE OR REPLACE FUNCTION platform.next_global_event_sequence()
RETURNS BIGINT
LANGUAGE plpgsql
AS $$
DECLARE
  allocated BIGINT;
BEGIN
  UPDATE platform.event_global_sequence
  SET
    last_sequence = last_sequence + 1,
    updated_at = clock_timestamp()
  WHERE singleton
  RETURNING last_sequence INTO allocated;

  IF allocated IS NULL THEN
    RAISE EXCEPTION 'global event sequence allocator is not initialized'
      USING ERRCODE = '55000';
  END IF;

  RETURN allocated;
END;
$$;

CREATE OR REPLACE FUNCTION platform.assign_global_event_sequence()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
  NEW.sequence := platform.next_global_event_sequence();
  RETURN NEW;
END;
$$;

CREATE TRIGGER event_outbox_assign_global_sequence
BEFORE INSERT ON platform.event_outbox
FOR EACH ROW
EXECUTE FUNCTION platform.assign_global_event_sequence();

CREATE OR REPLACE FUNCTION platform.assign_topic_sequence()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
  IF NEW.topic_sequence IS NULL THEN
    NEW.topic_sequence := platform.next_topic_sequence(NEW.topic);
  ELSE
    INSERT INTO platform.event_topic_sequences AS sequences (
      topic,
      last_sequence
    )
    VALUES (NEW.topic, NEW.topic_sequence)
    ON CONFLICT (topic) DO UPDATE
    SET
      last_sequence = GREATEST(sequences.last_sequence, EXCLUDED.last_sequence),
      updated_at = clock_timestamp();
  END IF;

  RETURN NEW;
END;
$$;

CREATE TRIGGER event_outbox_assign_topic_sequence
BEFORE INSERT ON platform.event_outbox
FOR EACH ROW
EXECUTE FUNCTION platform.assign_topic_sequence();

CREATE OR REPLACE FUNCTION platform.guard_event_outbox_update()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
  IF NEW.sequence IS DISTINCT FROM OLD.sequence
    OR NEW.event_id IS DISTINCT FROM OLD.event_id
    OR NEW.protocol_version IS DISTINCT FROM OLD.protocol_version
    OR NEW.command_id IS DISTINCT FROM OLD.command_id
    OR NEW.topic IS DISTINCT FROM OLD.topic
    OR NEW.topic_sequence IS DISTINCT FROM OLD.topic_sequence
    OR NEW.event_type IS DISTINCT FROM OLD.event_type
    OR NEW.occurred_at IS DISTINCT FROM OLD.occurred_at
    OR NEW.payload IS DISTINCT FROM OLD.payload
    OR NEW.causation_event_id IS DISTINCT FROM OLD.causation_event_id
    OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
    RAISE EXCEPTION 'committed event facts are immutable'
      USING ERRCODE = '55000';
  END IF;

  IF NEW.publish_attempts < OLD.publish_attempts THEN
    RAISE EXCEPTION 'event publish attempts cannot decrease'
      USING ERRCODE = '23514';
  END IF;

  IF OLD.published_at IS NOT NULL
    AND NEW.published_at IS DISTINCT FROM OLD.published_at THEN
    RAISE EXCEPTION 'published event timestamp is immutable'
      USING ERRCODE = '55000';
  END IF;

  RETURN NEW;
END;
$$;

CREATE TRIGGER event_outbox_guard_update
BEFORE UPDATE ON platform.event_outbox
FOR EACH ROW
EXECUTE FUNCTION platform.guard_event_outbox_update();

CREATE SCHEMA IF NOT EXISTS ledger;

CREATE TABLE ledger.accounts (
  account_id UUID PRIMARY KEY,
  company_id UUID NOT NULL,
  currency TEXT NOT NULL,
  code TEXT NOT NULL
    CHECK (btrim(code) <> '' AND char_length(code) <= 48),
  name TEXT NOT NULL
    CHECK (btrim(name) <> '' AND char_length(name) <= 120),
  category TEXT NOT NULL
    CHECK (category IN ('asset', 'liability', 'equity', 'revenue', 'expense')),
  status TEXT NOT NULL DEFAULT 'active'
    CHECK (status IN ('active', 'closed')),
  created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
  CONSTRAINT accounts_company_currency_fk
    FOREIGN KEY (company_id, currency)
    REFERENCES companies.companies (company_id, base_currency),
  CONSTRAINT accounts_company_code_unique
    UNIQUE (company_id, code),
  CONSTRAINT accounts_id_company_currency_unique
    UNIQUE (account_id, company_id, currency)
);

CREATE TABLE ledger.journals (
  journal_id UUID PRIMARY KEY,
  company_id UUID NOT NULL,
  currency TEXT NOT NULL,
  command_id UUID
    REFERENCES platform.command_log (command_id),
  source_type TEXT NOT NULL
    CHECK (
      source_type IN (
        'opening',
        'command',
        'trade',
        'production',
        'freight',
        'correction'
      )
    ),
  source_id UUID NOT NULL,
  description TEXT NOT NULL
    CHECK (btrim(description) <> '' AND char_length(description) <= 300),
  occurred_at TIMESTAMPTZ NOT NULL,
  posted_at TIMESTAMPTZ NOT NULL,
  reversal_of_journal_id UUID
    REFERENCES ledger.journals (journal_id)
    DEFERRABLE INITIALLY DEFERRED,
  created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
  CONSTRAINT journals_company_currency_fk
    FOREIGN KEY (company_id, currency)
    REFERENCES companies.companies (company_id, base_currency),
  CONSTRAINT journals_source_unique
    UNIQUE (company_id, source_type, source_id),
  CONSTRAINT journals_id_company_currency_unique
    UNIQUE (journal_id, company_id, currency),
  CONSTRAINT journals_posted_after_occurrence
    CHECK (posted_at >= occurred_at),
  CONSTRAINT journals_reversal_not_self
    CHECK (
      reversal_of_journal_id IS NULL
      OR reversal_of_journal_id <> journal_id
    )
);

CREATE UNIQUE INDEX journals_company_command_unique
  ON ledger.journals (company_id, command_id)
  WHERE command_id IS NOT NULL;

CREATE INDEX journals_company_occurred_idx
  ON ledger.journals (company_id, occurred_at DESC, journal_id);

CREATE TABLE ledger.entries (
  entry_id UUID PRIMARY KEY,
  journal_id UUID NOT NULL,
  account_id UUID NOT NULL,
  company_id UUID NOT NULL,
  currency TEXT NOT NULL,
  side TEXT NOT NULL
    CHECK (side IN ('debit', 'credit')),
  amount_minor BIGINT NOT NULL
    CHECK (amount_minor > 0),
  memo TEXT
    CHECK (memo IS NULL OR char_length(memo) <= 300),
  created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
  CONSTRAINT entries_journal_company_currency_fk
    FOREIGN KEY (journal_id, company_id, currency)
    REFERENCES ledger.journals (journal_id, company_id, currency),
  CONSTRAINT entries_account_company_currency_fk
    FOREIGN KEY (account_id, company_id, currency)
    REFERENCES ledger.accounts (account_id, company_id, currency)
);

CREATE INDEX entries_account_history_idx
  ON ledger.entries (account_id, created_at, entry_id);

CREATE OR REPLACE FUNCTION ledger.assert_journal_balanced()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
  target_journal_id UUID;
  entry_count BIGINT;
  debit_total NUMERIC;
  credit_total NUMERIC;
BEGIN
  target_journal_id := COALESCE(NEW.journal_id, OLD.journal_id);

  IF NOT EXISTS (
    SELECT 1
    FROM ledger.journals
    WHERE journal_id = target_journal_id
  ) THEN
    RETURN NULL;
  END IF;

  SELECT
    count(*),
    COALESCE(sum(amount_minor) FILTER (WHERE side = 'debit'), 0),
    COALESCE(sum(amount_minor) FILTER (WHERE side = 'credit'), 0)
  INTO entry_count, debit_total, credit_total
  FROM ledger.entries
  WHERE journal_id = target_journal_id;

  IF entry_count < 2 OR debit_total <> credit_total THEN
    RAISE EXCEPTION
      'journal % is not balanced: entries %, debit %, credit %',
      target_journal_id,
      entry_count,
      debit_total,
      credit_total
      USING ERRCODE = '23514';
  END IF;

  RETURN NULL;
END;
$$;

CREATE CONSTRAINT TRIGGER journals_require_balanced_entries
AFTER INSERT OR UPDATE ON ledger.journals
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW
EXECUTE FUNCTION ledger.assert_journal_balanced();

CREATE CONSTRAINT TRIGGER entries_preserve_journal_balance
AFTER INSERT OR UPDATE OR DELETE ON ledger.entries
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW
EXECUTE FUNCTION ledger.assert_journal_balanced();

CREATE OR REPLACE FUNCTION ledger.reject_posted_record_change()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
  RAISE EXCEPTION 'posted ledger records are immutable'
    USING ERRCODE = '55000';
END;
$$;

CREATE TRIGGER journals_are_immutable
BEFORE UPDATE OR DELETE ON ledger.journals
FOR EACH ROW
EXECUTE FUNCTION ledger.reject_posted_record_change();

CREATE TRIGGER entries_are_immutable
BEFORE UPDATE OR DELETE ON ledger.entries
FOR EACH ROW
EXECUTE FUNCTION ledger.reject_posted_record_change();

COMMENT ON SCHEMA ledger IS
  'Immutable double-entry company journals in integer currency minor units.';

COMMENT ON COLUMN ledger.entries.amount_minor IS
  'Positive integer amount in the journal currency minor unit; side carries the sign.';
