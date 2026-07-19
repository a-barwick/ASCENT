DROP TRIGGER IF EXISTS entries_are_immutable ON ledger.entries;
DROP TRIGGER IF EXISTS journals_are_immutable ON ledger.journals;
DROP TRIGGER IF EXISTS entries_preserve_journal_balance ON ledger.entries;
DROP TRIGGER IF EXISTS journals_require_balanced_entries ON ledger.journals;
DROP FUNCTION IF EXISTS ledger.reject_posted_record_change();
DROP FUNCTION IF EXISTS ledger.assert_journal_balanced();
DROP TABLE IF EXISTS ledger.entries;
DROP TABLE IF EXISTS ledger.journals;
DROP TABLE IF EXISTS ledger.accounts;
DROP SCHEMA IF EXISTS ledger;

DROP TRIGGER IF EXISTS event_outbox_guard_update ON platform.event_outbox;
DROP TRIGGER IF EXISTS event_outbox_assign_topic_sequence ON platform.event_outbox;
DROP TRIGGER IF EXISTS event_outbox_assign_global_sequence ON platform.event_outbox;
DROP FUNCTION IF EXISTS platform.guard_event_outbox_update();
DROP FUNCTION IF EXISTS platform.assign_topic_sequence();
DROP FUNCTION IF EXISTS platform.assign_global_event_sequence();
DROP FUNCTION IF EXISTS platform.next_global_event_sequence();
DROP FUNCTION IF EXISTS platform.next_topic_sequence(TEXT);

ALTER TABLE platform.event_outbox
  DROP CONSTRAINT IF EXISTS event_outbox_causation_event_fk,
  DROP CONSTRAINT IF EXISTS event_outbox_causation_not_self,
  DROP CONSTRAINT IF EXISTS event_outbox_topic_sequence_unique,
  DROP CONSTRAINT IF EXISTS event_outbox_topic_sequence_positive,
  DROP COLUMN IF EXISTS causation_event_id,
  DROP COLUMN IF EXISTS topic_sequence;

DROP TABLE IF EXISTS platform.event_topic_sequences;
DROP TABLE IF EXISTS platform.event_global_sequence;

DROP TRIGGER IF EXISTS command_log_guard_update ON platform.command_log;
DROP FUNCTION IF EXISTS platform.enforce_command_log_update();

ALTER TABLE platform.command_log
  DROP CONSTRAINT IF EXISTS command_log_result_state_consistent,
  DROP CONSTRAINT IF EXISTS command_log_type_valid,
  DROP CONSTRAINT IF EXISTS command_log_idempotency_key_valid,
  DROP CONSTRAINT IF EXISTS command_log_request_hash_length,
  DROP COLUMN IF EXISTS updated_at,
  DROP COLUMN IF EXISTS error_code,
  DROP COLUMN IF EXISTS request_hash,
  ADD CONSTRAINT command_log_commit_state_consistent
    CHECK (
      (status = 'committed' AND committed_at IS NOT NULL)
      OR (status <> 'committed')
    );
