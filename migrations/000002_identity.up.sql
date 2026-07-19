CREATE SCHEMA IF NOT EXISTS identity;

CREATE TABLE identity.players (
  player_id UUID PRIMARY KEY,
  provider TEXT NOT NULL
    CHECK (provider <> '' AND provider = lower(provider)),
  provider_subject TEXT NOT NULL
    CHECK (btrim(provider_subject) <> ''),
  display_name TEXT NOT NULL
    CHECK (btrim(display_name) <> '' AND char_length(display_name) <= 80),
  status TEXT NOT NULL DEFAULT 'active'
    CHECK (status IN ('active', 'suspended', 'closed')),
  created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
  CONSTRAINT players_provider_subject_unique
    UNIQUE (provider, provider_subject)
);

CREATE TABLE identity.sessions (
  session_id UUID PRIMARY KEY,
  player_id UUID NOT NULL
    REFERENCES identity.players (player_id),
  token_hash BYTEA NOT NULL
    CHECK (octet_length(token_hash) = 32),
  issued_at TIMESTAMPTZ NOT NULL,
  expires_at TIMESTAMPTZ NOT NULL,
  revoked_at TIMESTAMPTZ,
  last_seen_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
  CONSTRAINT sessions_token_hash_unique UNIQUE (token_hash),
  CONSTRAINT sessions_expiry_after_issue
    CHECK (expires_at > issued_at),
  CONSTRAINT sessions_revocation_after_issue
    CHECK (revoked_at IS NULL OR revoked_at >= issued_at),
  CONSTRAINT sessions_last_seen_after_issue
    CHECK (last_seen_at IS NULL OR last_seen_at >= issued_at)
);

CREATE INDEX sessions_player_active_idx
  ON identity.sessions (player_id, expires_at DESC)
  WHERE revoked_at IS NULL;

COMMENT ON SCHEMA identity IS
  'External identity subjects and revocable browser sessions; no password material is stored.';

COMMENT ON COLUMN identity.sessions.token_hash IS
  'SHA-256 digest of the opaque session token. The raw token is never persisted.';
