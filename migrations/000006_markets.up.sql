CREATE SCHEMA IF NOT EXISTS markets;

CREATE TABLE markets.markets (
  market_id UUID PRIMARY KEY,
  location_id UUID NOT NULL
    REFERENCES inventory.locations (location_id),
  commodity_id UUID NOT NULL
    REFERENCES inventory.commodities (commodity_id),
  currency TEXT NOT NULL
    CHECK (currency ~ '^[A-Z][A-Z0-9]{1,7}$'),
  price_scale SMALLINT NOT NULL DEFAULT 2
    CHECK (price_scale BETWEEN 0 AND 6),
  lot_size_minor BIGINT NOT NULL DEFAULT 1
    CHECK (lot_size_minor > 0),
  fee_basis_points INTEGER NOT NULL DEFAULT 0
    CHECK (fee_basis_points BETWEEN 0 AND 10000),
  status TEXT NOT NULL DEFAULT 'open'
    CHECK (status IN ('open', 'halted', 'closed')),
  created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
  CONSTRAINT markets_location_commodity_unique
    UNIQUE (location_id, commodity_id)
);

CREATE TABLE markets.orders (
  order_id UUID PRIMARY KEY,
  command_id UUID NOT NULL
    REFERENCES platform.command_log (command_id),
  market_id UUID NOT NULL
    REFERENCES markets.markets (market_id),
  company_id UUID NOT NULL
    REFERENCES companies.companies (company_id),
  side TEXT NOT NULL
    CHECK (side IN ('buy', 'sell')),
  limit_price_minor BIGINT NOT NULL
    CHECK (limit_price_minor > 0),
  original_quantity_minor BIGINT NOT NULL
    CHECK (original_quantity_minor > 0),
  remaining_quantity_minor BIGINT NOT NULL
    CHECK (
      remaining_quantity_minor >= 0
      AND remaining_quantity_minor <= original_quantity_minor
    ),
  status TEXT NOT NULL
    CHECK (
      status IN (
        'open',
        'partially_filled',
        'filled',
        'cancelled',
        'rejected'
      )
    ),
  priority_sequence BIGINT GENERATED ALWAYS AS IDENTITY,
  version BIGINT NOT NULL DEFAULT 1
    CHECK (version > 0),
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL,
  CONSTRAINT orders_command_unique UNIQUE (command_id),
  CONSTRAINT orders_priority_unique UNIQUE (priority_sequence),
  CONSTRAINT orders_status_quantity_consistent
    CHECK (
      (
        status IN ('open', 'partially_filled')
        AND remaining_quantity_minor > 0
      )
      OR (
        status = 'filled'
        AND remaining_quantity_minor = 0
      )
      OR status IN ('cancelled', 'rejected')
    )
);

CREATE INDEX orders_open_asks_idx
  ON markets.orders (
    market_id,
    limit_price_minor,
    priority_sequence
  )
  WHERE side = 'sell' AND status IN ('open', 'partially_filled');

CREATE INDEX orders_open_bids_idx
  ON markets.orders (
    market_id,
    limit_price_minor DESC,
    priority_sequence
  )
  WHERE side = 'buy' AND status IN ('open', 'partially_filled');

CREATE INDEX orders_company_history_idx
  ON markets.orders (company_id, created_at DESC, order_id);

CREATE TABLE markets.order_reservations (
  reservation_id UUID PRIMARY KEY,
  order_id UUID NOT NULL
    REFERENCES markets.orders (order_id),
  company_id UUID NOT NULL
    REFERENCES companies.companies (company_id),
  resource_type TEXT NOT NULL
    CHECK (resource_type IN ('cash', 'inventory')),
  ledger_account_id UUID,
  currency TEXT,
  holding_id UUID,
  original_reserved_minor BIGINT NOT NULL
    CHECK (original_reserved_minor > 0),
  remaining_reserved_minor BIGINT NOT NULL
    CHECK (
      remaining_reserved_minor >= 0
      AND remaining_reserved_minor <= original_reserved_minor
    ),
  status TEXT NOT NULL DEFAULT 'active'
    CHECK (status IN ('active', 'released', 'consumed')),
  version BIGINT NOT NULL DEFAULT 1
    CHECK (version > 0),
  created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
  CONSTRAINT order_reservations_cash_account_fk
    FOREIGN KEY (ledger_account_id, company_id, currency)
    REFERENCES ledger.accounts (account_id, company_id, currency),
  CONSTRAINT order_reservations_holding_company_fk
    FOREIGN KEY (holding_id, company_id)
    REFERENCES inventory.holdings (holding_id, company_id),
  CONSTRAINT order_reservations_resource_consistent
    CHECK (
      (
        resource_type = 'cash'
        AND ledger_account_id IS NOT NULL
        AND currency IS NOT NULL
        AND holding_id IS NULL
      )
      OR (
        resource_type = 'inventory'
        AND ledger_account_id IS NULL
        AND currency IS NULL
        AND holding_id IS NOT NULL
      )
    ),
  CONSTRAINT order_reservations_status_amount_consistent
    CHECK (
      (status = 'active' AND remaining_reserved_minor > 0)
      OR (
        status IN ('released', 'consumed')
        AND remaining_reserved_minor = 0
      )
    )
);

CREATE UNIQUE INDEX order_reservations_cash_order_unique
  ON markets.order_reservations (order_id)
  WHERE resource_type = 'cash';

CREATE UNIQUE INDEX order_reservations_order_holding_unique
  ON markets.order_reservations (order_id, holding_id)
  WHERE resource_type = 'inventory';

CREATE INDEX order_reservations_active_holding_idx
  ON markets.order_reservations (holding_id, order_id)
  WHERE resource_type = 'inventory' AND status = 'active';

CREATE TABLE markets.trades (
  trade_id UUID PRIMARY KEY,
  market_id UUID NOT NULL
    REFERENCES markets.markets (market_id),
  buy_order_id UUID NOT NULL
    REFERENCES markets.orders (order_id),
  sell_order_id UUID NOT NULL
    REFERENCES markets.orders (order_id),
  buyer_company_id UUID NOT NULL
    REFERENCES companies.companies (company_id),
  seller_company_id UUID NOT NULL
    REFERENCES companies.companies (company_id),
  price_minor BIGINT NOT NULL
    CHECK (price_minor > 0),
  quantity_minor BIGINT NOT NULL
    CHECK (quantity_minor > 0),
  buyer_fee_minor BIGINT NOT NULL DEFAULT 0
    CHECK (buyer_fee_minor >= 0),
  seller_fee_minor BIGINT NOT NULL DEFAULT 0
    CHECK (seller_fee_minor >= 0),
  inventory_movement_id UUID NOT NULL
    REFERENCES inventory.movements (movement_id),
  buyer_journal_id UUID NOT NULL
    REFERENCES ledger.journals (journal_id),
  seller_journal_id UUID NOT NULL
    REFERENCES ledger.journals (journal_id),
  event_id UUID NOT NULL
    REFERENCES platform.event_outbox (event_id),
  execution_sequence BIGINT GENERATED ALWAYS AS IDENTITY,
  executed_at TIMESTAMPTZ NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
  CONSTRAINT trades_order_pair_unique
    UNIQUE (buy_order_id, sell_order_id),
  CONSTRAINT trades_event_unique UNIQUE (event_id),
  CONSTRAINT trades_execution_sequence_unique UNIQUE (execution_sequence),
  CONSTRAINT trades_distinct_orders CHECK (buy_order_id <> sell_order_id),
  CONSTRAINT trades_distinct_companies
    CHECK (buyer_company_id <> seller_company_id)
);

CREATE INDEX trades_market_tape_idx
  ON markets.trades (market_id, execution_sequence DESC);

CREATE OR REPLACE FUNCTION markets.guard_order_update()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
  IF NEW.order_id IS DISTINCT FROM OLD.order_id
    OR NEW.command_id IS DISTINCT FROM OLD.command_id
    OR NEW.market_id IS DISTINCT FROM OLD.market_id
    OR NEW.company_id IS DISTINCT FROM OLD.company_id
    OR NEW.side IS DISTINCT FROM OLD.side
    OR NEW.limit_price_minor IS DISTINCT FROM OLD.limit_price_minor
    OR NEW.original_quantity_minor IS DISTINCT FROM OLD.original_quantity_minor
    OR NEW.priority_sequence IS DISTINCT FROM OLD.priority_sequence
    OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
    RAISE EXCEPTION 'order identity and terms are immutable'
      USING ERRCODE = '55000';
  END IF;

  IF OLD.status IN ('filled', 'cancelled', 'rejected') THEN
    RAISE EXCEPTION 'terminal order is immutable'
      USING ERRCODE = '55000';
  END IF;

  IF NEW.remaining_quantity_minor > OLD.remaining_quantity_minor THEN
    RAISE EXCEPTION 'order remaining quantity cannot increase'
      USING ERRCODE = '23514';
  END IF;

  IF NOT (
    (OLD.status = 'open' AND NEW.status IN ('open', 'partially_filled', 'filled', 'cancelled'))
    OR (
      OLD.status = 'partially_filled'
      AND NEW.status IN ('partially_filled', 'filled', 'cancelled')
    )
  ) THEN
    RAISE EXCEPTION 'invalid order status transition: % -> %', OLD.status, NEW.status
      USING ERRCODE = '23514';
  END IF;

  IF NEW.version <> OLD.version + 1 THEN
    RAISE EXCEPTION 'order version must advance by exactly one'
      USING ERRCODE = '23514';
  END IF;

  NEW.updated_at := clock_timestamp();
  RETURN NEW;
END;
$$;

CREATE TRIGGER orders_guard_update
BEFORE UPDATE ON markets.orders
FOR EACH ROW
EXECUTE FUNCTION markets.guard_order_update();

CREATE OR REPLACE FUNCTION markets.guard_reservation_update()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
  IF NEW.reservation_id IS DISTINCT FROM OLD.reservation_id
    OR NEW.order_id IS DISTINCT FROM OLD.order_id
    OR NEW.company_id IS DISTINCT FROM OLD.company_id
    OR NEW.resource_type IS DISTINCT FROM OLD.resource_type
    OR NEW.ledger_account_id IS DISTINCT FROM OLD.ledger_account_id
    OR NEW.currency IS DISTINCT FROM OLD.currency
    OR NEW.holding_id IS DISTINCT FROM OLD.holding_id
    OR NEW.original_reserved_minor IS DISTINCT FROM OLD.original_reserved_minor
    OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
    RAISE EXCEPTION 'reservation identity and source are immutable'
      USING ERRCODE = '55000';
  END IF;

  IF OLD.status IN ('released', 'consumed') THEN
    RAISE EXCEPTION 'terminal reservation is immutable'
      USING ERRCODE = '55000';
  END IF;

  IF NEW.remaining_reserved_minor > OLD.remaining_reserved_minor THEN
    RAISE EXCEPTION 'reservation amount cannot increase'
      USING ERRCODE = '23514';
  END IF;

  IF NOT (
    NEW.status = 'active'
    OR (OLD.status = 'active' AND NEW.status IN ('released', 'consumed'))
  ) THEN
    RAISE EXCEPTION 'invalid reservation status transition: % -> %', OLD.status, NEW.status
      USING ERRCODE = '23514';
  END IF;

  IF NEW.version <> OLD.version + 1 THEN
    RAISE EXCEPTION 'reservation version must advance by exactly one'
      USING ERRCODE = '23514';
  END IF;

  NEW.updated_at := clock_timestamp();
  RETURN NEW;
END;
$$;

CREATE TRIGGER order_reservations_guard_update
BEFORE UPDATE ON markets.order_reservations
FOR EACH ROW
EXECUTE FUNCTION markets.guard_reservation_update();

CREATE OR REPLACE FUNCTION markets.reject_trade_change()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
  RAISE EXCEPTION 'committed trades are immutable'
    USING ERRCODE = '55000';
END;
$$;

CREATE TRIGGER trades_are_immutable
BEFORE UPDATE OR DELETE ON markets.trades
FOR EACH ROW
EXECUTE FUNCTION markets.reject_trade_change();

CREATE OR REPLACE FUNCTION markets.assert_order_fill_consistent()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
  target_order_id UUID;
  target_order_ids UUID[];
  original_quantity BIGINT;
  remaining_quantity BIGINT;
  filled_quantity NUMERIC;
BEGIN
  IF TG_TABLE_NAME = 'orders' THEN
    target_order_ids := ARRAY[COALESCE(NEW.order_id, OLD.order_id)];
  ELSE
    target_order_ids := ARRAY[NEW.buy_order_id, NEW.sell_order_id];
  END IF;

  FOREACH target_order_id IN ARRAY target_order_ids LOOP
    SELECT original_quantity_minor, remaining_quantity_minor
    INTO original_quantity, remaining_quantity
    FROM markets.orders
    WHERE order_id = target_order_id;

    IF NOT FOUND THEN
      CONTINUE;
    END IF;

    SELECT COALESCE(sum(quantity_minor), 0)
    INTO filled_quantity
    FROM markets.trades
    WHERE buy_order_id = target_order_id
       OR sell_order_id = target_order_id;

    IF original_quantity - remaining_quantity <> filled_quantity THEN
      RAISE EXCEPTION
        'order % fill mismatch: original %, remaining %, trades %',
        target_order_id,
        original_quantity,
        remaining_quantity,
        filled_quantity
        USING ERRCODE = '23514';
    END IF;
  END LOOP;

  IF TG_TABLE_NAME = 'trades' THEN
    IF NOT EXISTS (
      SELECT 1
      FROM markets.orders AS buy_order
      JOIN markets.orders AS sell_order
        ON sell_order.order_id = NEW.sell_order_id
      JOIN markets.markets AS traded_market
        ON traded_market.market_id = NEW.market_id
      WHERE buy_order.order_id = NEW.buy_order_id
        AND buy_order.side = 'buy'
        AND sell_order.side = 'sell'
        AND buy_order.market_id = NEW.market_id
        AND sell_order.market_id = NEW.market_id
        AND buy_order.company_id = NEW.buyer_company_id
        AND sell_order.company_id = NEW.seller_company_id
        AND buy_order.limit_price_minor >= NEW.price_minor
        AND sell_order.limit_price_minor <= NEW.price_minor
        AND mod(NEW.quantity_minor, traded_market.lot_size_minor) = 0
    ) THEN
      RAISE EXCEPTION 'trade % is incompatible with its orders', NEW.trade_id
        USING ERRCODE = '23514';
    END IF;
  END IF;

  RETURN NULL;
END;
$$;

CREATE CONSTRAINT TRIGGER orders_match_trade_totals
AFTER INSERT OR UPDATE ON markets.orders
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW
EXECUTE FUNCTION markets.assert_order_fill_consistent();

CREATE CONSTRAINT TRIGGER trades_match_orders
AFTER INSERT ON markets.trades
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW
EXECUTE FUNCTION markets.assert_order_fill_consistent();

CREATE OR REPLACE FUNCTION markets.assert_order_reservations_consistent()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
  target_order_id UUID;
  order_side TEXT;
  order_status TEXT;
  order_market_id UUID;
  order_company_id UUID;
  order_original BIGINT;
  order_remaining BIGINT;
  order_limit_price BIGINT;
  active_reserved NUMERIC;
  expected_resource TEXT;
  market_commodity_id UUID;
  market_currency TEXT;
  market_lot_size BIGINT;
  commodity_quantity_scale SMALLINT;
  quantity_divisor NUMERIC;
  cash_numerator NUMERIC;
  cash_exposure NUMERIC;
BEGIN
  target_order_id := COALESCE(NEW.order_id, OLD.order_id);

  SELECT
    side,
    status,
    market_id,
    company_id,
    original_quantity_minor,
    remaining_quantity_minor,
    limit_price_minor
  INTO
    order_side,
    order_status,
    order_market_id,
    order_company_id,
    order_original,
    order_remaining,
    order_limit_price
  FROM markets.orders
  WHERE order_id = target_order_id;

  IF NOT FOUND THEN
    RETURN NULL;
  END IF;

  SELECT
    market.commodity_id,
    market.currency,
    market.lot_size_minor,
    commodity.quantity_scale
  INTO
    market_commodity_id,
    market_currency,
    market_lot_size,
    commodity_quantity_scale
  FROM markets.markets AS market
  JOIN inventory.commodities AS commodity
    ON commodity.commodity_id = market.commodity_id
  WHERE market.market_id = order_market_id;

  IF mod(order_original, market_lot_size) <> 0
    OR mod(order_remaining, market_lot_size) <> 0 THEN
    RAISE EXCEPTION
      'order % quantity must be an exact multiple of market lot size %',
      target_order_id,
      market_lot_size
      USING ERRCODE = '23514';
  END IF;

  expected_resource := CASE order_side WHEN 'buy' THEN 'cash' ELSE 'inventory' END;

  IF EXISTS (
    SELECT 1
    FROM markets.order_reservations
    WHERE order_id = target_order_id
      AND resource_type <> expected_resource
  ) THEN
    RAISE EXCEPTION 'order % has reservation of the wrong resource type', target_order_id
      USING ERRCODE = '23514';
  END IF;

  SELECT COALESCE(sum(remaining_reserved_minor), 0)
  INTO active_reserved
  FROM markets.order_reservations
  WHERE order_id = target_order_id
    AND status = 'active';

  IF order_status IN ('open', 'partially_filled') THEN
    IF order_side = 'sell' AND active_reserved <> order_remaining THEN
      RAISE EXCEPTION
        'sell order % must reserve exactly its remaining quantity',
        target_order_id
        USING ERRCODE = '23514';
    END IF;

    IF order_side = 'buy' THEN
      quantity_divisor := power(10::numeric, commodity_quantity_scale::numeric);
      cash_numerator :=
        order_limit_price::numeric * order_remaining::numeric;

      IF mod(cash_numerator, quantity_divisor) <> 0 THEN
        RAISE EXCEPTION
          'buy order % exposure is not an exact currency minor-unit amount',
          target_order_id
          USING ERRCODE = '23514';
      END IF;

      cash_exposure := cash_numerator / quantity_divisor;
      IF active_reserved < cash_exposure THEN
        RAISE EXCEPTION
          'buy order % reserves %, below limit-price exposure %',
          target_order_id,
          active_reserved,
          cash_exposure
          USING ERRCODE = '23514';
      END IF;
    END IF;
  ELSIF active_reserved <> 0 THEN
    RAISE EXCEPTION 'terminal order % retains an active reservation', target_order_id
      USING ERRCODE = '23514';
  END IF;

  IF EXISTS (
    SELECT 1
    FROM markets.order_reservations AS reservation
    LEFT JOIN ledger.accounts AS account
      ON account.account_id = reservation.ledger_account_id
    LEFT JOIN inventory.holdings AS holding
      ON holding.holding_id = reservation.holding_id
    WHERE reservation.order_id = target_order_id
      AND (
        reservation.company_id <> order_company_id
        OR (
          reservation.resource_type = 'cash'
          AND (
            account.category <> 'asset'
            OR reservation.currency <> market_currency
          )
        )
        OR (
          reservation.resource_type = 'inventory'
          AND holding.commodity_id <> market_commodity_id
        )
      )
  ) THEN
    RAISE EXCEPTION 'order % reservation source is incompatible with its market', target_order_id
      USING ERRCODE = '23514';
  END IF;

  RETURN NULL;
END;
$$;

CREATE CONSTRAINT TRIGGER orders_require_reservations
AFTER INSERT OR UPDATE ON markets.orders
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW
EXECUTE FUNCTION markets.assert_order_reservations_consistent();

CREATE CONSTRAINT TRIGGER reservations_match_order
AFTER INSERT OR UPDATE OR DELETE ON markets.order_reservations
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW
EXECUTE FUNCTION markets.assert_order_reservations_consistent();

CREATE OR REPLACE FUNCTION markets.assert_holding_reservation_total()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
  target_holding_id UUID;
  recorded_reserved BIGINT;
  reservation_total NUMERIC;
BEGIN
  IF TG_TABLE_NAME = 'holdings' THEN
    target_holding_id := COALESCE(NEW.holding_id, OLD.holding_id);
  ELSE
    target_holding_id := COALESCE(NEW.holding_id, OLD.holding_id);
  END IF;

  IF target_holding_id IS NULL THEN
    RETURN NULL;
  END IF;

  SELECT reserved_quantity_minor
  INTO recorded_reserved
  FROM inventory.holdings
  WHERE holding_id = target_holding_id;

  IF NOT FOUND THEN
    RETURN NULL;
  END IF;

  SELECT COALESCE(sum(remaining_reserved_minor), 0)
  INTO reservation_total
  FROM markets.order_reservations
  WHERE holding_id = target_holding_id
    AND status = 'active';

  IF recorded_reserved <> reservation_total THEN
    RAISE EXCEPTION
      'holding % reservation mismatch: holding %, reservations %',
      target_holding_id,
      recorded_reserved,
      reservation_total
      USING ERRCODE = '23514';
  END IF;

  RETURN NULL;
END;
$$;

CREATE CONSTRAINT TRIGGER holdings_match_market_reservations
AFTER INSERT OR UPDATE ON inventory.holdings
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW
EXECUTE FUNCTION markets.assert_holding_reservation_total();

CREATE CONSTRAINT TRIGGER inventory_reservations_match_holding
AFTER INSERT OR UPDATE OR DELETE ON markets.order_reservations
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW
EXECUTE FUNCTION markets.assert_holding_reservation_total();

COMMENT ON SCHEMA markets IS
  'Price-time-priority limit orders, auditable reservations, and immutable settled trades.';

COMMENT ON COLUMN markets.orders.priority_sequence IS
  'Monotonic database-assigned tie breaker after price for deterministic matching.';
