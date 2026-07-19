package gamepostgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"

	"ascent/internal/identity"
	"ascent/internal/ledger"
	domainmarkets "ascent/internal/markets"
	"ascent/internal/platform/ids"
	protocol "ascent/protocol/gen/go"
)

type placeOrderPayload struct {
	MarketID  string      `json:"marketId"`
	Side      string      `json:"side"`
	OrderType string      `json:"orderType"`
	Price     json.Number `json:"price"`
	Quantity  json.Number `json:"quantity"`
}

type cancelOrderPayload struct {
	OrderID string `json:"orderId"`
}

type marketRecord struct {
	id            string
	locationID    string
	commodityID   string
	currency      string
	priceScale    int64
	quantityScale int64
	lotSize       int64
}

type persistedOrder struct {
	id        string
	commandID string
	companyID string
	side      string
	price     int64
	remaining int64
	sequence  uint64
}

func (s *Service) placeOrder(
	ctx context.Context,
	transaction *sql.Tx,
	actor identity.Actor,
	command protocol.CommandEnvelope,
) (commandOutcome, *commandRejection, error) {
	_, companyVersion, rejection, err := s.authorizeCompany(
		ctx,
		transaction,
		actor,
		command,
		"owner",
		"operator",
		"trader",
	)
	if rejection != nil || err != nil {
		return commandOutcome{}, rejection, err
	}
	var payload placeOrderPayload
	if rejection := decodePayload(command.Payload, &payload); rejection != nil {
		return commandOutcome{}, rejection, nil
	}
	if !ids.IsUUID(payload.MarketID) || payload.OrderType != "limit" ||
		(payload.Side != "buy" && payload.Side != "sell") {
		return commandOutcome{}, reject("INVALID_ORDER", "Choose a valid market, side, limit price, and quantity."), nil
	}
	market, rejection, err := loadMarket(ctx, transaction, payload.MarketID)
	if rejection != nil || err != nil {
		return commandOutcome{}, rejection, err
	}
	priceMinor, ok := decimalMinorUnits(payload.Price, market.priceScale)
	if !ok {
		return commandOutcome{}, reject("INVALID_PRICE", "Price has too many decimals or is outside the supported range."), nil
	}
	quantityMinor, ok := decimalMinorUnits(payload.Quantity, market.quantityScale)
	if !ok {
		return commandOutcome{}, reject("INVALID_QUANTITY", "Quantity is not an exact supported market amount."), nil
	}
	if quantityMinor%market.lotSize != 0 {
		return commandOutcome{}, reject("INVALID_QUANTITY", "Quantity must align with the market lot size."), nil
	}
	quoteAmount, ok := quoteAmount(priceMinor, quantityMinor, market.quantityScale)
	if !ok {
		return commandOutcome{}, reject("INVALID_ORDER", "The order value is outside the supported range."), nil
	}

	companyID := *command.CompanyId
	orderID, err := ids.NewUUID()
	if err != nil {
		return commandOutcome{}, nil, err
	}
	reservationID, err := ids.NewUUID()
	if err != nil {
		return commandOutcome{}, nil, err
	}
	var (
		accountID string
		holdingID string
	)
	if payload.Side == "buy" {
		accountID, rejection, err = reserveCashCapacity(ctx, transaction, companyID, market.currency, quoteAmount)
	} else {
		holdingID, rejection, err = reserveInventoryCapacity(
			ctx,
			transaction,
			companyID,
			market.locationID,
			market.commodityID,
			quantityMinor,
		)
	}
	if rejection != nil || err != nil {
		return commandOutcome{}, rejection, err
	}

	now := s.clock()
	var prioritySequence int64
	err = transaction.QueryRowContext(
		ctx,
		`INSERT INTO markets.orders (
			order_id,
			command_id,
			market_id,
			company_id,
			side,
			limit_price_minor,
			original_quantity_minor,
			remaining_quantity_minor,
			status,
			created_at,
			updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $7, 'open', $8, $8)
		RETURNING priority_sequence`,
		orderID,
		command.CommandId,
		market.id,
		companyID,
		payload.Side,
		priceMinor,
		quantityMinor,
		now,
	).Scan(&prioritySequence)
	if err != nil {
		return commandOutcome{}, nil, fmt.Errorf("insert limit order: %w", err)
	}
	if payload.Side == "buy" {
		_, err = transaction.ExecContext(
			ctx,
			`INSERT INTO markets.order_reservations (
				reservation_id, order_id, company_id, resource_type,
				ledger_account_id, currency, original_reserved_minor,
				remaining_reserved_minor, status
			) VALUES ($1, $2, $3, 'cash', $4, $5, $6, $6, 'active')`,
			reservationID,
			orderID,
			companyID,
			accountID,
			market.currency,
			quoteAmount,
		)
	} else {
		_, err = transaction.ExecContext(
			ctx,
			`INSERT INTO markets.order_reservations (
				reservation_id, order_id, company_id, resource_type,
				holding_id, original_reserved_minor,
				remaining_reserved_minor, status
			) VALUES ($1, $2, $3, 'inventory', $4, $5, $5, 'active')`,
			reservationID,
			orderID,
			companyID,
			holdingID,
			quantityMinor,
		)
	}
	if err != nil {
		return commandOutcome{}, nil, fmt.Errorf("insert order reservation: %w", err)
	}

	execution, finalOrders, err := replayAndMatch(ctx, transaction, market, orderID)
	if errors.Is(err, domainmarkets.ErrSelfTrade) {
		return commandOutcome{}, reject("SELF_TRADE", "An order cannot trade against your own company."), nil
	}
	if err != nil {
		return commandOutcome{}, nil, err
	}
	if err := persistMatchedOrders(ctx, transaction, market, finalOrders, execution.Trades); err != nil {
		return commandOutcome{}, nil, err
	}
	tradeIDs, err := s.persistTrades(ctx, transaction, command, market, execution.Trades, now)
	if err != nil {
		return commandOutcome{}, nil, err
	}
	if err := incrementCounterpartyVersions(
		ctx,
		transaction,
		companyID,
		execution.Trades,
	); err != nil {
		return commandOutcome{}, nil, err
	}
	orderEvent, err := appendEvent(
		ctx,
		transaction,
		&command.CommandId,
		"public.market."+market.id,
		"ORDER_PLACED",
		map[string]any{
			"orderId":       orderID,
			"companyId":     companyID,
			"side":          payload.Side,
			"priceMinor":    priceMinor,
			"quantityMinor": quantityMinor,
			"tradeIds":      tradeIDs,
		},
		now,
	)
	if err != nil {
		return commandOutcome{}, nil, err
	}
	if err := incrementCompanyVersion(ctx, transaction, companyID, companyVersion); err != nil {
		return commandOutcome{}, nil, err
	}
	return commandOutcome{payload: map[string]any{
		"orderId":       orderID,
		"tradeIds":      tradeIDs,
		"eventSequence": orderEvent.Sequence,
		"priority":      prioritySequence,
	}}, nil, nil
}

func (s *Service) cancelOrder(
	ctx context.Context,
	transaction *sql.Tx,
	actor identity.Actor,
	command protocol.CommandEnvelope,
) (commandOutcome, *commandRejection, error) {
	_, companyVersion, rejection, err := s.authorizeCompany(
		ctx,
		transaction,
		actor,
		command,
		"owner",
		"operator",
		"trader",
	)
	if rejection != nil || err != nil {
		return commandOutcome{}, rejection, err
	}
	var payload cancelOrderPayload
	if rejection := decodePayload(command.Payload, &payload); rejection != nil {
		return commandOutcome{}, rejection, nil
	}
	if !ids.IsUUID(payload.OrderID) {
		return commandOutcome{}, reject("INVALID_ORDER", "Choose a valid open order."), nil
	}
	var (
		companyID string
		marketID  string
		side      string
		status    string
		remaining int64
	)
	err = transaction.QueryRowContext(
		ctx,
		`SELECT company_id, market_id, side, status, remaining_quantity_minor
		   FROM markets.orders
		  WHERE order_id = $1
		  FOR UPDATE`,
		payload.OrderID,
	).Scan(&companyID, &marketID, &side, &status, &remaining)
	if errors.Is(err, sql.ErrNoRows) {
		return commandOutcome{}, reject("ORDER_NOT_FOUND", "That order does not exist."), nil
	}
	if err != nil {
		return commandOutcome{}, nil, fmt.Errorf("lock order for cancellation: %w", err)
	}
	if companyID != *command.CompanyId {
		return commandOutcome{}, reject("FORBIDDEN", "The order belongs to another company."), nil
	}
	if status != "open" && status != "partially_filled" {
		return commandOutcome{}, reject("ORDER_CLOSED", "That order is already in a terminal state."), nil
	}
	var (
		reservationID string
		holdingID     sql.NullString
	)
	err = transaction.QueryRowContext(
		ctx,
		`SELECT reservation_id, holding_id
		   FROM markets.order_reservations
		  WHERE order_id = $1 AND status = 'active'
		  FOR UPDATE`,
		payload.OrderID,
	).Scan(&reservationID, &holdingID)
	if err != nil {
		return commandOutcome{}, nil, fmt.Errorf("lock order reservation: %w", err)
	}
	if side == "sell" {
		result, err := transaction.ExecContext(
			ctx,
			`UPDATE inventory.holdings
			    SET reserved_quantity_minor = reserved_quantity_minor - $2,
			        version = version + 1,
			        updated_at = clock_timestamp()
			  WHERE holding_id = $1
			    AND reserved_quantity_minor >= $2`,
			holdingID.String,
			remaining,
		)
		if err != nil {
			return commandOutcome{}, nil, fmt.Errorf("release inventory reservation: %w", err)
		}
		affected, _ := result.RowsAffected()
		if affected != 1 {
			return commandOutcome{}, nil, errors.New("inventory reservation could not be released")
		}
	}
	if _, err := transaction.ExecContext(
		ctx,
		`UPDATE markets.order_reservations
		    SET remaining_reserved_minor = 0,
		        status = 'released',
		        version = version + 1,
		        updated_at = clock_timestamp()
		  WHERE reservation_id = $1`,
		reservationID,
	); err != nil {
		return commandOutcome{}, nil, fmt.Errorf("release order reservation: %w", err)
	}
	if _, err := transaction.ExecContext(
		ctx,
		`UPDATE markets.orders
		    SET status = 'cancelled',
		        version = version + 1,
		        updated_at = clock_timestamp()
		  WHERE order_id = $1`,
		payload.OrderID,
	); err != nil {
		return commandOutcome{}, nil, fmt.Errorf("cancel order: %w", err)
	}
	now := s.clock()
	event, err := appendEvent(
		ctx,
		transaction,
		&command.CommandId,
		"public.market."+marketID,
		"ORDER_CANCELLED",
		map[string]any{"orderId": payload.OrderID, "releasedQuantityMinor": remaining},
		now,
	)
	if err != nil {
		return commandOutcome{}, nil, err
	}
	if err := incrementCompanyVersion(ctx, transaction, companyID, companyVersion); err != nil {
		return commandOutcome{}, nil, err
	}
	return commandOutcome{payload: map[string]any{
		"orderId":       payload.OrderID,
		"eventSequence": event.Sequence,
	}}, nil, nil
}

func loadMarket(
	ctx context.Context,
	transaction *sql.Tx,
	marketID string,
) (marketRecord, *commandRejection, error) {
	var (
		market     marketRecord
		priceScale int
		qtyScale   int
		status     string
	)
	err := transaction.QueryRowContext(
		ctx,
		`SELECT market.market_id,
		        market.location_id,
		        market.commodity_id,
		        market.currency,
		        market.price_scale,
		        commodity.quantity_scale,
		        market.lot_size_minor,
		        market.status
		   FROM markets.markets AS market
		   JOIN inventory.commodities AS commodity
		     ON commodity.commodity_id = market.commodity_id
		  WHERE market.market_id = $1
		  FOR SHARE OF market, commodity`,
		marketID,
	).Scan(
		&market.id,
		&market.locationID,
		&market.commodityID,
		&market.currency,
		&priceScale,
		&qtyScale,
		&market.lotSize,
		&status,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return marketRecord{}, reject("MARKET_NOT_FOUND", "That market does not exist."), nil
	}
	if err != nil {
		return marketRecord{}, nil, fmt.Errorf("load market: %w", err)
	}
	if status != "open" {
		return marketRecord{}, reject("MARKET_HALTED", "That market is not accepting orders."), nil
	}
	market.priceScale = powerOfTen(priceScale)
	market.quantityScale = powerOfTen(qtyScale)
	if market.priceScale == 0 || market.quantityScale == 0 || market.lotSize <= 0 {
		return marketRecord{}, nil, errors.New("market scale is invalid")
	}
	return market, nil, nil
}

func reserveCashCapacity(
	ctx context.Context,
	transaction *sql.Tx,
	companyID, currency string,
	required int64,
) (string, *commandRejection, error) {
	var (
		accountID string
		balance   int64
		reserved  int64
	)
	err := transaction.QueryRowContext(
		ctx,
		`SELECT account_id
		   FROM ledger.accounts
		  WHERE company_id = $1
		    AND currency = $2
		    AND code = 'CASH'
		    AND status = 'active'
		  FOR UPDATE`,
		companyID,
		currency,
	).Scan(&accountID)
	if err != nil {
		return "", nil, fmt.Errorf("lock cash account: %w", err)
	}
	err = transaction.QueryRowContext(
		ctx,
		`SELECT COALESCE(sum(
		          CASE side WHEN 'debit' THEN amount_minor ELSE -amount_minor END
		        ), 0)::bigint
		   FROM ledger.entries
		  WHERE account_id = $1`,
		accountID,
	).Scan(&balance)
	if err != nil {
		return "", nil, fmt.Errorf("load cash balance: %w", err)
	}
	err = transaction.QueryRowContext(
		ctx,
		`SELECT COALESCE(sum(remaining_reserved_minor), 0)::bigint
		   FROM markets.order_reservations
		  WHERE company_id = $1
		    AND resource_type = 'cash'
		    AND currency = $2
		    AND status = 'active'`,
		companyID,
		currency,
	).Scan(&reserved)
	if err != nil {
		return "", nil, fmt.Errorf("load cash reservations: %w", err)
	}
	if required > balance-reserved {
		return "", reject("INSUFFICIENT_CASH", "Available cash is below the order's limit-price exposure."), nil
	}
	return accountID, nil, nil
}

func reserveInventoryCapacity(
	ctx context.Context,
	transaction *sql.Tx,
	companyID, locationID, commodityID string,
	required int64,
) (string, *commandRejection, error) {
	var (
		holdingID string
		available int64
	)
	err := transaction.QueryRowContext(
		ctx,
		`SELECT holding_id, available_quantity_minor
		   FROM inventory.holdings
		  WHERE company_id = $1
		    AND location_id = $2
		    AND commodity_id = $3
		  FOR UPDATE`,
		companyID,
		locationID,
		commodityID,
	).Scan(&holdingID, &available)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && available < required) {
		return "", reject("INSUFFICIENT_INVENTORY", "Available inventory is below the sell quantity."), nil
	}
	if err != nil {
		return "", nil, fmt.Errorf("lock inventory holding: %w", err)
	}
	if _, err := transaction.ExecContext(
		ctx,
		`UPDATE inventory.holdings
		    SET reserved_quantity_minor = reserved_quantity_minor + $2,
		        version = version + 1,
		        updated_at = clock_timestamp()
		  WHERE holding_id = $1`,
		holdingID,
		required,
	); err != nil {
		return "", nil, fmt.Errorf("reserve inventory: %w", err)
	}
	return holdingID, nil, nil
}

func replayAndMatch(
	ctx context.Context,
	transaction *sql.Tx,
	market marketRecord,
	newOrderID string,
) (domainmarkets.Execution, map[string]domainmarkets.Order, error) {
	rows, err := transaction.QueryContext(
		ctx,
		`SELECT order_id, command_id, company_id, side,
		        limit_price_minor, remaining_quantity_minor, priority_sequence
		   FROM markets.orders
		  WHERE market_id = $1
		    AND status IN ('open', 'partially_filled')
		  ORDER BY priority_sequence
		  FOR UPDATE`,
		market.id,
	)
	if err != nil {
		return domainmarkets.Execution{}, nil, fmt.Errorf("load order book for matching: %w", err)
	}
	defer rows.Close()
	var orders []persistedOrder
	for rows.Next() {
		var sequence int64
		var order persistedOrder
		if err := rows.Scan(
			&order.id,
			&order.commandID,
			&order.companyID,
			&order.side,
			&order.price,
			&order.remaining,
			&sequence,
		); err != nil {
			return domainmarkets.Execution{}, nil, err
		}
		order.sequence = uint64(sequence)
		orders = append(orders, order)
	}
	if err := rows.Err(); err != nil {
		return domainmarkets.Execution{}, nil, err
	}
	book, err := domainmarkets.NewBook(domainmarkets.Market{
		ID:            market.id,
		ProductID:     market.commodityID,
		Currency:      market.currency,
		LocationID:    market.locationID,
		QuantityScale: market.quantityScale,
		LotSize:       market.lotSize,
	})
	if err != nil {
		return domainmarkets.Execution{}, nil, err
	}
	var target domainmarkets.Execution
	for _, order := range orders {
		next, execution, err := book.Submit(domainmarkets.OrderRequest{
			ID:          order.id,
			OperationID: order.commandID,
			CompanyID:   order.companyID,
			Side:        domainmarkets.Side(order.side),
			Price:       order.price,
			Quantity:    order.remaining,
			Sequence:    order.sequence,
		}, math.MaxInt64)
		if err != nil {
			return domainmarkets.Execution{}, nil, err
		}
		book = next
		if order.id == newOrderID {
			target = execution
		}
	}
	final := make(map[string]domainmarkets.Order)
	for _, order := range book.Orders() {
		final[order.ID] = order
	}
	return target, final, nil
}

func persistMatchedOrders(
	ctx context.Context,
	transaction *sql.Tx,
	market marketRecord,
	final map[string]domainmarkets.Order,
	trades []domainmarkets.Trade,
) error {
	if len(trades) == 0 {
		return nil
	}
	touched := make(map[string]struct{})
	for _, trade := range trades {
		touched[trade.BuyOrderID] = struct{}{}
		touched[trade.SellOrderID] = struct{}{}
	}
	orderIDs := make([]string, 0, len(touched))
	for orderID := range touched {
		orderIDs = append(orderIDs, orderID)
	}
	sort.Strings(orderIDs)
	for _, orderID := range orderIDs {
		order := final[orderID]
		status := string(order.Status)
		if status == "canceled" {
			status = "cancelled"
		}
		if _, err := transaction.ExecContext(
			ctx,
			`UPDATE markets.orders
			    SET remaining_quantity_minor = $2,
			        status = $3,
			        version = version + 1,
			        updated_at = clock_timestamp()
			  WHERE order_id = $1`,
			order.ID,
			order.RemainingQuantity,
			status,
		); err != nil {
			return fmt.Errorf("persist matched order: %w", err)
		}
		reservationRemaining := order.RemainingQuantity
		if order.Side == domainmarkets.SideBuy {
			var ok bool
			reservationRemaining, ok = quoteAmount(order.Price, order.RemainingQuantity, market.quantityScale)
			if !ok {
				return errors.New("matched reservation overflow")
			}
		}
		reservationStatus := "active"
		if reservationRemaining == 0 {
			reservationStatus = "consumed"
		}
		var holdingID sql.NullString
		err := transaction.QueryRowContext(
			ctx,
			`UPDATE markets.order_reservations
			    SET remaining_reserved_minor = $2,
			        status = $3,
			        version = version + 1,
			        updated_at = clock_timestamp()
			  WHERE order_id = $1 AND status = 'active'
			  RETURNING holding_id`,
			order.ID,
			reservationRemaining,
			reservationStatus,
		).Scan(&holdingID)
		if err != nil {
			return fmt.Errorf("persist matched reservation: %w", err)
		}
		if order.Side == domainmarkets.SideSell {
			var filled int64
			for _, trade := range trades {
				if trade.SellOrderID == order.ID {
					filled += trade.Quantity
				}
			}
			if _, err := transaction.ExecContext(
				ctx,
				`UPDATE inventory.holdings
				    SET reserved_quantity_minor = reserved_quantity_minor - $2,
				        version = version + 1,
				        updated_at = clock_timestamp()
				  WHERE holding_id = $1`,
				holdingID.String,
				filled,
			); err != nil {
				return fmt.Errorf("consume holding reservation: %w", err)
			}
		}
	}
	return nil
}

type journalAmounts struct {
	bought   int64
	sold     int64
	soldCost int64
}

type stagedTrade struct {
	trade      domainmarkets.Trade
	tradeID    string
	movementID string
}

func (s *Service) persistTrades(
	ctx context.Context,
	transaction *sql.Tx,
	command protocol.CommandEnvelope,
	market marketRecord,
	trades []domainmarkets.Trade,
	now time.Time,
) ([]string, error) {
	if len(trades) == 0 {
		return nil, nil
	}
	amounts := make(map[string]journalAmounts)
	staged := make([]stagedTrade, 0, len(trades))
	for _, trade := range trades {
		tradeID, err := ids.NewUUID()
		if err != nil {
			return nil, err
		}
		movementID, err := ids.NewUUID()
		if err != nil {
			return nil, err
		}
		var (
			sellerHoldingID string
			sellerQuantity  int64
			sellerCostBasis int64
		)
		err = transaction.QueryRowContext(
			ctx,
			`SELECT holding_id, quantity_minor, cost_basis_minor
			   FROM inventory.holdings
			  WHERE company_id = $1
			    AND location_id = $2
			    AND commodity_id = $3
			  FOR UPDATE`,
			trade.SellerID,
			market.locationID,
			market.commodityID,
		).Scan(&sellerHoldingID, &sellerQuantity, &sellerCostBasis)
		if err != nil {
			return nil, fmt.Errorf("lock seller holding: %w", err)
		}
		soldCost, ok := proportionalCost(sellerCostBasis, trade.Quantity, sellerQuantity)
		if !ok {
			return nil, errors.New("seller inventory cost basis overflow")
		}
		buyerHoldingID, err := ensureBuyerHolding(
			ctx,
			transaction,
			trade.BuyerID,
			market.locationID,
			market.commodityID,
		)
		if err != nil {
			return nil, err
		}
		sellerResult, err := transaction.ExecContext(
			ctx,
			`UPDATE inventory.holdings
			    SET quantity_minor = quantity_minor - $2,
			        cost_basis_minor = cost_basis_minor - $3,
			        version = version + 1,
			        updated_at = clock_timestamp()
			  WHERE holding_id = $1
			    AND quantity_minor >= $2
			    AND cost_basis_minor >= $3`,
			sellerHoldingID,
			trade.Quantity,
			soldCost,
		)
		if err != nil {
			return nil, fmt.Errorf("settle seller inventory: %w", err)
		}
		affected, _ := sellerResult.RowsAffected()
		if affected != 1 {
			return nil, errors.New("seller inventory changed before settlement")
		}
		if _, err := transaction.ExecContext(
			ctx,
			`UPDATE inventory.holdings
			    SET quantity_minor = quantity_minor + $2,
			        cost_basis_minor = cost_basis_minor + $3,
			        version = version + 1,
			        updated_at = clock_timestamp()
			  WHERE holding_id = $1`,
			buyerHoldingID,
			trade.Quantity,
			trade.QuoteAmount,
		); err != nil {
			return nil, fmt.Errorf("settle buyer inventory: %w", err)
		}
		if _, err := transaction.ExecContext(
			ctx,
			`INSERT INTO inventory.movements (
				movement_id, command_id, movement_kind, source_id,
				from_holding_id, to_holding_id, quantity_minor,
				occurred_at, reason
			) VALUES ($1, $2, 'settlement', $3, $4, $5, $6, $7, $8)`,
			movementID,
			command.CommandId,
			tradeID,
			sellerHoldingID,
			buyerHoldingID,
			trade.Quantity,
			now,
			"Immediate spot-market settlement",
		); err != nil {
			return nil, fmt.Errorf("record inventory settlement: %w", err)
		}

		buyer := amounts[trade.BuyerID]
		buyer.bought += trade.QuoteAmount
		amounts[trade.BuyerID] = buyer
		seller := amounts[trade.SellerID]
		seller.sold += trade.QuoteAmount
		seller.soldCost += soldCost
		amounts[trade.SellerID] = seller
		staged = append(staged, stagedTrade{
			trade:      trade,
			tradeID:    tradeID,
			movementID: movementID,
		})
	}
	journals, err := postTradeJournals(ctx, transaction, command, market.currency, amounts, now)
	if err != nil {
		return nil, err
	}

	tradeIDs := make([]string, 0, len(staged))
	for _, stagedTrade := range staged {
		trade := stagedTrade.trade
		event, err := appendEvent(
			ctx,
			transaction,
			&command.CommandId,
			"public.market."+market.id,
			"TRADE_EXECUTED",
			map[string]any{
				"tradeId":         stagedTrade.tradeID,
				"priceMinor":      trade.Price,
				"quantityMinor":   trade.Quantity,
				"buyerCompanyId":  trade.BuyerID,
				"sellerCompanyId": trade.SellerID,
			},
			now,
		)
		if err != nil {
			return nil, err
		}
		if _, err := transaction.ExecContext(
			ctx,
			`INSERT INTO markets.trades (
				trade_id, market_id, buy_order_id, sell_order_id,
				buyer_company_id, seller_company_id, price_minor,
				quantity_minor, inventory_movement_id, buyer_journal_id,
				seller_journal_id, event_id, executed_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)`,
			stagedTrade.tradeID,
			market.id,
			trade.BuyOrderID,
			trade.SellOrderID,
			trade.BuyerID,
			trade.SellerID,
			trade.Price,
			trade.Quantity,
			stagedTrade.movementID,
			journals[trade.BuyerID],
			journals[trade.SellerID],
			*event.EventId,
			now,
		); err != nil {
			return nil, fmt.Errorf("record trade: %w", err)
		}
		tradeIDs = append(tradeIDs, stagedTrade.tradeID)
	}
	return tradeIDs, nil
}

func postTradeJournals(
	ctx context.Context,
	transaction *sql.Tx,
	command protocol.CommandEnvelope,
	currency string,
	amounts map[string]journalAmounts,
	now time.Time,
) (map[string]string, error) {
	companyIDs := make([]string, 0, len(amounts))
	for companyID := range amounts {
		companyIDs = append(companyIDs, companyID)
	}
	sort.Strings(companyIDs)
	result := make(map[string]string, len(companyIDs))
	for _, companyID := range companyIDs {
		amount := amounts[companyID]
		accounts, err := loadAccountIDs(ctx, transaction, companyID)
		if err != nil {
			return nil, err
		}
		journalID, err := ids.NewUUID()
		if err != nil {
			return nil, err
		}
		var entries []ledger.Entry
		if amount.bought > 0 {
			entries = append(entries,
				ledger.Entry{AccountID: accounts["INVENTORY"], Currency: currency, Amount: amount.bought},
				ledger.Entry{AccountID: accounts["CASH"], Currency: currency, Amount: -amount.bought},
			)
		}
		if amount.sold > 0 {
			entries = append(entries,
				ledger.Entry{AccountID: accounts["CASH"], Currency: currency, Amount: amount.sold},
				ledger.Entry{AccountID: accounts["SALES"], Currency: currency, Amount: -amount.sold},
			)
			if amount.soldCost > 0 {
				entries = append(entries,
					ledger.Entry{AccountID: accounts["COGS"], Currency: currency, Amount: amount.soldCost},
					ledger.Entry{AccountID: accounts["INVENTORY"], Currency: currency, Amount: -amount.soldCost},
				)
			}
		}
		journal, err := ledger.NewJournal(ledger.JournalDraft{
			ID:          journalID,
			OperationID: command.CommandId,
			Description: "Atomic spot-market settlement",
			Entries:     entries,
		})
		if err != nil {
			return nil, fmt.Errorf("validate trade journal: %w", err)
		}
		if _, err := transaction.ExecContext(
			ctx,
			`INSERT INTO ledger.journals (
				journal_id, company_id, currency, command_id, source_type,
				source_id, description, occurred_at, posted_at
			) VALUES ($1, $2, $3, $4, 'trade', $4, $5, $6, $6)`,
			journalID,
			companyID,
			currency,
			command.CommandId,
			journal.Description(),
			now,
		); err != nil {
			return nil, fmt.Errorf("insert trade journal: %w", err)
		}
		for _, entry := range journal.Entries() {
			entryID, err := ids.NewUUID()
			if err != nil {
				return nil, err
			}
			side := "debit"
			value := entry.Amount
			if value < 0 {
				side = "credit"
				value = -value
			}
			if _, err := transaction.ExecContext(
				ctx,
				`INSERT INTO ledger.entries (
					entry_id, journal_id, account_id, company_id,
					currency, side, amount_minor, memo
				) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
				entryID,
				journalID,
				entry.AccountID,
				companyID,
				entry.Currency,
				side,
				value,
				"Spot-market settlement",
			); err != nil {
				return nil, fmt.Errorf("insert trade journal entry: %w", err)
			}
		}
		result[companyID] = journalID
	}
	return result, nil
}

func loadAccountIDs(
	ctx context.Context,
	transaction *sql.Tx,
	companyID string,
) (map[string]string, error) {
	rows, err := transaction.QueryContext(
		ctx,
		`SELECT code, account_id
		   FROM ledger.accounts
		  WHERE company_id = $1
		    AND code IN ('CASH', 'INVENTORY', 'SALES', 'COGS')`,
		companyID,
	)
	if err != nil {
		return nil, fmt.Errorf("load settlement accounts: %w", err)
	}
	defer rows.Close()
	accounts := make(map[string]string)
	for rows.Next() {
		var code, accountID string
		if err := rows.Scan(&code, &accountID); err != nil {
			return nil, err
		}
		accounts[code] = accountID
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, code := range []string{"CASH", "INVENTORY", "SALES", "COGS"} {
		if accounts[code] == "" {
			return nil, fmt.Errorf("company %s is missing account %s", companyID, code)
		}
	}
	return accounts, nil
}

func ensureBuyerHolding(
	ctx context.Context,
	transaction *sql.Tx,
	companyID, locationID, commodityID string,
) (string, error) {
	var holdingID string
	err := transaction.QueryRowContext(
		ctx,
		`SELECT holding_id
		   FROM inventory.holdings
		  WHERE company_id = $1
		    AND location_id = $2
		    AND commodity_id = $3
		  FOR UPDATE`,
		companyID,
		locationID,
		commodityID,
	).Scan(&holdingID)
	if err == nil {
		return holdingID, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("load buyer holding: %w", err)
	}
	holdingID, err = ids.NewUUID()
	if err != nil {
		return "", err
	}
	if _, err := transaction.ExecContext(
		ctx,
		`INSERT INTO inventory.holdings (
			holding_id, company_id, location_id, commodity_id,
			quantity_minor, reserved_quantity_minor, cost_basis_minor
		) VALUES ($1, $2, $3, $4, 0, 0, 0)`,
		holdingID,
		companyID,
		locationID,
		commodityID,
	); err != nil {
		return "", fmt.Errorf("create buyer holding: %w", err)
	}
	return holdingID, nil
}

func quoteAmount(price, quantity, quantityScale int64) (int64, bool) {
	if price <= 0 || quantity < 0 || quantityScale <= 0 {
		return 0, false
	}
	if quantity != 0 && price > math.MaxInt64/quantity {
		return 0, false
	}
	product := price * quantity
	if product%quantityScale != 0 {
		return 0, false
	}
	return product / quantityScale, true
}

func powerOfTen(exponent int) int64 {
	if exponent < 0 || exponent > 9 {
		return 0
	}
	value := int64(1)
	for range exponent {
		value *= 10
	}
	return value
}

func incrementCounterpartyVersions(
	ctx context.Context,
	transaction *sql.Tx,
	actingCompanyID string,
	trades []domainmarkets.Trade,
) error {
	companySet := make(map[string]struct{})
	for _, trade := range trades {
		if trade.BuyerID != actingCompanyID {
			companySet[trade.BuyerID] = struct{}{}
		}
		if trade.SellerID != actingCompanyID {
			companySet[trade.SellerID] = struct{}{}
		}
	}
	companyIDs := make([]string, 0, len(companySet))
	for companyID := range companySet {
		companyIDs = append(companyIDs, companyID)
	}
	sort.Strings(companyIDs)
	for _, companyID := range companyIDs {
		result, err := transaction.ExecContext(
			ctx,
			`UPDATE companies.companies
			    SET version = version + 1,
			        updated_at = clock_timestamp()
			  WHERE company_id = $1 AND status = 'active'`,
			companyID,
		)
		if err != nil {
			return fmt.Errorf("advance counterparty company version: %w", err)
		}
		affected, _ := result.RowsAffected()
		if affected != 1 {
			return fmt.Errorf("counterparty company %s is unavailable", companyID)
		}
	}
	return nil
}
