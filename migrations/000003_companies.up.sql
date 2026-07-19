CREATE SCHEMA IF NOT EXISTS companies;

CREATE TABLE companies.companies (
  company_id UUID PRIMARY KEY,
  name TEXT NOT NULL
    CHECK (btrim(name) <> '' AND char_length(name) <= 120),
  slug TEXT NOT NULL
    CHECK (
      slug = lower(slug)
      AND slug ~ '^[a-z0-9]+(?:-[a-z0-9]+)*$'
      AND char_length(slug) <= 80
    ),
  base_currency TEXT NOT NULL DEFAULT 'CR'
    CHECK (base_currency ~ '^[A-Z][A-Z0-9]{1,7}$'),
  status TEXT NOT NULL DEFAULT 'active'
    CHECK (status IN ('active', 'suspended', 'dissolved')),
  version BIGINT NOT NULL DEFAULT 1
    CHECK (version > 0),
  created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
  CONSTRAINT companies_slug_unique UNIQUE (slug),
  CONSTRAINT companies_id_currency_unique UNIQUE (company_id, base_currency)
);

CREATE UNIQUE INDEX companies_name_casefold_unique
  ON companies.companies (lower(name));

CREATE TABLE companies.memberships (
  company_id UUID NOT NULL
    REFERENCES companies.companies (company_id),
  player_id UUID NOT NULL
    REFERENCES identity.players (player_id),
  role TEXT NOT NULL
    CHECK (role IN ('owner', 'operator', 'trader', 'analyst', 'viewer')),
  approval_limit_minor BIGINT
    CHECK (approval_limit_minor IS NULL OR approval_limit_minor >= 0),
  joined_at TIMESTAMPTZ NOT NULL,
  left_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
  PRIMARY KEY (company_id, player_id),
  CONSTRAINT memberships_end_after_join
    CHECK (left_at IS NULL OR left_at >= joined_at)
);

CREATE INDEX memberships_player_active_idx
  ON companies.memberships (player_id, company_id)
  WHERE left_at IS NULL;

CREATE OR REPLACE FUNCTION companies.assert_active_company_has_owner()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
  target_company_id UUID;
BEGIN
  target_company_id := COALESCE(NEW.company_id, OLD.company_id);

  IF NOT EXISTS (
    SELECT 1
    FROM companies.companies
    WHERE company_id = target_company_id
      AND status = 'active'
  ) THEN
    RETURN NULL;
  END IF;

  IF NOT EXISTS (
    SELECT 1
    FROM companies.memberships
    WHERE company_id = target_company_id
      AND role = 'owner'
      AND left_at IS NULL
  ) THEN
    RAISE EXCEPTION 'active company % must have an active owner', target_company_id
      USING ERRCODE = '23514';
  END IF;

  RETURN NULL;
END;
$$;

CREATE CONSTRAINT TRIGGER companies_require_owner
AFTER INSERT OR UPDATE OF status ON companies.companies
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW
EXECUTE FUNCTION companies.assert_active_company_has_owner();

CREATE CONSTRAINT TRIGGER memberships_preserve_owner
AFTER INSERT OR UPDATE OR DELETE ON companies.memberships
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW
EXECUTE FUNCTION companies.assert_active_company_has_owner();

COMMENT ON SCHEMA companies IS
  'Companies and explicit player authority within a company boundary.';

COMMENT ON COLUMN companies.memberships.approval_limit_minor IS
  'Optional command approval ceiling in the company base currency minor unit.';
