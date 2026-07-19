CREATE SCHEMA IF NOT EXISTS production;

CREATE TABLE production.facility_types (
  facility_type_id UUID PRIMARY KEY,
  code TEXT NOT NULL
    CHECK (
      code = lower(code)
      AND code ~ '^[a-z0-9]+(?:-[a-z0-9]+)*$'
      AND char_length(code) <= 80
    ),
  name TEXT NOT NULL
    CHECK (btrim(name) <> '' AND char_length(name) <= 120),
  created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
  CONSTRAINT facility_types_code_unique UNIQUE (code)
);

CREATE TABLE production.recipes (
  recipe_id UUID PRIMARY KEY,
  facility_type_id UUID NOT NULL
    REFERENCES production.facility_types (facility_type_id),
  rule_version TEXT NOT NULL
    CHECK (btrim(rule_version) <> '' AND char_length(rule_version) <= 48),
  cycle_seconds INTEGER NOT NULL
    CHECK (cycle_seconds > 0),
  output_commodity_id UUID NOT NULL
    REFERENCES inventory.commodities (commodity_id),
  output_quantity_minor BIGINT NOT NULL
    CHECK (output_quantity_minor > 0),
  created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
  CONSTRAINT recipes_type_version_unique
    UNIQUE (facility_type_id, rule_version),
  CONSTRAINT recipes_id_type_unique
    UNIQUE (recipe_id, facility_type_id)
);

CREATE TABLE production.recipe_inputs (
  recipe_id UUID NOT NULL
    REFERENCES production.recipes (recipe_id),
  commodity_id UUID NOT NULL
    REFERENCES inventory.commodities (commodity_id),
  quantity_minor BIGINT NOT NULL
    CHECK (quantity_minor > 0),
  PRIMARY KEY (recipe_id, commodity_id)
);

CREATE TABLE production.facilities (
  facility_id UUID PRIMARY KEY,
  company_id UUID NOT NULL
    REFERENCES companies.companies (company_id),
  location_id UUID NOT NULL
    REFERENCES inventory.locations (location_id),
  facility_type_id UUID NOT NULL
    REFERENCES production.facility_types (facility_type_id),
  active_recipe_id UUID NOT NULL,
  name TEXT NOT NULL
    CHECK (btrim(name) <> '' AND char_length(name) <= 120),
  nominal_capacity_minor BIGINT NOT NULL
    CHECK (nominal_capacity_minor > 0),
  utilization_basis_points INTEGER NOT NULL DEFAULT 10000
    CHECK (utilization_basis_points BETWEEN 0 AND 10000),
  condition_basis_points INTEGER NOT NULL DEFAULT 10000
    CHECK (condition_basis_points BETWEEN 0 AND 10000),
  status TEXT NOT NULL DEFAULT 'operational'
    CHECK (status IN ('planned', 'operational', 'constrained', 'maintenance', 'closed')),
  next_execution_at TIMESTAMPTZ,
  version BIGINT NOT NULL DEFAULT 1
    CHECK (version > 0),
  created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
  CONSTRAINT facilities_recipe_type_fk
    FOREIGN KEY (active_recipe_id, facility_type_id)
    REFERENCES production.recipes (recipe_id, facility_type_id)
);

CREATE INDEX facilities_company_location_idx
  ON production.facilities (company_id, location_id, facility_id);

CREATE INDEX facilities_due_idx
  ON production.facilities (next_execution_at, facility_id)
  WHERE status IN ('operational', 'constrained')
    AND next_execution_at IS NOT NULL;

CREATE TABLE production.jobs (
  job_id UUID PRIMARY KEY,
  facility_id UUID NOT NULL
    REFERENCES production.facilities (facility_id),
  recipe_id UUID NOT NULL
    REFERENCES production.recipes (recipe_id),
  rule_version TEXT NOT NULL
    CHECK (btrim(rule_version) <> '' AND char_length(rule_version) <= 48),
  due_at TIMESTAMPTZ NOT NULL,
  status TEXT NOT NULL DEFAULT 'queued'
    CHECK (status IN ('queued', 'running', 'committed', 'failed')),
  attempt_count INTEGER NOT NULL DEFAULT 0
    CHECK (attempt_count >= 0),
  random_seed BIGINT NOT NULL,
  worker_id TEXT
    CHECK (worker_id IS NULL OR char_length(worker_id) <= 120),
  claimed_at TIMESTAMPTZ,
  lease_expires_at TIMESTAMPTZ,
  completed_at TIMESTAMPTZ,
  failure_code TEXT
    CHECK (failure_code IS NULL OR btrim(failure_code) <> ''),
  event_id UUID
    REFERENCES platform.event_outbox (event_id),
  created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
  CONSTRAINT production_jobs_execution_unique
    UNIQUE (facility_id, due_at, rule_version),
  CONSTRAINT production_jobs_event_unique UNIQUE (event_id),
  CONSTRAINT production_jobs_lease_consistent
    CHECK (
      (claimed_at IS NULL AND lease_expires_at IS NULL)
      OR (
        claimed_at IS NOT NULL
        AND lease_expires_at IS NOT NULL
        AND lease_expires_at > claimed_at
      )
    ),
  CONSTRAINT production_jobs_state_consistent
    CHECK (
      (
        status = 'queued'
        AND completed_at IS NULL
        AND failure_code IS NULL
        AND event_id IS NULL
      )
      OR (
        status = 'running'
        AND claimed_at IS NOT NULL
        AND completed_at IS NULL
        AND failure_code IS NULL
        AND event_id IS NULL
      )
      OR (
        status = 'committed'
        AND claimed_at IS NOT NULL
        AND completed_at IS NOT NULL
        AND completed_at >= claimed_at
        AND failure_code IS NULL
        AND event_id IS NOT NULL
      )
      OR (
        status = 'failed'
        AND completed_at IS NOT NULL
        AND failure_code IS NOT NULL
        AND event_id IS NULL
      )
    )
);

CREATE INDEX production_jobs_due_idx
  ON production.jobs (due_at, job_id)
  WHERE status IN ('queued', 'failed');

CREATE TABLE production.job_inventory_movements (
  job_id UUID NOT NULL
    REFERENCES production.jobs (job_id),
  movement_id UUID NOT NULL
    REFERENCES inventory.movements (movement_id),
  role TEXT NOT NULL
    CHECK (role IN ('input', 'output')),
  PRIMARY KEY (job_id, movement_id)
);

CREATE TABLE production.job_journals (
  job_id UUID NOT NULL
    REFERENCES production.jobs (job_id),
  journal_id UUID NOT NULL
    REFERENCES ledger.journals (journal_id),
  PRIMARY KEY (job_id, journal_id)
);

CREATE OR REPLACE FUNCTION production.assert_job_contract()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
  target_job_id UUID;
  job_status TEXT;
  job_recipe_id UUID;
  job_rule_version TEXT;
  recipe_rule_version TEXT;
  output_count BIGINT;
  journal_count BIGINT;
BEGIN
  target_job_id := COALESCE(NEW.job_id, OLD.job_id);

  SELECT status, recipe_id, rule_version
  INTO job_status, job_recipe_id, job_rule_version
  FROM production.jobs
  WHERE job_id = target_job_id;

  IF NOT FOUND THEN
    RETURN NULL;
  END IF;

  SELECT rule_version
  INTO recipe_rule_version
  FROM production.recipes
  WHERE recipe_id = job_recipe_id;

  IF recipe_rule_version IS DISTINCT FROM job_rule_version THEN
    RAISE EXCEPTION 'production job % rule version does not match recipe', target_job_id
      USING ERRCODE = '23514';
  END IF;

  IF EXISTS (
    SELECT 1
    FROM production.job_inventory_movements AS link
    JOIN inventory.movements AS movement
      ON movement.movement_id = link.movement_id
    WHERE link.job_id = target_job_id
      AND (
        movement.source_id <> target_job_id
        OR (link.role = 'input' AND movement.movement_kind <> 'consumption')
        OR (link.role = 'output' AND movement.movement_kind <> 'production')
      )
  ) THEN
    RAISE EXCEPTION 'production job % has incompatible inventory movements', target_job_id
      USING ERRCODE = '23514';
  END IF;

  SELECT count(*)
  INTO output_count
  FROM production.job_inventory_movements
  WHERE job_id = target_job_id
    AND role = 'output';

  SELECT count(*)
  INTO journal_count
  FROM production.job_journals
  WHERE job_id = target_job_id;

  IF job_status = 'committed'
    AND (output_count = 0 OR journal_count = 0) THEN
    RAISE EXCEPTION
      'committed production job % requires output movement and journal',
      target_job_id
      USING ERRCODE = '23514';
  END IF;

  RETURN NULL;
END;
$$;

CREATE CONSTRAINT TRIGGER production_jobs_require_effects
AFTER INSERT OR UPDATE ON production.jobs
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW
EXECUTE FUNCTION production.assert_job_contract();

CREATE CONSTRAINT TRIGGER production_job_movements_match
AFTER INSERT OR UPDATE OR DELETE ON production.job_inventory_movements
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW
EXECUTE FUNCTION production.assert_job_contract();

CREATE CONSTRAINT TRIGGER production_job_journals_match
AFTER INSERT OR UPDATE OR DELETE ON production.job_journals
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW
EXECUTE FUNCTION production.assert_job_contract();

CREATE SCHEMA IF NOT EXISTS freight;

CREATE TABLE freight.routes (
  route_id UUID PRIMARY KEY,
  code TEXT NOT NULL
    CHECK (
      code = lower(code)
      AND code ~ '^[a-z0-9]+(?:-[a-z0-9]+)*$'
      AND char_length(code) <= 80
    ),
  origin_location_id UUID NOT NULL
    REFERENCES inventory.locations (location_id),
  destination_location_id UUID NOT NULL
    REFERENCES inventory.locations (location_id),
  transit_seconds INTEGER NOT NULL
    CHECK (transit_seconds > 0),
  capacity_quantity_minor BIGINT NOT NULL
    CHECK (capacity_quantity_minor > 0),
  status TEXT NOT NULL DEFAULT 'open'
    CHECK (status IN ('open', 'constrained', 'closed')),
  created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
  CONSTRAINT freight_routes_code_unique UNIQUE (code),
  CONSTRAINT freight_routes_distinct_endpoints
    CHECK (origin_location_id <> destination_location_id)
);

CREATE TABLE freight.contracts (
  contract_id UUID PRIMARY KEY,
  command_id UUID NOT NULL
    REFERENCES platform.command_log (command_id),
  route_id UUID NOT NULL
    REFERENCES freight.routes (route_id),
  commodity_id UUID NOT NULL
    REFERENCES inventory.commodities (commodity_id),
  shipper_company_id UUID NOT NULL
    REFERENCES companies.companies (company_id),
  carrier_company_id UUID NOT NULL
    REFERENCES companies.companies (company_id),
  quantity_minor BIGINT NOT NULL
    CHECK (quantity_minor > 0),
  delivered_quantity_minor BIGINT NOT NULL DEFAULT 0
    CHECK (
      delivered_quantity_minor >= 0
      AND delivered_quantity_minor <= quantity_minor
    ),
  unit_price_minor BIGINT NOT NULL
    CHECK (unit_price_minor >= 0),
  currency TEXT NOT NULL
    CHECK (currency ~ '^[A-Z][A-Z0-9]{1,7}$'),
  status TEXT NOT NULL
    CHECK (
      status IN (
        'offered',
        'active',
        'completed',
        'breached',
        'cancelled'
      )
    ),
  pickup_after TIMESTAMPTZ NOT NULL,
  deliver_by TIMESTAMPTZ NOT NULL,
  terms JSONB NOT NULL DEFAULT '{}'::jsonb,
  version BIGINT NOT NULL DEFAULT 1
    CHECK (version > 0),
  created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
  CONSTRAINT freight_contracts_command_unique UNIQUE (command_id),
  CONSTRAINT freight_contracts_distinct_companies
    CHECK (shipper_company_id <> carrier_company_id),
  CONSTRAINT freight_contracts_window_valid
    CHECK (deliver_by > pickup_after),
  CONSTRAINT freight_contracts_status_quantity_consistent
    CHECK (
      (status = 'completed' AND delivered_quantity_minor = quantity_minor)
      OR status <> 'completed'
    )
);

CREATE INDEX freight_contracts_company_status_idx
  ON freight.contracts (shipper_company_id, status, deliver_by);

CREATE INDEX freight_contracts_carrier_status_idx
  ON freight.contracts (carrier_company_id, status, deliver_by);

CREATE TABLE freight.deliveries (
  delivery_id UUID PRIMARY KEY,
  command_id UUID
    REFERENCES platform.command_log (command_id),
  contract_id UUID NOT NULL
    REFERENCES freight.contracts (contract_id),
  delivery_sequence INTEGER NOT NULL
    CHECK (delivery_sequence > 0),
  quantity_minor BIGINT NOT NULL
    CHECK (quantity_minor > 0),
  status TEXT NOT NULL
    CHECK (status IN ('scheduled', 'in_transit', 'delivered', 'failed', 'cancelled')),
  scheduled_departure_at TIMESTAMPTZ NOT NULL,
  departed_at TIMESTAMPTZ,
  arrived_at TIMESTAMPTZ,
  failure_code TEXT
    CHECK (failure_code IS NULL OR btrim(failure_code) <> ''),
  inventory_movement_id UUID
    REFERENCES inventory.movements (movement_id),
  shipper_journal_id UUID
    REFERENCES ledger.journals (journal_id),
  carrier_journal_id UUID
    REFERENCES ledger.journals (journal_id),
  event_id UUID
    REFERENCES platform.event_outbox (event_id),
  created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
  CONSTRAINT freight_deliveries_contract_sequence_unique
    UNIQUE (contract_id, delivery_sequence),
  CONSTRAINT freight_deliveries_command_unique UNIQUE (command_id),
  CONSTRAINT freight_deliveries_movement_unique UNIQUE (inventory_movement_id),
  CONSTRAINT freight_deliveries_event_unique UNIQUE (event_id),
  CONSTRAINT freight_deliveries_state_consistent
    CHECK (
      (
        status = 'scheduled'
        AND departed_at IS NULL
        AND arrived_at IS NULL
        AND failure_code IS NULL
        AND inventory_movement_id IS NULL
        AND shipper_journal_id IS NULL
        AND carrier_journal_id IS NULL
        AND event_id IS NULL
      )
      OR (
        status = 'in_transit'
        AND departed_at IS NOT NULL
        AND arrived_at IS NULL
        AND failure_code IS NULL
        AND inventory_movement_id IS NULL
        AND event_id IS NULL
      )
      OR (
        status = 'delivered'
        AND departed_at IS NOT NULL
        AND arrived_at IS NOT NULL
        AND arrived_at >= departed_at
        AND failure_code IS NULL
        AND inventory_movement_id IS NOT NULL
        AND shipper_journal_id IS NOT NULL
        AND carrier_journal_id IS NOT NULL
        AND event_id IS NOT NULL
      )
      OR (
        status = 'failed'
        AND failure_code IS NOT NULL
        AND inventory_movement_id IS NULL
        AND event_id IS NULL
      )
      OR (
        status = 'cancelled'
        AND arrived_at IS NULL
        AND inventory_movement_id IS NULL
        AND event_id IS NULL
      )
    )
);

CREATE INDEX freight_deliveries_due_idx
  ON freight.deliveries (scheduled_departure_at, delivery_id)
  WHERE status = 'scheduled';

CREATE OR REPLACE FUNCTION freight.assert_contract_delivery_total()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
  target_contract_id UUID;
  contract_quantity BIGINT;
  recorded_delivered BIGINT;
  delivered_total NUMERIC;
BEGIN
  target_contract_id := COALESCE(NEW.contract_id, OLD.contract_id);

  SELECT quantity_minor, delivered_quantity_minor
  INTO contract_quantity, recorded_delivered
  FROM freight.contracts
  WHERE contract_id = target_contract_id;

  IF NOT FOUND THEN
    RETURN NULL;
  END IF;

  SELECT COALESCE(sum(quantity_minor), 0)
  INTO delivered_total
  FROM freight.deliveries
  WHERE contract_id = target_contract_id
    AND status = 'delivered';

  IF delivered_total > contract_quantity
    OR recorded_delivered <> delivered_total THEN
    RAISE EXCEPTION
      'freight contract % delivery mismatch: contracted %, recorded %, deliveries %',
      target_contract_id,
      contract_quantity,
      recorded_delivered,
      delivered_total
      USING ERRCODE = '23514';
  END IF;

  RETURN NULL;
END;
$$;

CREATE CONSTRAINT TRIGGER freight_contracts_match_deliveries
AFTER INSERT OR UPDATE ON freight.contracts
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW
EXECUTE FUNCTION freight.assert_contract_delivery_total();

CREATE CONSTRAINT TRIGGER freight_deliveries_match_contract
AFTER INSERT OR UPDATE OR DELETE ON freight.deliveries
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW
EXECUTE FUNCTION freight.assert_contract_delivery_total();

CREATE OR REPLACE FUNCTION freight.guard_delivery_update()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
  IF NEW.delivery_id IS DISTINCT FROM OLD.delivery_id
    OR NEW.command_id IS DISTINCT FROM OLD.command_id
    OR NEW.contract_id IS DISTINCT FROM OLD.contract_id
    OR NEW.delivery_sequence IS DISTINCT FROM OLD.delivery_sequence
    OR NEW.quantity_minor IS DISTINCT FROM OLD.quantity_minor
    OR NEW.scheduled_departure_at IS DISTINCT FROM OLD.scheduled_departure_at
    OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
    RAISE EXCEPTION 'delivery identity and terms are immutable'
      USING ERRCODE = '55000';
  END IF;

  IF OLD.status IN ('delivered', 'cancelled') THEN
    RAISE EXCEPTION 'terminal delivery is immutable'
      USING ERRCODE = '55000';
  END IF;

  IF NOT (
    (OLD.status = 'scheduled' AND NEW.status IN ('in_transit', 'failed', 'cancelled'))
    OR (OLD.status = 'in_transit' AND NEW.status IN ('delivered', 'failed'))
    OR (OLD.status = 'failed' AND NEW.status = 'scheduled')
  ) THEN
    RAISE EXCEPTION 'invalid delivery status transition: % -> %', OLD.status, NEW.status
      USING ERRCODE = '23514';
  END IF;

  NEW.updated_at := clock_timestamp();
  RETURN NEW;
END;
$$;

CREATE TRIGGER freight_deliveries_guard_update
BEFORE UPDATE ON freight.deliveries
FOR EACH ROW
EXECUTE FUNCTION freight.guard_delivery_update();

COMMENT ON SCHEMA production IS
  'Versioned facility recipes and idempotent due-work execution records.';

COMMENT ON SCHEMA freight IS
  'Route capacity, simple freight obligations, and atomically settled deliveries.';
