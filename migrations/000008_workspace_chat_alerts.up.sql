CREATE SCHEMA IF NOT EXISTS workspace;

CREATE TABLE workspace.devices (
  device_id UUID PRIMARY KEY,
  player_id UUID NOT NULL
    REFERENCES identity.players (player_id),
  session_id UUID
    REFERENCES identity.sessions (session_id),
  device_key TEXT NOT NULL
    CHECK (btrim(device_key) <> '' AND char_length(device_key) <= 160),
  name TEXT NOT NULL
    CHECK (btrim(name) <> '' AND char_length(name) <= 80),
  device_class TEXT NOT NULL
    CHECK (device_class IN ('desktop', 'tablet', 'phone', 'passive')),
  status TEXT NOT NULL DEFAULT 'registered'
    CHECK (status IN ('registered', 'disabled')),
  registered_at TIMESTAMPTZ NOT NULL,
  last_seen_at TIMESTAMPTZ NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
  CONSTRAINT workspace_devices_player_key_unique
    UNIQUE (player_id, device_key),
  CONSTRAINT workspace_devices_session_unique UNIQUE (session_id),
  CONSTRAINT workspace_devices_id_player_unique
    UNIQUE (device_id, player_id),
  CONSTRAINT workspace_devices_seen_after_registration
    CHECK (last_seen_at >= registered_at)
);

CREATE INDEX workspace_devices_player_seen_idx
  ON workspace.devices (player_id, last_seen_at DESC);

CREATE TABLE workspace.views (
  view_id UUID PRIMARY KEY,
  player_id UUID NOT NULL
    REFERENCES identity.players (player_id),
  name TEXT NOT NULL
    CHECK (btrim(name) <> '' AND char_length(name) <= 80),
  device_class TEXT NOT NULL
    CHECK (device_class IN ('desktop', 'tablet', 'phone', 'passive')),
  panel_definitions JSONB NOT NULL DEFAULT '[]'::jsonb
    CHECK (jsonb_typeof(panel_definitions) = 'array'),
  subscriptions JSONB NOT NULL DEFAULT '[]'::jsonb
    CHECK (jsonb_typeof(subscriptions) = 'array'),
  version BIGINT NOT NULL DEFAULT 1
    CHECK (version > 0),
  created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
  CONSTRAINT workspace_views_player_name_unique UNIQUE (player_id, name)
);

ALTER TABLE workspace.devices
  ADD COLUMN active_view_id UUID
    REFERENCES workspace.views (view_id);

CREATE TABLE workspace.panel_deliveries (
  panel_delivery_id UUID PRIMARY KEY,
  command_id UUID NOT NULL
    REFERENCES platform.command_log (command_id),
  player_id UUID NOT NULL
    REFERENCES identity.players (player_id),
  sender_device_id UUID NOT NULL,
  target_device_id UUID NOT NULL,
  route TEXT NOT NULL
    CHECK (
      route LIKE '/%'
      AND char_length(route) <= 500
      AND route !~ '[[:cntrl:]]'
    ),
  panel_payload JSONB NOT NULL DEFAULT '{}'::jsonb
    CHECK (jsonb_typeof(panel_payload) = 'object'),
  status TEXT NOT NULL DEFAULT 'queued'
    CHECK (status IN ('queued', 'delivered', 'expired', 'rejected')),
  event_id UUID
    REFERENCES platform.event_outbox (event_id),
  created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
  expires_at TIMESTAMPTZ NOT NULL,
  delivered_at TIMESTAMPTZ,
  CONSTRAINT panel_deliveries_command_unique UNIQUE (command_id),
  CONSTRAINT panel_deliveries_event_unique UNIQUE (event_id),
  CONSTRAINT panel_deliveries_sender_player_fk
    FOREIGN KEY (sender_device_id, player_id)
    REFERENCES workspace.devices (device_id, player_id),
  CONSTRAINT panel_deliveries_target_player_fk
    FOREIGN KEY (target_device_id, player_id)
    REFERENCES workspace.devices (device_id, player_id),
  CONSTRAINT panel_deliveries_distinct_devices
    CHECK (sender_device_id <> target_device_id),
  CONSTRAINT panel_deliveries_expiry_after_creation
    CHECK (expires_at > created_at),
  CONSTRAINT panel_deliveries_state_consistent
    CHECK (
      (
        status = 'queued'
        AND delivered_at IS NULL
        AND event_id IS NOT NULL
      )
      OR (
        status = 'delivered'
        AND delivered_at IS NOT NULL
        AND delivered_at >= created_at
        AND event_id IS NOT NULL
      )
      OR (
        status IN ('expired', 'rejected')
        AND delivered_at IS NULL
      )
    )
);

CREATE INDEX panel_deliveries_target_queue_idx
  ON workspace.panel_deliveries (target_device_id, created_at)
  WHERE status = 'queued';

CREATE SCHEMA IF NOT EXISTS chat;

CREATE TABLE chat.channels (
  channel_id UUID PRIMARY KEY,
  channel_type TEXT NOT NULL
    CHECK (channel_type IN ('global', 'regional', 'company', 'contract', 'direct')),
  name TEXT NOT NULL
    CHECK (btrim(name) <> '' AND char_length(name) <= 120),
  company_id UUID
    REFERENCES companies.companies (company_id),
  contract_id UUID
    REFERENCES freight.contracts (contract_id),
  region_location_id UUID
    REFERENCES inventory.locations (location_id),
  status TEXT NOT NULL DEFAULT 'active'
    CHECK (status IN ('active', 'archived', 'locked')),
  created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
  CONSTRAINT chat_channels_scope_consistent
    CHECK (
      (
        channel_type IN ('global', 'direct')
        AND company_id IS NULL
        AND contract_id IS NULL
        AND region_location_id IS NULL
      )
      OR (
        channel_type = 'regional'
        AND region_location_id IS NOT NULL
        AND company_id IS NULL
        AND contract_id IS NULL
      )
      OR (
        channel_type = 'company'
        AND company_id IS NOT NULL
        AND contract_id IS NULL
        AND region_location_id IS NULL
      )
      OR (
        channel_type = 'contract'
        AND contract_id IS NOT NULL
        AND company_id IS NULL
        AND region_location_id IS NULL
      )
    )
);

CREATE UNIQUE INDEX chat_one_global_channel
  ON chat.channels ((1))
  WHERE channel_type = 'global';

CREATE UNIQUE INDEX chat_company_channel_unique
  ON chat.channels (company_id)
  WHERE channel_type = 'company';

CREATE UNIQUE INDEX chat_contract_channel_unique
  ON chat.channels (contract_id)
  WHERE channel_type = 'contract';

CREATE UNIQUE INDEX chat_regional_channel_unique
  ON chat.channels (region_location_id)
  WHERE channel_type = 'regional';

CREATE TABLE chat.channel_members (
  channel_id UUID NOT NULL
    REFERENCES chat.channels (channel_id),
  player_id UUID NOT NULL
    REFERENCES identity.players (player_id),
  member_role TEXT NOT NULL DEFAULT 'member'
    CHECK (member_role IN ('member', 'moderator')),
  joined_at TIMESTAMPTZ NOT NULL,
  left_at TIMESTAMPTZ,
  PRIMARY KEY (channel_id, player_id),
  CONSTRAINT chat_channel_members_end_after_join
    CHECK (left_at IS NULL OR left_at >= joined_at)
);

CREATE TABLE chat.messages (
  message_id UUID PRIMARY KEY,
  command_id UUID NOT NULL
    REFERENCES platform.command_log (command_id),
  channel_id UUID NOT NULL
    REFERENCES chat.channels (channel_id),
  sender_player_id UUID NOT NULL
    REFERENCES identity.players (player_id),
  body TEXT NOT NULL
    CHECK (
      btrim(body) <> ''
      AND char_length(body) <= 2000
      AND body !~ '[\u0000]'
    ),
  event_id UUID NOT NULL
    REFERENCES platform.event_outbox (event_id),
  sent_at TIMESTAMPTZ NOT NULL,
  removed_at TIMESTAMPTZ,
  removed_by_player_id UUID
    REFERENCES identity.players (player_id),
  removal_reason TEXT
    CHECK (
      removal_reason IS NULL
      OR (btrim(removal_reason) <> '' AND char_length(removal_reason) <= 300)
    ),
  created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
  CONSTRAINT chat_messages_command_unique UNIQUE (command_id),
  CONSTRAINT chat_messages_event_unique UNIQUE (event_id),
  CONSTRAINT chat_messages_removal_consistent
    CHECK (
      (
        removed_at IS NULL
        AND removed_by_player_id IS NULL
        AND removal_reason IS NULL
      )
      OR (
        removed_at IS NOT NULL
        AND removed_at >= sent_at
        AND removed_by_player_id IS NOT NULL
        AND removal_reason IS NOT NULL
      )
    )
);

CREATE INDEX chat_messages_history_idx
  ON chat.messages (channel_id, sent_at DESC, message_id);

CREATE OR REPLACE FUNCTION chat.guard_message_update()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
  IF NEW.message_id IS DISTINCT FROM OLD.message_id
    OR NEW.command_id IS DISTINCT FROM OLD.command_id
    OR NEW.channel_id IS DISTINCT FROM OLD.channel_id
    OR NEW.sender_player_id IS DISTINCT FROM OLD.sender_player_id
    OR NEW.body IS DISTINCT FROM OLD.body
    OR NEW.event_id IS DISTINCT FROM OLD.event_id
    OR NEW.sent_at IS DISTINCT FROM OLD.sent_at
    OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
    RAISE EXCEPTION 'chat message content and attribution are immutable'
      USING ERRCODE = '55000';
  END IF;

  IF OLD.removed_at IS NOT NULL THEN
    RAISE EXCEPTION 'message moderation record is immutable'
      USING ERRCODE = '55000';
  END IF;

  RETURN NEW;
END;
$$;

CREATE TRIGGER chat_messages_guard_update
BEFORE UPDATE ON chat.messages
FOR EACH ROW
EXECUTE FUNCTION chat.guard_message_update();

CREATE TABLE chat.mutes (
  muting_player_id UUID NOT NULL
    REFERENCES identity.players (player_id),
  muted_player_id UUID NOT NULL
    REFERENCES identity.players (player_id),
  expires_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
  PRIMARY KEY (muting_player_id, muted_player_id),
  CONSTRAINT chat_mutes_not_self
    CHECK (muting_player_id <> muted_player_id),
  CONSTRAINT chat_mutes_expiry_after_creation
    CHECK (expires_at IS NULL OR expires_at > created_at)
);

CREATE TABLE chat.blocks (
  blocking_player_id UUID NOT NULL
    REFERENCES identity.players (player_id),
  blocked_player_id UUID NOT NULL
    REFERENCES identity.players (player_id),
  created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
  PRIMARY KEY (blocking_player_id, blocked_player_id),
  CONSTRAINT chat_blocks_not_self
    CHECK (blocking_player_id <> blocked_player_id)
);

CREATE TABLE chat.reports (
  report_id UUID PRIMARY KEY,
  reporter_player_id UUID NOT NULL
    REFERENCES identity.players (player_id),
  message_id UUID NOT NULL
    REFERENCES chat.messages (message_id),
  reason TEXT NOT NULL
    CHECK (btrim(reason) <> '' AND char_length(reason) <= 500),
  status TEXT NOT NULL DEFAULT 'open'
    CHECK (status IN ('open', 'reviewing', 'resolved', 'dismissed')),
  resolved_by_player_id UUID
    REFERENCES identity.players (player_id),
  resolution_note TEXT
    CHECK (
      resolution_note IS NULL
      OR (btrim(resolution_note) <> '' AND char_length(resolution_note) <= 500)
    ),
  created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
  resolved_at TIMESTAMPTZ,
  CONSTRAINT chat_reports_reporter_message_unique
    UNIQUE (reporter_player_id, message_id),
  CONSTRAINT chat_reports_resolution_consistent
    CHECK (
      (
        status IN ('open', 'reviewing')
        AND resolved_by_player_id IS NULL
        AND resolved_at IS NULL
      )
      OR (
        status IN ('resolved', 'dismissed')
        AND resolved_by_player_id IS NOT NULL
        AND resolved_at IS NOT NULL
        AND resolved_at >= created_at
      )
    )
);

CREATE INDEX chat_reports_open_idx
  ON chat.reports (created_at, report_id)
  WHERE status IN ('open', 'reviewing');

CREATE SCHEMA IF NOT EXISTS alerts;

CREATE TABLE alerts.rules (
  alert_rule_id UUID PRIMARY KEY,
  player_id UUID NOT NULL
    REFERENCES identity.players (player_id),
  company_id UUID
    REFERENCES companies.companies (company_id),
  alert_type TEXT NOT NULL
    CHECK (alert_type IN ('price', 'capacity', 'contract', 'delivery', 'incident')),
  target_type TEXT NOT NULL
    CHECK (target_type IN ('market', 'facility', 'contract', 'delivery', 'route')),
  target_id UUID NOT NULL,
  comparator TEXT NOT NULL
    CHECK (comparator IN ('above', 'below', 'changed', 'due', 'failed')),
  threshold_minor BIGINT,
  status TEXT NOT NULL DEFAULT 'active'
    CHECK (status IN ('active', 'paused', 'deleted')),
  cooldown_seconds INTEGER NOT NULL DEFAULT 0
    CHECK (cooldown_seconds >= 0),
  created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
  CONSTRAINT alert_rules_threshold_consistent
    CHECK (
      (
        comparator IN ('above', 'below')
        AND threshold_minor IS NOT NULL
      )
      OR (
        comparator IN ('changed', 'due', 'failed')
        AND threshold_minor IS NULL
      )
    )
);

CREATE INDEX alert_rules_target_active_idx
  ON alerts.rules (target_type, target_id, alert_type)
  WHERE status = 'active';

CREATE TABLE alerts.notifications (
  notification_id UUID PRIMARY KEY,
  alert_rule_id UUID
    REFERENCES alerts.rules (alert_rule_id),
  player_id UUID NOT NULL
    REFERENCES identity.players (player_id),
  source_event_id UUID
    REFERENCES platform.event_outbox (event_id),
  title TEXT NOT NULL
    CHECK (btrim(title) <> '' AND char_length(title) <= 160),
  body TEXT NOT NULL
    CHECK (btrim(body) <> '' AND char_length(body) <= 1000),
  payload JSONB NOT NULL DEFAULT '{}'::jsonb
    CHECK (jsonb_typeof(payload) = 'object'),
  created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
  delivered_at TIMESTAMPTZ,
  read_at TIMESTAMPTZ,
  CONSTRAINT alert_notifications_delivery_after_creation
    CHECK (delivered_at IS NULL OR delivered_at >= created_at),
  CONSTRAINT alert_notifications_read_after_creation
    CHECK (read_at IS NULL OR read_at >= created_at)
);

CREATE UNIQUE INDEX alert_notifications_rule_event_unique
  ON alerts.notifications (alert_rule_id, source_event_id)
  WHERE alert_rule_id IS NOT NULL AND source_event_id IS NOT NULL;

CREATE INDEX alert_notifications_player_unread_idx
  ON alerts.notifications (player_id, created_at DESC)
  WHERE read_at IS NULL;

COMMENT ON SCHEMA workspace IS
  'Named browser devices, preset views, and durable cross-device panel delivery.';

COMMENT ON SCHEMA chat IS
  'Scoped message history and player moderation state, isolated from economic tables.';

COMMENT ON SCHEMA alerts IS
  'Player-defined economic alert rules and durable notification history.';
