package gamepostgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"ascent/internal/identity"
	"ascent/internal/platform/ids"
	protocol "ascent/protocol/gen/go"
)

type snapshotScope struct {
	actorID        string
	displayName    string
	companyID      string
	companyName    string
	companyVersion int64
	role           string
	operator       bool
}

type snapshotMarketRecord struct {
	id            string
	location      string
	commodity     string
	unit          string
	currency      string
	quantityScale int16
	priceScale    int16
}

type marketHistory struct {
	points          []PricePoint
	firstPriceMinor int64
	lastPriceMinor  int64
	volumeMinor     int64
	latestBidMinor  int64
	latestAskMinor  int64
}

// Snapshot returns a transactionally consistent, disposable projection of the
// actor's current company state. Economic totals are recomputed from immutable
// ledger entries rather than copied from mutable company columns.
func (s *Service) Snapshot(ctx context.Context, actor identity.Actor) (protocol.SnapshotEnvelope, error) {
	if s == nil || s.database == nil {
		return protocol.SnapshotEnvelope{}, errors.New("load game snapshot: database is required")
	}
	if !ids.IsUUID(actor.ID) {
		return protocol.SnapshotEnvelope{}, errors.New("load game snapshot: actor ID must be a UUID")
	}
	transaction, err := s.database.BeginTx(ctx, &sql.TxOptions{
		Isolation: sql.LevelRepeatableRead,
		ReadOnly:  true,
	})
	if err != nil {
		return protocol.SnapshotEnvelope{}, fmt.Errorf("load game snapshot: begin transaction: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()

	scope, err := loadSnapshotScope(ctx, transaction, actor.ID)
	if err != nil {
		return protocol.SnapshotEnvelope{}, err
	}
	now := s.clock().UTC()
	position, err := loadFinancialPosition(ctx, transaction, scope.companyID, now)
	if err != nil {
		return protocol.SnapshotEnvelope{}, err
	}
	cash, assets, liabilities, netWorth, availableCredit, ratio, rating := companyMetrics(position)
	payload := GameSnapshot{
		SystemTime: now.Format("2006-01-02 15:04:05 UTC"),
		Actor: SnapshotActor{
			ID:          scope.actorID,
			DisplayName: scope.displayName,
			Status:      "authenticated",
		},
		Membership: SnapshotMembership{
			CompanyID:   scope.companyID,
			Role:        scope.role,
			Permissions: permissionsFor(scope.role, scope.operator),
		},
		Company: SnapshotCompany{
			ID:                scope.companyID,
			Name:              scope.companyName,
			Version:           scope.companyVersion,
			Cash:              cash,
			TotalAssets:       assets,
			TotalLiabilities:  liabilities,
			NetWorth:          netWorth,
			CreditRating:      rating,
			AvailableCredit:   availableCredit,
			DebtToEquityRatio: ratio,
			Statements: []CompanyStatement{
				{Label: "Revenue / 24h", Value: fixedToDisplay(position.Revenue24h, currencyScale), Change: nil},
				{Label: "Operating cost / 24h", Value: fixedToDisplay(position.Expense24h, currencyScale), Change: nil},
				{Label: "EBITDA / 24h", Value: fixedToDisplay(position.Revenue24h-position.Expense24h, currencyScale), Change: nil},
			},
		},
	}

	if payload.Markets, err = loadMarkets(ctx, transaction); err != nil {
		return protocol.SnapshotEnvelope{}, err
	}
	if payload.OpenOrders, err = loadOpenOrders(ctx, transaction, scope.companyID); err != nil {
		return protocol.SnapshotEnvelope{}, err
	}
	if payload.Trades, err = loadTrades(ctx, transaction, scope.companyID); err != nil {
		return protocol.SnapshotEnvelope{}, err
	}
	if payload.Inventory, err = loadInventory(ctx, transaction, scope.companyID); err != nil {
		return protocol.SnapshotEnvelope{}, err
	}
	if payload.Facilities, payload.ProductionTrace, err = loadFacilities(ctx, transaction, scope.companyID); err != nil {
		return protocol.SnapshotEnvelope{}, err
	}
	if payload.Freight, err = loadFreight(ctx, transaction, scope.companyID); err != nil {
		return protocol.SnapshotEnvelope{}, err
	}
	if payload.Devices, err = loadDevices(ctx, transaction, scope.actorID); err != nil {
		return protocol.SnapshotEnvelope{}, err
	}
	if payload.Panels, err = loadPanels(ctx, transaction, scope.actorID); err != nil {
		return protocol.SnapshotEnvelope{}, err
	}
	if payload.Chat, err = loadChat(ctx, transaction, scope.companyID); err != nil {
		return protocol.SnapshotEnvelope{}, err
	}
	if payload.Alerts, err = loadAlerts(ctx, transaction, scope.actorID); err != nil {
		return protocol.SnapshotEnvelope{}, err
	}
	if payload.OperatorAudit, err = loadAudit(ctx, transaction, scope.actorID, scope.companyID); err != nil {
		return protocol.SnapshotEnvelope{}, err
	}
	payload.Indices = marketIndices(payload.Markets)

	sequence, err := authorizedSequence(ctx, transaction, scope.actorID)
	if err != nil {
		return protocol.SnapshotEnvelope{}, err
	}
	encodedPayload, err := json.Marshal(payload)
	if err != nil {
		return protocol.SnapshotEnvelope{}, fmt.Errorf("load game snapshot: encode payload: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		return protocol.SnapshotEnvelope{}, fmt.Errorf("load game snapshot: commit read: %w", err)
	}
	expiresAt := now.Add(2 * time.Minute)
	return protocol.SnapshotEnvelope{
		ExpiresAt:       &expiresAt,
		GeneratedAt:     now,
		Payload:         encodedPayload,
		ProtocolVersion: protocol.Version,
		Sequence:        sequence,
		SnapshotId:      fmt.Sprintf("snapshot-%s-%d", scope.companyID, sequence),
		Topic:           "game." + scope.companyID,
	}, nil
}

func loadSnapshotScope(ctx context.Context, transaction *sql.Tx, actorID string) (snapshotScope, error) {
	var scope snapshotScope
	err := transaction.QueryRowContext(ctx, `
		SELECT
			player.player_id::text,
			player.display_name,
			company.company_id::text,
			company.name,
			company.version,
			membership.role,
			EXISTS (
				SELECT 1
				FROM operator_admin.grants AS grant_record
				WHERE grant_record.player_id = player.player_id
				  AND grant_record.role IN ('operator', 'administrator')
				  AND grant_record.revoked_at IS NULL
			)
		FROM identity.players AS player
		JOIN companies.memberships AS membership
		  ON membership.player_id = player.player_id
		 AND membership.left_at IS NULL
		JOIN companies.companies AS company
		  ON company.company_id = membership.company_id
		 AND company.status = 'active'
		WHERE player.player_id = $1::uuid
		  AND player.status = 'active'
		ORDER BY (membership.role = 'owner') DESC, membership.joined_at, company.company_id
		LIMIT 1`, actorID).Scan(
		&scope.actorID,
		&scope.displayName,
		&scope.companyID,
		&scope.companyName,
		&scope.companyVersion,
		&scope.role,
		&scope.operator,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return snapshotScope{}, ErrNotSeeded
	}
	if err != nil {
		return snapshotScope{}, fmt.Errorf("load game snapshot: actor scope: %w", err)
	}
	return scope, nil
}

func loadFinancialPosition(ctx context.Context, transaction *sql.Tx, companyID string, now time.Time) (financialPosition, error) {
	var position financialPosition
	err := transaction.QueryRowContext(ctx, `
		SELECT
			COALESCE(sum(
				CASE WHEN account.code = 'CASH'
					THEN CASE entry.side WHEN 'debit' THEN entry.amount_minor ELSE -entry.amount_minor END
					ELSE 0 END
			), 0)::bigint,
			COALESCE(sum(
				CASE WHEN account.category = 'asset'
					THEN CASE entry.side WHEN 'debit' THEN entry.amount_minor ELSE -entry.amount_minor END
					ELSE 0 END
			), 0)::bigint,
			COALESCE(sum(
				CASE WHEN account.category = 'liability'
					THEN CASE entry.side WHEN 'credit' THEN entry.amount_minor ELSE -entry.amount_minor END
					ELSE 0 END
			), 0)::bigint,
			COALESCE(sum(
				CASE WHEN account.category = 'revenue' AND journal.occurred_at >= $2
					THEN CASE entry.side WHEN 'credit' THEN entry.amount_minor ELSE -entry.amount_minor END
					ELSE 0 END
			), 0)::bigint,
			COALESCE(sum(
				CASE WHEN account.category = 'expense' AND journal.occurred_at >= $2
					THEN CASE entry.side WHEN 'debit' THEN entry.amount_minor ELSE -entry.amount_minor END
					ELSE 0 END
			), 0)::bigint
		FROM ledger.accounts AS account
		LEFT JOIN ledger.entries AS entry
		  ON entry.account_id = account.account_id
		LEFT JOIN ledger.journals AS journal
		  ON journal.journal_id = entry.journal_id
		WHERE account.company_id = $1::uuid`, companyID, now.Add(-24*time.Hour)).Scan(
		&position.Cash,
		&position.Assets,
		&position.Liabilities,
		&position.Revenue24h,
		&position.Expense24h,
	)
	if err != nil {
		return financialPosition{}, fmt.Errorf("load game snapshot: financial position: %w", err)
	}
	return position, nil
}

func loadMarkets(ctx context.Context, transaction *sql.Tx) ([]SnapshotMarket, error) {
	rows, err := transaction.QueryContext(ctx, `
		SELECT
			market.market_id::text,
			location.name,
			commodity.name,
			commodity.unit,
			market.currency,
			commodity.quantity_scale,
			market.price_scale
		FROM markets.markets AS market
		JOIN inventory.locations AS location
		  ON location.location_id = market.location_id
		JOIN inventory.commodities AS commodity
		  ON commodity.commodity_id = market.commodity_id
		WHERE market.status <> 'closed'
		ORDER BY location.name, commodity.name, market.market_id`)
	if err != nil {
		return nil, fmt.Errorf("load game snapshot: markets: %w", err)
	}
	defer rows.Close()
	records := make([]snapshotMarketRecord, 0)
	for rows.Next() {
		var record snapshotMarketRecord
		if err := rows.Scan(
			&record.id,
			&record.location,
			&record.commodity,
			&record.unit,
			&record.currency,
			&record.quantityScale,
			&record.priceScale,
		); err != nil {
			return nil, fmt.Errorf("load game snapshot: scan market: %w", err)
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("load game snapshot: market rows: %w", err)
	}

	markets := make([]SnapshotMarket, 0, len(records))
	for _, record := range records {
		history, err := loadMarketHistory(ctx, transaction, record)
		if err != nil {
			return nil, err
		}
		book, err := loadOrderBook(ctx, transaction, record)
		if err != nil {
			return nil, err
		}
		bidMinor, askMinor := history.latestBidMinor, history.latestAskMinor
		if len(book.Bids) > 0 {
			bidMinor = int64(math.Round(book.Bids[0].Price * math.Pow10(int(record.priceScale))))
		}
		if len(book.Asks) > 0 {
			askMinor = int64(math.Round(book.Asks[0].Price * math.Pow10(int(record.priceScale))))
		}
		spreadMinor := int64(0)
		if bidMinor > 0 && askMinor >= bidMinor {
			spreadMinor = askMinor - bidMinor
		}
		markets = append(markets, SnapshotMarket{
			ID:           record.id,
			Location:     record.location,
			Commodity:    record.commodity,
			Unit:         record.unit,
			Currency:     record.currency,
			LastPrice:    fixedToDisplay(history.lastPriceMinor, record.priceScale),
			Change24Hour: percentageChange(history.firstPriceMinor, history.lastPriceMinor),
			Volume24Hour: fixedToDisplay(history.volumeMinor, record.quantityScale),
			Spread:       fixedToDisplay(spreadMinor, record.priceScale),
			History:      history.points,
			OrderBook:    book,
		})
	}
	return markets, nil
}

func loadMarketHistory(ctx context.Context, transaction *sql.Tx, market snapshotMarketRecord) (marketHistory, error) {
	rows, err := transaction.QueryContext(ctx, `
		SELECT
			event.payload->>'label',
			(event.payload->>'priceMinor')::bigint,
			COALESCE((event.payload->>'volumeQuantityMinor')::bigint, 0),
			COALESCE((event.payload->>'bestBidMinor')::bigint, 0),
			COALESCE((event.payload->>'bestAskMinor')::bigint, 0)
		FROM platform.event_outbox AS event
		WHERE event.event_type = 'MARKET_PRICE_OBSERVED'
		  AND event.payload->>'marketId' = $1
		ORDER BY event.occurred_at, event.sequence`, market.id)
	if err != nil {
		return marketHistory{}, fmt.Errorf("load game snapshot: market history %s: %w", market.id, err)
	}
	defer rows.Close()
	history := marketHistory{points: make([]PricePoint, 0)}
	for rows.Next() {
		var label string
		var priceMinor, volumeMinor, bidMinor, askMinor int64
		if err := rows.Scan(&label, &priceMinor, &volumeMinor, &bidMinor, &askMinor); err != nil {
			return marketHistory{}, fmt.Errorf("load game snapshot: scan market history %s: %w", market.id, err)
		}
		if len(history.points) == 0 {
			history.firstPriceMinor = priceMinor
		}
		history.lastPriceMinor = priceMinor
		history.volumeMinor += volumeMinor
		history.latestBidMinor = bidMinor
		history.latestAskMinor = askMinor
		history.points = append(history.points, PricePoint{Label: label, Value: fixedToDisplay(priceMinor, market.priceScale)})
	}
	if err := rows.Err(); err != nil {
		return marketHistory{}, fmt.Errorf("load game snapshot: market history rows %s: %w", market.id, err)
	}
	if len(history.points) == 0 {
		return loadTradeHistory(ctx, transaction, market)
	}
	return history, nil
}

func loadTradeHistory(ctx context.Context, transaction *sql.Tx, market snapshotMarketRecord) (marketHistory, error) {
	rows, err := transaction.QueryContext(ctx, `
		SELECT
			to_char(trade.executed_at AT TIME ZONE 'UTC', 'HH24:MI'),
			trade.price_minor,
			trade.quantity_minor
		FROM markets.trades AS trade
		WHERE trade.market_id = $1::uuid
		ORDER BY trade.execution_sequence
		LIMIT 24`, market.id)
	if err != nil {
		return marketHistory{}, fmt.Errorf("load game snapshot: trade history %s: %w", market.id, err)
	}
	defer rows.Close()
	type tradePoint struct {
		label         string
		price, volume int64
	}
	points := make([]tradePoint, 0)
	for rows.Next() {
		var point tradePoint
		if err := rows.Scan(&point.label, &point.price, &point.volume); err != nil {
			return marketHistory{}, fmt.Errorf("load game snapshot: scan trade history %s: %w", market.id, err)
		}
		points = append(points, point)
	}
	if err := rows.Err(); err != nil {
		return marketHistory{}, fmt.Errorf("load game snapshot: trade history rows %s: %w", market.id, err)
	}
	history := marketHistory{points: make([]PricePoint, 0, len(points))}
	for index, point := range points {
		if index == 0 {
			history.firstPriceMinor = point.price
		}
		history.lastPriceMinor = point.price
		history.volumeMinor += point.volume
		history.points = append(history.points, PricePoint{Label: point.label, Value: fixedToDisplay(point.price, market.priceScale)})
	}
	return history, nil
}

func loadOrderBook(ctx context.Context, transaction *sql.Tx, market snapshotMarketRecord) (OrderBook, error) {
	rows, err := transaction.QueryContext(ctx, `
		SELECT
			order_record.side,
			order_record.limit_price_minor,
			sum(order_record.remaining_quantity_minor)::bigint,
			count(*)::bigint
		FROM markets.orders AS order_record
		WHERE order_record.market_id = $1::uuid
		  AND order_record.status IN ('open', 'partially_filled')
		GROUP BY order_record.side, order_record.limit_price_minor
		ORDER BY
			CASE order_record.side WHEN 'buy' THEN 0 ELSE 1 END,
			CASE WHEN order_record.side = 'buy' THEN order_record.limit_price_minor END DESC,
			CASE WHEN order_record.side = 'sell' THEN order_record.limit_price_minor END ASC`, market.id)
	if err != nil {
		return OrderBook{}, fmt.Errorf("load game snapshot: order book %s: %w", market.id, err)
	}
	defer rows.Close()
	book := OrderBook{Bids: make([]OrderLevel, 0), Asks: make([]OrderLevel, 0)}
	for rows.Next() {
		var side string
		var priceMinor, quantityMinor, orders int64
		if err := rows.Scan(&side, &priceMinor, &quantityMinor, &orders); err != nil {
			return OrderBook{}, fmt.Errorf("load game snapshot: scan order book %s: %w", market.id, err)
		}
		level := OrderLevel{
			Price:    fixedToDisplay(priceMinor, market.priceScale),
			Quantity: fixedToDisplay(quantityMinor, market.quantityScale),
			Orders:   orders,
		}
		if side == "buy" {
			book.Bids = append(book.Bids, level)
		} else {
			book.Asks = append(book.Asks, level)
		}
	}
	if err := rows.Err(); err != nil {
		return OrderBook{}, fmt.Errorf("load game snapshot: order book rows %s: %w", market.id, err)
	}
	return book, nil
}

func loadOpenOrders(ctx context.Context, transaction *sql.Tx, companyID string) ([]SnapshotOpenOrder, error) {
	rows, err := transaction.QueryContext(ctx, `
		SELECT
			order_record.order_id::text,
			order_record.market_id::text,
			order_record.side,
			market.price_scale,
			commodity.quantity_scale,
			order_record.limit_price_minor,
			order_record.original_quantity_minor,
			order_record.remaining_quantity_minor,
			order_record.status,
			order_record.created_at
		FROM markets.orders AS order_record
		JOIN markets.markets AS market
		  ON market.market_id = order_record.market_id
		JOIN inventory.commodities AS commodity
		  ON commodity.commodity_id = market.commodity_id
		WHERE order_record.company_id = $1::uuid
		  AND order_record.status IN ('open', 'partially_filled')
		ORDER BY order_record.created_at DESC, order_record.order_id`, companyID)
	if err != nil {
		return nil, fmt.Errorf("load game snapshot: open orders: %w", err)
	}
	defer rows.Close()
	orders := make([]SnapshotOpenOrder, 0)
	for rows.Next() {
		var order SnapshotOpenOrder
		var priceScale, quantityScale int16
		var priceMinor, originalMinor, remainingMinor int64
		var status string
		if err := rows.Scan(
			&order.ID,
			&order.MarketID,
			&order.Side,
			&priceScale,
			&quantityScale,
			&priceMinor,
			&originalMinor,
			&remainingMinor,
			&status,
			&order.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("load game snapshot: scan open order: %w", err)
		}
		order.OrderType = "limit"
		order.Price = fixedToDisplay(priceMinor, priceScale)
		order.Quantity = fixedToDisplay(originalMinor, quantityScale)
		order.FilledQuantity = fixedToDisplay(originalMinor-remainingMinor, quantityScale)
		order.Status = snapshotOrderStatus(status)
		orders = append(orders, order)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("load game snapshot: open order rows: %w", err)
	}
	return orders, nil
}

func loadTrades(ctx context.Context, transaction *sql.Tx, companyID string) ([]SnapshotTrade, error) {
	rows, err := transaction.QueryContext(ctx, `
		SELECT
			trade.trade_id::text,
			trade.market_id::text,
			CASE WHEN trade.buyer_company_id = $1::uuid THEN 'buy' ELSE 'sell' END,
			market.price_scale,
			commodity.quantity_scale,
			trade.price_minor,
			trade.quantity_minor,
			counterparty.name,
			trade.executed_at
		FROM markets.trades AS trade
		JOIN markets.markets AS market
		  ON market.market_id = trade.market_id
		JOIN inventory.commodities AS commodity
		  ON commodity.commodity_id = market.commodity_id
		JOIN companies.companies AS counterparty
		  ON counterparty.company_id = CASE
			WHEN trade.buyer_company_id = $1::uuid THEN trade.seller_company_id
			ELSE trade.buyer_company_id
		  END
		WHERE trade.buyer_company_id = $1::uuid
		   OR trade.seller_company_id = $1::uuid
		ORDER BY trade.execution_sequence DESC
		LIMIT 50`, companyID)
	if err != nil {
		return nil, fmt.Errorf("load game snapshot: trades: %w", err)
	}
	defer rows.Close()
	trades := make([]SnapshotTrade, 0)
	for rows.Next() {
		var trade SnapshotTrade
		var priceScale, quantityScale int16
		var priceMinor, quantityMinor int64
		if err := rows.Scan(
			&trade.ID,
			&trade.MarketID,
			&trade.Side,
			&priceScale,
			&quantityScale,
			&priceMinor,
			&quantityMinor,
			&trade.Counterparty,
			&trade.OccurredAt,
		); err != nil {
			return nil, fmt.Errorf("load game snapshot: scan trade: %w", err)
		}
		trade.Price = fixedToDisplay(priceMinor, priceScale)
		trade.Quantity = fixedToDisplay(quantityMinor, quantityScale)
		trade.Total = trade.Price * trade.Quantity
		trades = append(trades, trade)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("load game snapshot: trade rows: %w", err)
	}
	return trades, nil
}

func loadInventory(ctx context.Context, transaction *sql.Tx, companyID string) ([]InventoryPosition, error) {
	rows, err := transaction.QueryContext(ctx, `
		SELECT
			holding.holding_id::text,
			commodity.name,
			location.name,
			holding.quantity_minor,
			holding.reserved_quantity_minor,
			commodity.quantity_scale,
			commodity.unit
		FROM inventory.holdings AS holding
		JOIN inventory.commodities AS commodity
		  ON commodity.commodity_id = holding.commodity_id
		JOIN inventory.locations AS location
		  ON location.location_id = holding.location_id
		WHERE holding.company_id = $1::uuid
		ORDER BY commodity.name, location.name, holding.holding_id`, companyID)
	if err != nil {
		return nil, fmt.Errorf("load game snapshot: inventory: %w", err)
	}
	defer rows.Close()
	positions := make([]InventoryPosition, 0)
	for rows.Next() {
		var position InventoryPosition
		var quantityMinor, reservedMinor int64
		var scale int16
		if err := rows.Scan(
			&position.ID,
			&position.Commodity,
			&position.Location,
			&quantityMinor,
			&reservedMinor,
			&scale,
			&position.Unit,
		); err != nil {
			return nil, fmt.Errorf("load game snapshot: scan inventory: %w", err)
		}
		position.Quantity = fixedToDisplay(quantityMinor, scale)
		position.Reserved = fixedToDisplay(reservedMinor, scale)
		positions = append(positions, position)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("load game snapshot: inventory rows: %w", err)
	}
	return positions, nil
}

func loadFacilities(ctx context.Context, transaction *sql.Tx, companyID string) ([]SnapshotFacility, []ProductionTrace, error) {
	rows, err := transaction.QueryContext(ctx, `
		SELECT
			facility.facility_id::text,
			facility.name,
			facility_type.name,
			location.name,
			facility.utilization_basis_points,
			facility.condition_basis_points,
			facility.status,
			input_commodity.name,
			output_commodity.name,
			facility.nominal_capacity_minor,
			output_commodity.quantity_scale,
			output_commodity.unit
		FROM production.facilities AS facility
		JOIN production.facility_types AS facility_type
		  ON facility_type.facility_type_id = facility.facility_type_id
		JOIN inventory.locations AS location
		  ON location.location_id = facility.location_id
		JOIN production.recipes AS recipe
		  ON recipe.recipe_id = facility.active_recipe_id
		JOIN inventory.commodities AS output_commodity
		  ON output_commodity.commodity_id = recipe.output_commodity_id
		JOIN LATERAL (
			SELECT commodity.name
			FROM production.recipe_inputs AS recipe_input
			JOIN inventory.commodities AS commodity
			  ON commodity.commodity_id = recipe_input.commodity_id
			WHERE recipe_input.recipe_id = recipe.recipe_id
			ORDER BY commodity.name
			LIMIT 1
		) AS input_commodity ON true
		WHERE facility.company_id = $1::uuid
		ORDER BY facility.name, facility.facility_id`, companyID)
	if err != nil {
		return nil, nil, fmt.Errorf("load game snapshot: facilities: %w", err)
	}
	defer rows.Close()
	facilities := make([]SnapshotFacility, 0)
	trace := make([]ProductionTrace, 0)
	for rows.Next() {
		var facility SnapshotFacility
		var utilizationBasisPoints, conditionBasisPoints int
		var nominalCapacityMinor int64
		var scale int16
		var unit string
		var databaseStatus string
		if err := rows.Scan(
			&facility.ID,
			&facility.Name,
			&facility.Type,
			&facility.Location,
			&utilizationBasisPoints,
			&conditionBasisPoints,
			&databaseStatus,
			&facility.InputCommodity,
			&facility.OutputCommodity,
			&nominalCapacityMinor,
			&scale,
			&unit,
		); err != nil {
			return nil, nil, fmt.Errorf("load game snapshot: scan facility: %w", err)
		}
		facility.Utilization = float64(utilizationBasisPoints) / 100
		facility.Change = float64(conditionBasisPoints-10_000) / 100
		facility.Status = snapshotFacilityStatus(databaseStatus)
		facility.Capacity = fixedToDisplay(nominalCapacityMinor, scale)
		facility.CapacityUnit = unit + "/cycle"
		facilities = append(facilities, facility)
		trace = append(trace,
			ProductionTrace{
				ID: facility.ID + ":input", Label: facility.InputCommodity,
				Value: fmt.Sprintf("available at %s", facility.Location), Change: 0, Depth: 1, Status: "input",
			},
			ProductionTrace{
				ID: facility.ID + ":process", Label: facility.Name,
				Value: fmt.Sprintf("%.0f%% utilization", facility.Utilization), Change: facility.Change, Depth: 0, Status: "process",
			},
			ProductionTrace{
				ID: facility.ID + ":output", Label: facility.OutputCommodity,
				Value: fmt.Sprintf("%.3g %s", facility.Capacity, facility.CapacityUnit), Change: 0, Depth: 1, Status: "output",
			},
		)
		if facility.Status == "constrained" {
			trace = append(trace, ProductionTrace{
				ID: facility.ID + ":constraint", Label: "Facility constraint",
				Value: "capacity constrained", Change: facility.Change, Depth: 2, Status: "constraint",
			})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("load game snapshot: facility rows: %w", err)
	}
	return facilities, trace, nil
}

func loadFreight(ctx context.Context, transaction *sql.Tx, companyID string) ([]FreightShipment, error) {
	rows, err := transaction.QueryContext(ctx, `
		SELECT
			delivery.delivery_id::text,
			origin.name,
			destination.name,
			commodity.name,
			delivery.quantity_minor,
			commodity.quantity_scale,
			commodity.unit,
			delivery.status,
			contract.deliver_by
		FROM freight.deliveries AS delivery
		JOIN freight.contracts AS contract
		  ON contract.contract_id = delivery.contract_id
		JOIN freight.routes AS route
		  ON route.route_id = contract.route_id
		JOIN inventory.locations AS origin
		  ON origin.location_id = route.origin_location_id
		JOIN inventory.locations AS destination
		  ON destination.location_id = route.destination_location_id
		JOIN inventory.commodities AS commodity
		  ON commodity.commodity_id = contract.commodity_id
		WHERE (contract.shipper_company_id = $1::uuid OR contract.carrier_company_id = $1::uuid)
		  AND delivery.status IN ('scheduled', 'in_transit', 'delivered')
		ORDER BY contract.deliver_by, delivery.delivery_id`, companyID)
	if err != nil {
		return nil, fmt.Errorf("load game snapshot: freight: %w", err)
	}
	defer rows.Close()
	shipments := make([]FreightShipment, 0)
	for rows.Next() {
		var shipment FreightShipment
		var quantityMinor int64
		var scale int16
		var databaseStatus string
		if err := rows.Scan(
			&shipment.ID,
			&shipment.Origin,
			&shipment.Destination,
			&shipment.Cargo,
			&quantityMinor,
			&scale,
			&shipment.Unit,
			&databaseStatus,
			&shipment.ETA,
		); err != nil {
			return nil, fmt.Errorf("load game snapshot: scan freight: %w", err)
		}
		shipment.Quantity = fixedToDisplay(quantityMinor, scale)
		shipment.Status = snapshotDeliveryStatus(databaseStatus)
		shipments = append(shipments, shipment)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("load game snapshot: freight rows: %w", err)
	}
	return shipments, nil
}

func loadDevices(ctx context.Context, transaction *sql.Tx, actorID string) ([]SnapshotDevice, error) {
	rows, err := transaction.QueryContext(ctx, `
		SELECT device_id::text, name, device_class, status, last_seen_at
		FROM workspace.devices
		WHERE player_id = $1::uuid
		ORDER BY last_seen_at DESC, device_id`, actorID)
	if err != nil {
		return nil, fmt.Errorf("load game snapshot: devices: %w", err)
	}
	defer rows.Close()
	devices := make([]SnapshotDevice, 0)
	for rows.Next() {
		var device SnapshotDevice
		var deviceClass, databaseStatus string
		var lastSeen time.Time
		if err := rows.Scan(&device.ID, &device.Name, &deviceClass, &databaseStatus, &lastSeen); err != nil {
			return nil, fmt.Errorf("load game snapshot: scan device: %w", err)
		}
		device.Status = snapshotDeviceStatus(databaseStatus)
		device.LastSeenAt = &lastSeen
		device.Capabilities = []string{"panel.receive"}
		if deviceClass != "passive" && databaseStatus == "registered" {
			device.Capabilities = append(device.Capabilities, "panel.send")
		}
		devices = append(devices, device)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("load game snapshot: device rows: %w", err)
	}
	return devices, nil
}

func loadPanels(ctx context.Context, transaction *sql.Tx, actorID string) ([]DevicePanel, error) {
	rows, err := transaction.QueryContext(ctx, `
		SELECT panel_definitions
		FROM workspace.views
		WHERE player_id = $1::uuid
		ORDER BY name, view_id`, actorID)
	if err != nil {
		return nil, fmt.Errorf("load game snapshot: panels: %w", err)
	}
	defer rows.Close()
	panels := make([]DevicePanel, 0)
	for rows.Next() {
		var definitions []byte
		if err := rows.Scan(&definitions); err != nil {
			return nil, fmt.Errorf("load game snapshot: scan panels: %w", err)
		}
		var viewPanels []DevicePanel
		if err := json.Unmarshal(definitions, &viewPanels); err != nil {
			return nil, fmt.Errorf("load game snapshot: decode panels: %w", err)
		}
		for index := range viewPanels {
			if viewPanels[index].Status != "ready" && viewPanels[index].Status != "busy" && viewPanels[index].Status != "offline" {
				viewPanels[index].Status = "offline"
			}
		}
		panels = append(panels, viewPanels...)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("load game snapshot: panel rows: %w", err)
	}
	return panels, nil
}

func loadChat(ctx context.Context, transaction *sql.Tx, companyID string) ([]ChatMessage, error) {
	rows, err := transaction.QueryContext(ctx, `
		SELECT
			message.message_id::text,
			message.channel_id::text,
			message.sender_player_id::text,
			player.display_name,
			message.body,
			message.sent_at
		FROM chat.messages AS message
		JOIN chat.channels AS channel
		  ON channel.channel_id = message.channel_id
		JOIN identity.players AS player
		  ON player.player_id = message.sender_player_id
		WHERE channel.channel_type = 'company'
		  AND channel.company_id = $1::uuid
		  AND channel.status = 'active'
		  AND message.removed_at IS NULL
		ORDER BY message.sent_at DESC, message.message_id DESC
		LIMIT 50`, companyID)
	if err != nil {
		return nil, fmt.Errorf("load game snapshot: chat: %w", err)
	}
	defer rows.Close()
	messages := make([]ChatMessage, 0)
	for rows.Next() {
		var message ChatMessage
		if err := rows.Scan(
			&message.ID,
			&message.ChannelID,
			&message.ActorID,
			&message.ActorName,
			&message.Body,
			&message.OccurredAt,
		); err != nil {
			return nil, fmt.Errorf("load game snapshot: scan chat: %w", err)
		}
		message.Kind = "player"
		messages = append(messages, message)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("load game snapshot: chat rows: %w", err)
	}
	sort.SliceStable(messages, func(i, j int) bool { return messages[i].OccurredAt.Before(messages[j].OccurredAt) })
	return messages, nil
}

func loadAlerts(ctx context.Context, transaction *sql.Tx, actorID string) ([]SnapshotAlert, error) {
	rows, err := transaction.QueryContext(ctx, `
		SELECT
			notification.notification_id::text,
			COALESCE(notification.payload->>'severity', 'info'),
			COALESCE(NULLIF(notification.body, ''), notification.title),
			notification.created_at
		FROM alerts.notifications AS notification
		WHERE notification.player_id = $1::uuid
		ORDER BY notification.created_at DESC, notification.notification_id DESC
		LIMIT 50`, actorID)
	if err != nil {
		return nil, fmt.Errorf("load game snapshot: alerts: %w", err)
	}
	defer rows.Close()
	alerts := make([]SnapshotAlert, 0)
	for rows.Next() {
		var alert SnapshotAlert
		if err := rows.Scan(&alert.ID, &alert.Severity, &alert.Summary, &alert.OccurredAt); err != nil {
			return nil, fmt.Errorf("load game snapshot: scan alert: %w", err)
		}
		if alert.Severity != "info" && alert.Severity != "warning" && alert.Severity != "critical" {
			alert.Severity = "info"
		}
		alerts = append(alerts, alert)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("load game snapshot: alert rows: %w", err)
	}
	return alerts, nil
}

func loadAudit(ctx context.Context, transaction *sql.Tx, actorID, companyID string) ([]AuditEntry, error) {
	rows, err := transaction.QueryContext(ctx, `
		SELECT
			command_record.command_id::text,
			COALESCE(player.display_name, command_record.actor_id),
			command_record.command_type,
			COALESCE(
				command_record.result #>> '{payload,compensationId}',
				command_record.result #>> '{payload,tradeId}',
				command_record.result #>> '{payload,orderId}',
				command_record.result #>> '{payload,jobId}',
				command_record.result #>> '{payload,deliveryId}',
				command_record.result #>> '{payload,messageId}',
				command_record.result #>> '{payload,deviceId}',
				command_record.command_id::text
			),
			command_record.status,
			COALESCE(command_record.committed_at, command_record.received_at)
		FROM platform.command_log AS command_record
		LEFT JOIN identity.players AS player
		  ON player.player_id::text = command_record.actor_id
		WHERE command_record.actor_id = $1
		   OR command_record.company_id = $2::uuid
		ORDER BY COALESCE(command_record.committed_at, command_record.received_at) DESC,
		         command_record.command_id DESC
		LIMIT 50`, actorID, companyID)
	if err != nil {
		return nil, fmt.Errorf("load game snapshot: operator audit: %w", err)
	}
	defer rows.Close()
	audit := make([]AuditEntry, 0)
	for rows.Next() {
		var entry AuditEntry
		var status string
		if err := rows.Scan(
			&entry.ID,
			&entry.ActorName,
			&entry.Action,
			&entry.Target,
			&status,
			&entry.OccurredAt,
		); err != nil {
			return nil, fmt.Errorf("load game snapshot: scan operator audit: %w", err)
		}
		entry.Outcome = snapshotAuditOutcome(status, entry.Action)
		audit = append(audit, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("load game snapshot: operator audit rows: %w", err)
	}
	return audit, nil
}

func marketIndices(markets []SnapshotMarket) []MarketIndex {
	indices := make([]MarketIndex, 0, len(markets))
	for _, market := range markets {
		indices = append(indices, MarketIndex{
			Name:   strings.TrimSpace(market.Location + " " + market.Commodity),
			Value:  math.Round(market.LastPrice*6.95*10) / 10,
			Change: market.Change24Hour,
		})
	}
	return indices
}
