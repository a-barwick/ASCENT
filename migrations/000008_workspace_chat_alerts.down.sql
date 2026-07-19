DROP TABLE IF EXISTS alerts.notifications;
DROP TABLE IF EXISTS alerts.rules;
DROP SCHEMA IF EXISTS alerts;

DROP TRIGGER IF EXISTS chat_messages_guard_update ON chat.messages;
DROP FUNCTION IF EXISTS chat.guard_message_update();
DROP TABLE IF EXISTS chat.reports;
DROP TABLE IF EXISTS chat.blocks;
DROP TABLE IF EXISTS chat.mutes;
DROP TABLE IF EXISTS chat.messages;
DROP TABLE IF EXISTS chat.channel_members;
DROP TABLE IF EXISTS chat.channels;
DROP SCHEMA IF EXISTS chat;

DROP TABLE IF EXISTS workspace.panel_deliveries;
ALTER TABLE workspace.devices
  DROP COLUMN IF EXISTS active_view_id;
DROP TABLE IF EXISTS workspace.views;
DROP TABLE IF EXISTS workspace.devices;
DROP SCHEMA IF EXISTS workspace;
