CREATE SCHEMA IF NOT EXISTS inventory;

CREATE TABLE inventory.locations (
  location_id UUID PRIMARY KEY,
  parent_location_id UUID
    REFERENCES inventory.locations (location_id),
  code TEXT NOT NULL
    CHECK (
      code = lower(code)
      AND code ~ '^[a-z0-9]+(?:-[a-z0-9]+)*$'
      AND char_length(code) <= 80
    ),
  name TEXT NOT NULL
    CHECK (btrim(name) <> '' AND char_length(name) <= 120),
  location_type TEXT NOT NULL
    CHECK (location_type IN ('earth', 'orbit', 'surface', 'facility', 'storage')),
  status TEXT NOT NULL DEFAULT 'active'
    CHECK (status IN ('active', 'constrained', 'closed')),
  created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
  CONSTRAINT locations_code_unique UNIQUE (code),
  CONSTRAINT locations_parent_not_self
    CHECK (parent_location_id IS NULL OR parent_location_id <> location_id)
);

CREATE TABLE inventory.commodities (
  commodity_id UUID PRIMARY KEY,
  code TEXT NOT NULL
    CHECK (
      code = lower(code)
      AND code ~ '^[a-z0-9]+(?:-[a-z0-9]+)*$'
      AND char_length(code) <= 80
    ),
  name TEXT NOT NULL
    CHECK (btrim(name) <> '' AND char_length(name) <= 120),
  unit TEXT NOT NULL
    CHECK (btrim(unit) <> '' AND char_length(unit) <= 24),
  quantity_scale SMALLINT NOT NULL DEFAULT 6
    CHECK (quantity_scale BETWEEN 0 AND 9),
  status TEXT NOT NULL DEFAULT 'active'
    CHECK (status IN ('active', 'retired')),
  created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
  CONSTRAINT commodities_code_unique UNIQUE (code)
);

CREATE TABLE inventory.holdings (
  holding_id UUID PRIMARY KEY,
  company_id UUID NOT NULL
    REFERENCES companies.companies (company_id),
  location_id UUID NOT NULL
    REFERENCES inventory.locations (location_id),
  commodity_id UUID NOT NULL
    REFERENCES inventory.commodities (commodity_id),
  quantity_minor BIGINT NOT NULL DEFAULT 0
    CHECK (quantity_minor >= 0),
  reserved_quantity_minor BIGINT NOT NULL DEFAULT 0
    CHECK (
      reserved_quantity_minor >= 0
      AND reserved_quantity_minor <= quantity_minor
    ),
  cost_basis_minor BIGINT NOT NULL DEFAULT 0
    CHECK (cost_basis_minor >= 0),
  version BIGINT NOT NULL DEFAULT 1
    CHECK (version > 0),
  created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
  available_quantity_minor BIGINT GENERATED ALWAYS AS (
    quantity_minor - reserved_quantity_minor
  ) STORED,
  CONSTRAINT holdings_company_location_commodity_unique
    UNIQUE (company_id, location_id, commodity_id),
  CONSTRAINT holdings_id_company_unique
    UNIQUE (holding_id, company_id)
);

CREATE INDEX holdings_company_commodity_idx
  ON inventory.holdings (company_id, commodity_id, location_id);

CREATE TABLE inventory.movements (
  movement_id UUID PRIMARY KEY,
  command_id UUID
    REFERENCES platform.command_log (command_id),
  movement_kind TEXT NOT NULL
    CHECK (
      movement_kind IN (
        'opening',
        'transfer',
        'settlement',
        'production',
        'consumption',
        'loss',
        'correction'
      )
    ),
  source_id UUID NOT NULL,
  from_holding_id UUID
    REFERENCES inventory.holdings (holding_id),
  to_holding_id UUID
    REFERENCES inventory.holdings (holding_id),
  quantity_minor BIGINT NOT NULL
    CHECK (quantity_minor > 0),
  occurred_at TIMESTAMPTZ NOT NULL,
  reason TEXT NOT NULL
    CHECK (btrim(reason) <> '' AND char_length(reason) <= 300),
  created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
  CONSTRAINT movements_source_unique
    UNIQUE NULLS NOT DISTINCT (
      movement_kind,
      source_id,
      from_holding_id,
      to_holding_id
    ),
  CONSTRAINT movements_endpoints_consistent
    CHECK (
      (
        movement_kind IN ('transfer', 'settlement')
        AND from_holding_id IS NOT NULL
        AND to_holding_id IS NOT NULL
        AND from_holding_id <> to_holding_id
      )
      OR (
        movement_kind IN ('opening', 'production')
        AND from_holding_id IS NULL
        AND to_holding_id IS NOT NULL
      )
      OR (
        movement_kind IN ('consumption', 'loss')
        AND from_holding_id IS NOT NULL
        AND to_holding_id IS NULL
      )
      OR (
        movement_kind = 'correction'
        AND (from_holding_id IS NULL) <> (to_holding_id IS NULL)
      )
    )
);

CREATE INDEX movements_from_history_idx
  ON inventory.movements (from_holding_id, occurred_at, movement_id)
  WHERE from_holding_id IS NOT NULL;

CREATE INDEX movements_to_history_idx
  ON inventory.movements (to_holding_id, occurred_at, movement_id)
  WHERE to_holding_id IS NOT NULL;

CREATE OR REPLACE FUNCTION inventory.assert_movement_compatible()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
  from_commodity_id UUID;
  to_commodity_id UUID;
BEGIN
  IF NEW.from_holding_id IS NULL OR NEW.to_holding_id IS NULL THEN
    RETURN NULL;
  END IF;

  SELECT commodity_id
  INTO from_commodity_id
  FROM inventory.holdings
  WHERE holding_id = NEW.from_holding_id;

  SELECT commodity_id
  INTO to_commodity_id
  FROM inventory.holdings
  WHERE holding_id = NEW.to_holding_id;

  IF from_commodity_id IS DISTINCT FROM to_commodity_id THEN
    RAISE EXCEPTION
      'inventory movement % cannot change commodity',
      NEW.movement_id
      USING ERRCODE = '23514';
  END IF;

  RETURN NULL;
END;
$$;

CREATE CONSTRAINT TRIGGER movements_preserve_commodity
AFTER INSERT OR UPDATE ON inventory.movements
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW
EXECUTE FUNCTION inventory.assert_movement_compatible();

CREATE OR REPLACE FUNCTION inventory.reject_movement_change()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
  RAISE EXCEPTION 'committed inventory movements are immutable'
    USING ERRCODE = '55000';
END;
$$;

CREATE TRIGGER movements_are_immutable
BEFORE UPDATE OR DELETE ON inventory.movements
FOR EACH ROW
EXECUTE FUNCTION inventory.reject_movement_change();

COMMENT ON SCHEMA inventory IS
  'Location-specific commodity holdings and immutable inventory movement history.';

COMMENT ON COLUMN inventory.holdings.quantity_minor IS
  'Fixed-scale integer quantity using inventory.commodities.quantity_scale.';
