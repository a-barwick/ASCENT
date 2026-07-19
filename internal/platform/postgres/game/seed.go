package gamepostgres

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"ascent/internal/fixtures"
	protocol "ascent/protocol/gen/go"
)

const (
	SeededActorIndex   = 1
	SeededDisplayName  = "Mara Vance"
	seedCurrency       = "CR"
	seedQuantityScale  = int16(3)
	seedLotSizeMinor   = int64(1_000)
	seedWaterCommodity = 1
	seedLOXCommodity   = 2
	seedMoonLocation   = 1
	seedOrbitLocation  = 2
	seedWaterMarket    = 1
	seedLOXMarket      = 2
)

var (
	SeededActorID   = fixtures.SeededPlayerID(SeededActorIndex)
	SeededCompanyID = fixtures.SeededCompanyID(SeededActorIndex)
)

type seedBookOrder struct {
	index       int
	company     int
	side        string
	priceMinor  int64
	quantityMin int64
	createdAt   time.Time
}

var waterBook = []seedBookOrder{
	{index: 1, company: 1, side: "buy", priceMinor: 30_780, quantityMin: 120_000, createdAt: fixtures.GeneratedAt.Add(-6 * time.Minute)},
	{index: 2, company: 2, side: "buy", priceMinor: 30_725, quantityMin: 90_000, createdAt: fixtures.GeneratedAt.Add(-5 * time.Minute)},
	{index: 3, company: 3, side: "buy", priceMinor: 30_610, quantityMin: 140_000, createdAt: fixtures.GeneratedAt.Add(-4 * time.Minute)},
	{index: 4, company: 4, side: "buy", priceMinor: 30_540, quantityMin: 180_000, createdAt: fixtures.GeneratedAt.Add(-3 * time.Minute)},
	{index: 5, company: 5, side: "sell", priceMinor: 30_875, quantityMin: 95_000, createdAt: fixtures.GeneratedAt.Add(-6 * time.Minute)},
	{index: 6, company: 6, side: "sell", priceMinor: 30_920, quantityMin: 110_000, createdAt: fixtures.GeneratedAt.Add(-5 * time.Minute)},
	{index: 7, company: 7, side: "sell", priceMinor: 31_000, quantityMin: 150_000, createdAt: fixtures.GeneratedAt.Add(-4 * time.Minute)},
	{index: 8, company: 8, side: "sell", priceMinor: 31_150, quantityMin: 200_000, createdAt: fixtures.GeneratedAt.Add(-3 * time.Minute)},
}

type seedPricePoint struct {
	label          string
	priceMinor     int64
	volumeQuantity int64
	bestBidMinor   int64
	bestAskMinor   int64
}

var seedMarketHistory = map[int][]seedPricePoint{
	seedWaterMarket: {
		{label: "08:00", priceMinor: 29_740, volumeQuantity: 2_140_000, bestBidMinor: 29_690, bestAskMinor: 29_785},
		{label: "09:00", priceMinor: 30_010, volumeQuantity: 2_360_000, bestBidMinor: 29_960, bestAskMinor: 30_055},
		{label: "10:00", priceMinor: 29_920, volumeQuantity: 2_180_000, bestBidMinor: 29_870, bestAskMinor: 29_965},
		{label: "11:00", priceMinor: 30_380, volumeQuantity: 2_710_000, bestBidMinor: 30_330, bestAskMinor: 30_425},
		{label: "12:00", priceMinor: 30_270, volumeQuantity: 2_520_000, bestBidMinor: 30_220, bestAskMinor: 30_315},
		{label: "13:00", priceMinor: 30_630, volumeQuantity: 2_980_000, bestBidMinor: 30_580, bestAskMinor: 30_675},
		{label: "14:00", priceMinor: 30_825, volumeQuantity: 3_530_000, bestBidMinor: 30_780, bestAskMinor: 30_875},
	},
	seedLOXMarket: {
		{label: "08:00", priceMinor: 50_920, volumeQuantity: 980_000, bestBidMinor: 50_860, bestAskMinor: 50_980},
		{label: "09:00", priceMinor: 51_140, volumeQuantity: 1_120_000, bestBidMinor: 51_080, bestAskMinor: 51_200},
		{label: "10:00", priceMinor: 51_080, volumeQuantity: 1_040_000, bestBidMinor: 51_020, bestAskMinor: 51_140},
		{label: "11:00", priceMinor: 51_460, volumeQuantity: 1_310_000, bestBidMinor: 51_400, bestAskMinor: 51_520},
		{label: "12:00", priceMinor: 51_680, volumeQuantity: 1_460_000, bestBidMinor: 51_620, bestAskMinor: 51_740},
		{label: "13:00", priceMinor: 51_940, volumeQuantity: 1_580_000, bestBidMinor: 51_880, bestAskMinor: 52_000},
		{label: "14:00", priceMinor: 52_135, volumeQuantity: 1_740_000, bestBidMinor: 52_075, bestAskMinor: 52_195},
	},
}

// Seed installs the deterministic first-playable scenario in one transaction.
// Every insert is conflict-safe so a completed scenario is a no-op on retry.
func (s *Service) Seed(ctx context.Context) error {
	if s == nil || s.database == nil {
		return errors.New("seed game: database is required")
	}
	started := time.Now()
	transaction, err := s.database.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return fmt.Errorf("seed game: begin transaction: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()

	steps := []struct {
		name string
		seed func(context.Context, *sql.Tx) error
	}{
		{name: "players and companies", seed: seedPlayersAndCompanies},
		{name: "ledger", seed: seedLedger},
		{name: "inventory", seed: seedInventory},
		{name: "market history", seed: seedPriceHistory},
		{name: "market book", seed: seedOrdersAndTrade},
		{name: "production and freight", seed: seedProductionAndFreight},
		{name: "workspace chat alerts and operator", seed: seedWorkspaceChatAlertsOperator},
	}
	for _, step := range steps {
		if err := step.seed(ctx, transaction); err != nil {
			return fmt.Errorf("seed game: %s: %w", step.name, err)
		}
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("seed game: commit: %w", err)
	}
	if s.logger != nil {
		s.logger.Info("first-playable scenario seeded",
			"scenario_version", fixtures.ScenarioVersion,
			"companies", fixtures.ScenarioCompanyCount,
			"seed", fixtures.Seed,
			"duration_ms", time.Since(started).Milliseconds(),
		)
	}
	return nil
}

func seedPlayersAndCompanies(ctx context.Context, transaction *sql.Tx) error {
	for index := 1; index <= fixtures.ScenarioCompanyCount; index++ {
		playerID := fixtures.SeededPlayerID(index)
		companyID := fixtures.SeededCompanyID(index)
		displayName := fmt.Sprintf("Operator %02d", index)
		companyName := fmt.Sprintf("Cislunar Company %02d", index)
		slug := fmt.Sprintf("cislunar-company-%02d", index)
		if index == SeededActorIndex {
			displayName = SeededDisplayName
			companyName = "Helios Industries"
			slug = "helios-industries"
		}
		if _, err := transaction.ExecContext(ctx, `
			INSERT INTO identity.players (
				player_id, provider, provider_subject, display_name, status, created_at, updated_at
			) VALUES ($1, 'development', $2, $3, 'active', $4, $4)
			ON CONFLICT DO NOTHING`,
			playerID, fmt.Sprintf("seed-player-%02d", index), displayName, fixtures.GeneratedAt,
		); err != nil {
			return fmt.Errorf("insert player %d: %w", index, err)
		}
		if _, err := transaction.ExecContext(ctx, `
			INSERT INTO companies.companies (
				company_id, name, slug, base_currency, status, version, created_at, updated_at
			) VALUES ($1, $2, $3, $4, 'active', 1, $5, $5)
			ON CONFLICT DO NOTHING`,
			companyID, companyName, slug, seedCurrency, fixtures.GeneratedAt,
		); err != nil {
			return fmt.Errorf("insert company %d: %w", index, err)
		}
		if _, err := transaction.ExecContext(ctx, `
			INSERT INTO companies.memberships (
				company_id, player_id, role, approval_limit_minor, joined_at, created_at, updated_at
			) VALUES ($1, $2, 'owner', NULL, $3, $3, $3)
			ON CONFLICT DO NOTHING`, companyID, playerID, fixtures.GeneratedAt.Add(-24*time.Hour)); err != nil {
			return fmt.Errorf("insert membership %d: %w", index, err)
		}
	}
	return nil
}

func seedLedger(ctx context.Context, transaction *sql.Tx) error {
	for companyIndex := 1; companyIndex <= fixtures.ScenarioCompanyCount; companyIndex++ {
		companyID := fixtures.SeededCompanyID(companyIndex)
		accounts := []struct {
			offset   int
			code     string
			name     string
			category string
		}{
			{offset: 1, code: "CASH", name: "Cash", category: "asset"},
			{offset: 2, code: "INVENTORY", name: "Inventory", category: "asset"},
			{offset: 3, code: "LIABILITIES", name: "Liabilities", category: "liability"},
			{offset: 4, code: "EQUITY", name: "Owner equity", category: "equity"},
			{offset: 5, code: "SALES", name: "Sales revenue", category: "revenue"},
			{offset: 6, code: "COGS", name: "Cost of goods sold", category: "expense"},
			{offset: 7, code: "PRODUCTION_WIP", name: "Production work in progress", category: "asset"},
		}
		for _, account := range accounts {
			if _, err := transaction.ExecContext(ctx, `
				INSERT INTO ledger.accounts (
					account_id, company_id, currency, code, name, category, status, created_at
				) VALUES ($1, $2, $3, $4, $5, $6, 'active', $7)
				ON CONFLICT DO NOTHING`,
				accountID(companyIndex, account.offset), companyID, seedCurrency,
				account.code, account.name, account.category, fixtures.GeneratedAt,
			); err != nil {
				return fmt.Errorf("insert account company %d code %s: %w", companyIndex, account.code, err)
			}
		}

		cash, inventoryValue, liabilities := openingFinancials(companyIndex)
		equity := cash + inventoryValue - liabilities
		journalID := scenarioID(fixtures.NamespaceJournal, companyIndex)
		if _, err := transaction.ExecContext(ctx, `
			INSERT INTO ledger.journals (
				journal_id, company_id, currency, source_type, source_id, description,
				occurred_at, posted_at, created_at
			) VALUES ($1, $2, $3, 'opening', $1, 'Deterministic opening position', $4, $4, $4)
			ON CONFLICT DO NOTHING`, journalID, companyID, seedCurrency, fixtures.GeneratedAt.Add(-24*time.Hour)); err != nil {
			return fmt.Errorf("insert opening journal company %d: %w", companyIndex, err)
		}
		entries := []struct {
			offset  int
			account int
			side    string
			amount  int64
			memo    string
		}{
			{offset: 1, account: 1, side: "debit", amount: cash, memo: "Opening cash"},
			{offset: 2, account: 2, side: "debit", amount: inventoryValue, memo: "Opening inventory value"},
			{offset: 3, account: 3, side: "credit", amount: liabilities, memo: "Opening liabilities"},
			{offset: 4, account: 4, side: "credit", amount: equity, memo: "Opening owner equity"},
		}
		for _, entry := range entries {
			if _, err := transaction.ExecContext(ctx, `
				INSERT INTO ledger.entries (
					entry_id, journal_id, account_id, company_id, currency, side, amount_minor, memo, created_at
				) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
				ON CONFLICT DO NOTHING`,
				scenarioID(fixtures.NamespaceEntry, companyIndex*10+entry.offset), journalID,
				accountID(companyIndex, entry.account), companyID, seedCurrency,
				entry.side, entry.amount, entry.memo, fixtures.GeneratedAt.Add(-24*time.Hour),
			); err != nil {
				return fmt.Errorf("insert opening entry company %d offset %d: %w", companyIndex, entry.offset, err)
			}
		}
	}
	return nil
}

func seedInventory(ctx context.Context, transaction *sql.Tx) error {
	locations := []struct {
		index        int
		code         string
		name         string
		locationType string
	}{
		{index: seedMoonLocation, code: "lunar-south-pole", name: "Lunar south pole", locationType: "surface"},
		{index: seedOrbitLocation, code: "lunar-orbit", name: "Lunar orbit", locationType: "orbit"},
	}
	for _, location := range locations {
		if _, err := transaction.ExecContext(ctx, `
			INSERT INTO inventory.locations (
				location_id, code, name, location_type, status, created_at
			) VALUES ($1, $2, $3, $4, 'active', $5)
			ON CONFLICT DO NOTHING`,
			locationID(location.index), location.code, location.name, location.locationType, fixtures.GeneratedAt,
		); err != nil {
			return fmt.Errorf("insert location %s: %w", location.code, err)
		}
	}
	commodities := []struct {
		index int
		code  string
		name  string
		unit  string
	}{
		{index: seedWaterCommodity, code: "water-ice", name: "Water ice", unit: "t"},
		{index: seedLOXCommodity, code: "liquid-oxygen", name: "Liquid oxygen", unit: "t"},
	}
	for _, commodity := range commodities {
		if _, err := transaction.ExecContext(ctx, `
			INSERT INTO inventory.commodities (
				commodity_id, code, name, unit, quantity_scale, status, created_at
			) VALUES ($1, $2, $3, $4, $5, 'active', $6)
			ON CONFLICT DO NOTHING`,
			commodityID(commodity.index), commodity.code, commodity.name, commodity.unit,
			seedQuantityScale, fixtures.GeneratedAt,
		); err != nil {
			return fmt.Errorf("insert commodity %s: %w", commodity.code, err)
		}
	}

	reservedWater := make(map[int]int64)
	for _, order := range waterBook {
		if order.side == "sell" {
			reservedWater[order.company] += order.quantityMin
		}
	}
	const tradeQuantity int64 = 18_000
	for companyIndex := 1; companyIndex <= fixtures.ScenarioCompanyCount; companyIndex++ {
		waterQuantity := int64(1_000_000 + companyIndex*25_000)
		loxQuantity := int64(700_000 + companyIndex*20_000)
		if companyIndex == 1 {
			waterQuantity = 8_420_000
			loxQuantity = 3_180_000
		}
		if companyIndex == 2 {
			loxQuantity = 4_000_000
		}
		holdings := []struct {
			commodityIndex int
			locationIndex  int
			quantity       int64
			reserved       int64
			opening        int64
		}{
			{commodityIndex: seedWaterCommodity, locationIndex: seedMoonLocation, quantity: waterQuantity, reserved: reservedWater[companyIndex], opening: waterQuantity},
			{commodityIndex: seedLOXCommodity, locationIndex: seedOrbitLocation, quantity: loxQuantity, opening: loxQuantity},
		}
		if companyIndex == 1 {
			holdings[1].opening -= tradeQuantity
		}
		if companyIndex == 2 {
			holdings[1].opening += tradeQuantity
		}
		for _, holding := range holdings {
			holdingID := holdingID(companyIndex, holding.commodityIndex)
			costBasis := holdingCostBasis(companyIndex, holding.commodityIndex)
			if _, err := transaction.ExecContext(ctx, `
				INSERT INTO inventory.holdings (
					holding_id, company_id, location_id, commodity_id, quantity_minor,
					reserved_quantity_minor, cost_basis_minor, version, created_at, updated_at
				) VALUES ($1, $2, $3, $4, $5, $6, $7, 1, $8, $8)
				ON CONFLICT DO NOTHING`,
				holdingID, fixtures.SeededCompanyID(companyIndex), locationID(holding.locationIndex),
				commodityID(holding.commodityIndex), holding.quantity, holding.reserved, costBasis,
				fixtures.GeneratedAt.Add(-24*time.Hour),
			); err != nil {
				return fmt.Errorf("insert holding company %d commodity %d: %w", companyIndex, holding.commodityIndex, err)
			}
			if _, err := transaction.ExecContext(ctx, `
				INSERT INTO inventory.movements (
					movement_id, movement_kind, source_id, to_holding_id, quantity_minor,
					occurred_at, reason, created_at
				) VALUES ($1, 'opening', $2, $2, $3, $4, 'Deterministic opening inventory', $4)
				ON CONFLICT DO NOTHING`,
				movementID(companyIndex, holding.commodityIndex), holdingID, holding.opening, fixtures.GeneratedAt.Add(-24*time.Hour),
			); err != nil {
				return fmt.Errorf("insert opening movement company %d commodity %d: %w", companyIndex, holding.commodityIndex, err)
			}
		}
	}

	markets := []struct {
		index          int
		locationIndex  int
		commodityIndex int
	}{
		{index: seedWaterMarket, locationIndex: seedMoonLocation, commodityIndex: seedWaterCommodity},
		{index: seedLOXMarket, locationIndex: seedOrbitLocation, commodityIndex: seedLOXCommodity},
	}
	for _, market := range markets {
		if _, err := transaction.ExecContext(ctx, `
			INSERT INTO markets.markets (
				market_id, location_id, commodity_id, currency, price_scale,
				lot_size_minor, fee_basis_points, status, created_at
			) VALUES ($1, $2, $3, $4, 2, $5, 0, 'open', $6)
			ON CONFLICT DO NOTHING`,
			marketID(market.index), locationID(market.locationIndex), commodityID(market.commodityIndex),
			seedCurrency, seedLotSizeMinor, fixtures.GeneratedAt,
		); err != nil {
			return fmt.Errorf("insert market %d: %w", market.index, err)
		}
	}
	return nil
}

func seedPriceHistory(ctx context.Context, transaction *sql.Tx) error {
	for marketIndex, points := range seedMarketHistory {
		for pointIndex, point := range points {
			eventIndex := pointIndex + 1
			if marketIndex == seedLOXMarket {
				eventIndex += 10
			}
			payload := mustJSON(map[string]any{
				"marketId":            marketID(marketIndex),
				"label":               point.label,
				"priceMinor":          point.priceMinor,
				"volumeQuantityMinor": point.volumeQuantity,
				"bestBidMinor":        point.bestBidMinor,
				"bestAskMinor":        point.bestAskMinor,
			})
			occurredAt := fixtures.GeneratedAt.Add(time.Duration(pointIndex-6) * time.Hour)
			if err := insertSeedEvent(ctx, transaction, scenarioID(fixtures.NamespaceEvent, eventIndex), nil,
				fmt.Sprintf("public.market.%s", marketID(marketIndex)), int64(pointIndex+1),
				"MARKET_PRICE_OBSERVED", occurredAt, payload); err != nil {
				return fmt.Errorf("insert market %d history point %d: %w", marketIndex, pointIndex, err)
			}
		}
	}
	return nil
}

func seedOrdersAndTrade(ctx context.Context, transaction *sql.Tx) error {
	for _, order := range waterBook {
		commandID := scenarioID(fixtures.NamespaceCommand, order.index)
		orderID := scenarioID(fixtures.NamespaceOrder, order.index)
		payload := mustJSON(map[string]any{
			"marketId": marketID(seedWaterMarket), "side": order.side,
			"limitPriceMinor": order.priceMinor, "quantityMinor": order.quantityMin,
		})
		resultPayload := mustJSON(map[string]any{"orderId": orderID})
		if err := insertSeedCommand(ctx, transaction, commandID, order.company,
			fmt.Sprintf("seed-water-order-%02d", order.index), "market.place_order",
			fixtures.SeededCompanyID(order.company), order.createdAt, payload, resultPayload); err != nil {
			return fmt.Errorf("insert water order command %d: %w", order.index, err)
		}
		if _, err := transaction.ExecContext(ctx, `
			INSERT INTO markets.orders (
				order_id, command_id, market_id, company_id, side, limit_price_minor,
				original_quantity_minor, remaining_quantity_minor, status, version, created_at, updated_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $7, 'open', 1, $8, $8)
			ON CONFLICT DO NOTHING`,
			orderID, commandID, marketID(seedWaterMarket), fixtures.SeededCompanyID(order.company),
			order.side, order.priceMinor, order.quantityMin, order.createdAt,
		); err != nil {
			return fmt.Errorf("insert water order %d: %w", order.index, err)
		}
		if order.side == "buy" {
			reserved := cashReservationMinor(order.priceMinor, order.quantityMin, seedQuantityScale)
			if _, err := transaction.ExecContext(ctx, `
				INSERT INTO markets.order_reservations (
					reservation_id, order_id, company_id, resource_type, ledger_account_id, currency,
					original_reserved_minor, remaining_reserved_minor, status, version, created_at, updated_at
				) VALUES ($1, $2, $3, 'cash', $4, $5, $6, $6, 'active', 1, $7, $7)
				ON CONFLICT DO NOTHING`,
				scenarioID(fixtures.NamespaceReservation, order.index), orderID,
				fixtures.SeededCompanyID(order.company), accountID(order.company, 1), seedCurrency,
				reserved, order.createdAt,
			); err != nil {
				return fmt.Errorf("insert water cash reservation %d: %w", order.index, err)
			}
		} else {
			if _, err := transaction.ExecContext(ctx, `
				INSERT INTO markets.order_reservations (
					reservation_id, order_id, company_id, resource_type, holding_id,
					original_reserved_minor, remaining_reserved_minor, status, version, created_at, updated_at
				) VALUES ($1, $2, $3, 'inventory', $4, $5, $5, 'active', 1, $6, $6)
				ON CONFLICT DO NOTHING`,
				scenarioID(fixtures.NamespaceReservation, order.index), orderID,
				fixtures.SeededCompanyID(order.company), holdingID(order.company, seedWaterCommodity),
				order.quantityMin, order.createdAt,
			); err != nil {
				return fmt.Errorf("insert water inventory reservation %d: %w", order.index, err)
			}
		}
	}

	const (
		buyOrderIndex  = 21
		sellOrderIndex = 22
		tradeQuantity  = int64(18_000)
		tradePrice     = int64(52_040)
	)
	tradeAt := fixtures.GeneratedAt.Add(-11 * time.Minute)
	tradeID := scenarioID(fixtures.NamespaceTrade, 1)
	tradeEventID := scenarioID(fixtures.NamespaceEvent, 30)
	tradeCommandID := scenarioID(fixtures.NamespaceCommand, buyOrderIndex)
	tradeCommands := []struct {
		index   int
		company int
		side    string
	}{
		{index: buyOrderIndex, company: 1, side: "buy"},
		{index: sellOrderIndex, company: 2, side: "sell"},
	}
	for _, order := range tradeCommands {
		commandID := scenarioID(fixtures.NamespaceCommand, order.index)
		orderID := scenarioID(fixtures.NamespaceOrder, order.index)
		payload := mustJSON(map[string]any{
			"marketId": marketID(seedLOXMarket), "side": order.side,
			"limitPriceMinor": tradePrice, "quantityMinor": tradeQuantity,
		})
		if err := insertSeedCommand(ctx, transaction, commandID, order.company,
			fmt.Sprintf("seed-lox-trade-%s", order.side), "market.place_order",
			fixtures.SeededCompanyID(order.company), tradeAt, payload,
			mustJSON(map[string]any{"orderId": orderID, "tradeId": tradeID})); err != nil {
			return fmt.Errorf("insert LOX %s command: %w", order.side, err)
		}
		if _, err := transaction.ExecContext(ctx, `
			INSERT INTO markets.orders (
				order_id, command_id, market_id, company_id, side, limit_price_minor,
				original_quantity_minor, remaining_quantity_minor, status, version, created_at, updated_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, 0, 'filled', 1, $8, $8)
			ON CONFLICT DO NOTHING`,
			orderID, commandID, marketID(seedLOXMarket), fixtures.SeededCompanyID(order.company),
			order.side, tradePrice, tradeQuantity, tradeAt,
		); err != nil {
			return fmt.Errorf("insert LOX %s order: %w", order.side, err)
		}
		if order.side == "buy" {
			if _, err := transaction.ExecContext(ctx, `
				INSERT INTO markets.order_reservations (
					reservation_id, order_id, company_id, resource_type, ledger_account_id, currency,
					original_reserved_minor, remaining_reserved_minor, status, version, created_at, updated_at
				) VALUES ($1, $2, $3, 'cash', $4, $5, $6, 0, 'consumed', 1, $7, $7)
				ON CONFLICT DO NOTHING`,
				scenarioID(fixtures.NamespaceReservation, order.index), orderID,
				fixtures.SeededCompanyID(order.company), accountID(order.company, 1), seedCurrency,
				cashReservationMinor(tradePrice, tradeQuantity, seedQuantityScale), tradeAt,
			); err != nil {
				return fmt.Errorf("insert LOX buy reservation: %w", err)
			}
		} else {
			if _, err := transaction.ExecContext(ctx, `
				INSERT INTO markets.order_reservations (
					reservation_id, order_id, company_id, resource_type, holding_id,
					original_reserved_minor, remaining_reserved_minor, status, version, created_at, updated_at
				) VALUES ($1, $2, $3, 'inventory', $4, $5, 0, 'consumed', 1, $6, $6)
				ON CONFLICT DO NOTHING`,
				scenarioID(fixtures.NamespaceReservation, order.index), orderID,
				fixtures.SeededCompanyID(order.company), holdingID(order.company, seedLOXCommodity),
				tradeQuantity, tradeAt,
			); err != nil {
				return fmt.Errorf("insert LOX sell reservation: %w", err)
			}
		}
	}
	if err := insertSeedEvent(ctx, transaction, tradeEventID,
		&tradeCommandID, "public.market."+marketID(seedLOXMarket), 8,
		"TRADE_EXECUTED", tradeAt, mustJSON(map[string]any{
			"tradeId": tradeID, "marketId": marketID(seedLOXMarket),
			"priceMinor": tradePrice, "quantityMinor": tradeQuantity,
		})); err != nil {
		return fmt.Errorf("insert trade event: %w", err)
	}
	tradeValueMinor := tradePrice * tradeQuantity / 1_000
	if err := seedTradeJournal(ctx, transaction, 1, 1001, tradeCommandID, tradeID, tradeAt, tradeValueMinor, true); err != nil {
		return err
	}
	if err := seedTradeJournal(ctx, transaction, 2, 1002, tradeCommandID, tradeID, tradeAt, tradeValueMinor, false); err != nil {
		return err
	}
	tradeMovementID := scenarioID(fixtures.NamespaceMovement, 1001)
	if _, err := transaction.ExecContext(ctx, `
		INSERT INTO inventory.movements (
			movement_id, command_id, movement_kind, source_id, from_holding_id, to_holding_id,
			quantity_minor, occurred_at, reason, created_at
		) VALUES ($1, $2, 'settlement', $3, $4, $5, $6, $7, 'Seeded LOX trade settlement', $7)
		ON CONFLICT DO NOTHING`,
		tradeMovementID, tradeCommandID, tradeID, holdingID(2, seedLOXCommodity),
		holdingID(1, seedLOXCommodity), tradeQuantity, tradeAt,
	); err != nil {
		return fmt.Errorf("insert trade inventory movement: %w", err)
	}
	if _, err := transaction.ExecContext(ctx, `
		INSERT INTO markets.trades (
			trade_id, market_id, buy_order_id, sell_order_id, buyer_company_id, seller_company_id,
			price_minor, quantity_minor, buyer_fee_minor, seller_fee_minor,
			inventory_movement_id, buyer_journal_id, seller_journal_id, event_id, executed_at, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 0, 0, $9, $10, $11, $12, $13, $13)
		ON CONFLICT DO NOTHING`,
		tradeID, marketID(seedLOXMarket), scenarioID(fixtures.NamespaceOrder, buyOrderIndex),
		scenarioID(fixtures.NamespaceOrder, sellOrderIndex), fixtures.SeededCompanyID(1), fixtures.SeededCompanyID(2),
		tradePrice, tradeQuantity, tradeMovementID, scenarioID(fixtures.NamespaceJournal, 1001),
		scenarioID(fixtures.NamespaceJournal, 1002), tradeEventID, tradeAt,
	); err != nil {
		return fmt.Errorf("insert trade: %w", err)
	}
	return nil
}

func seedTradeJournal(
	ctx context.Context,
	transaction *sql.Tx,
	companyIndex int,
	journalIndex int,
	commandID string,
	tradeID string,
	occurredAt time.Time,
	amountMinor int64,
	buyer bool,
) error {
	journalID := scenarioID(fixtures.NamespaceJournal, journalIndex)
	if _, err := transaction.ExecContext(ctx, `
		INSERT INTO ledger.journals (
			journal_id, company_id, currency, command_id, source_type, source_id, description,
			occurred_at, posted_at, created_at
		) VALUES ($1, $2, $3, $4, 'trade', $5, 'Seeded LOX trade settlement', $6, $6, $6)
		ON CONFLICT DO NOTHING`,
		journalID, fixtures.SeededCompanyID(companyIndex), seedCurrency, commandID, tradeID, occurredAt,
	); err != nil {
		return fmt.Errorf("insert trade journal company %d: %w", companyIndex, err)
	}
	debitAccount, creditAccount := accountID(companyIndex, 1), accountID(companyIndex, 2)
	if buyer {
		debitAccount, creditAccount = accountID(companyIndex, 2), accountID(companyIndex, 1)
	}
	entries := []struct {
		index   int
		account string
		side    string
	}{
		{index: journalIndex*10 + 1, account: debitAccount, side: "debit"},
		{index: journalIndex*10 + 2, account: creditAccount, side: "credit"},
	}
	for _, entry := range entries {
		if _, err := transaction.ExecContext(ctx, `
			INSERT INTO ledger.entries (
				entry_id, journal_id, account_id, company_id, currency, side, amount_minor, memo, created_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, 'LOX settlement', $8)
			ON CONFLICT DO NOTHING`,
			scenarioID(fixtures.NamespaceEntry, entry.index), journalID, entry.account,
			fixtures.SeededCompanyID(companyIndex), seedCurrency, entry.side, amountMinor, occurredAt,
		); err != nil {
			return fmt.Errorf("insert trade entry company %d: %w", companyIndex, err)
		}
	}
	return nil
}

func seedProductionAndFreight(ctx context.Context, transaction *sql.Tx) error {
	facilityTypeID := scenarioID(fixtures.NamespaceFacility, 1000)
	recipeID := scenarioID(fixtures.NamespaceRecipe, 1)
	facilityID := scenarioID(fixtures.NamespaceFacility, 1)
	jobID := scenarioID(fixtures.NamespaceJob, 1)
	dueAt := fixtures.GeneratedAt.Add(-time.Minute)
	if _, err := transaction.ExecContext(ctx, `
		INSERT INTO production.facility_types (facility_type_id, code, name, created_at)
		VALUES ($1, 'water-electrolysis', 'Water electrolysis plant', $2)
		ON CONFLICT DO NOTHING`, facilityTypeID, fixtures.GeneratedAt); err != nil {
		return fmt.Errorf("insert facility type: %w", err)
	}
	if _, err := transaction.ExecContext(ctx, `
		INSERT INTO production.recipes (
			recipe_id, facility_type_id, rule_version, cycle_seconds,
			output_commodity_id, output_quantity_minor, created_at
		) VALUES ($1, $2, 'mvp-1', 3600, $3, 780000, $4)
		ON CONFLICT DO NOTHING`, recipeID, facilityTypeID, commodityID(seedLOXCommodity), fixtures.GeneratedAt); err != nil {
		return fmt.Errorf("insert production recipe: %w", err)
	}
	if _, err := transaction.ExecContext(ctx, `
		INSERT INTO production.recipe_inputs (recipe_id, commodity_id, quantity_minor)
		VALUES ($1, $2, 900000)
		ON CONFLICT DO NOTHING`, recipeID, commodityID(seedWaterCommodity)); err != nil {
		return fmt.Errorf("insert recipe input: %w", err)
	}
	if _, err := transaction.ExecContext(ctx, `
		INSERT INTO production.facilities (
			facility_id, company_id, location_id, facility_type_id, active_recipe_id,
			name, nominal_capacity_minor, utilization_basis_points, condition_basis_points,
			status, next_execution_at, version, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, 'Water Ice Refinery 02', 1000000, 9200, 9700,
			'operational', $6, 1, $7, $7)
		ON CONFLICT DO NOTHING`,
		facilityID, SeededCompanyID, locationID(seedMoonLocation), facilityTypeID, recipeID, dueAt, fixtures.GeneratedAt.Add(-30*24*time.Hour),
	); err != nil {
		return fmt.Errorf("insert facility: %w", err)
	}
	if _, err := transaction.ExecContext(ctx, `
		INSERT INTO production.jobs (
			job_id, facility_id, recipe_id, rule_version, due_at, status,
			attempt_count, random_seed, created_at, updated_at
		) VALUES ($1, $2, $3, 'mvp-1', $4, 'queued', 0, $5, $6, $6)
		ON CONFLICT DO NOTHING`, jobID, facilityID, recipeID, dueAt, fixtures.Seed, fixtures.GeneratedAt); err != nil {
		return fmt.Errorf("insert due production job: %w", err)
	}

	routeID := scenarioID(fixtures.NamespaceFreight, 1)
	contractID := scenarioID(fixtures.NamespaceFreight, 2)
	deliveryID := scenarioID(fixtures.NamespaceFreight, 3)
	commandID := scenarioID(fixtures.NamespaceCommand, 30)
	if err := insertSeedCommand(ctx, transaction, commandID, 1, "seed-freight-contract",
		"freight.create_contract", SeededCompanyID, fixtures.GeneratedAt.Add(-40*time.Minute),
		mustJSON(map[string]any{"routeId": routeID, "quantityMinor": 160_000}),
		mustJSON(map[string]any{"contractId": contractID, "deliveryId": deliveryID})); err != nil {
		return fmt.Errorf("insert freight command: %w", err)
	}
	if _, err := transaction.ExecContext(ctx, `
		INSERT INTO freight.routes (
			route_id, code, origin_location_id, destination_location_id,
			transit_seconds, capacity_quantity_minor, status, created_at
		) VALUES ($1, 'lunar-surface-to-orbit', $2, $3, 5400, 1000000, 'open', $4)
		ON CONFLICT DO NOTHING`, routeID, locationID(seedMoonLocation), locationID(seedOrbitLocation), fixtures.GeneratedAt); err != nil {
		return fmt.Errorf("insert freight route: %w", err)
	}
	if _, err := transaction.ExecContext(ctx, `
		INSERT INTO freight.contracts (
			contract_id, command_id, route_id, commodity_id, shipper_company_id,
			carrier_company_id, quantity_minor, delivered_quantity_minor, unit_price_minor,
			currency, status, pickup_after, deliver_by, terms, version, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, 160000, 0, 18400, $7, 'active',
			$8, $9, '{"serviceLevel":"standard"}'::jsonb, 1, $8, $8)
		ON CONFLICT DO NOTHING`,
		contractID, commandID, routeID, commodityID(seedWaterCommodity), fixtures.SeededCompanyID(2),
		SeededCompanyID, seedCurrency, fixtures.GeneratedAt.Add(-30*time.Minute), fixtures.GeneratedAt.Add(92*time.Minute),
	); err != nil {
		return fmt.Errorf("insert freight contract: %w", err)
	}
	if _, err := transaction.ExecContext(ctx, `
		INSERT INTO freight.deliveries (
			delivery_id, contract_id, delivery_sequence, quantity_minor, status,
			scheduled_departure_at, created_at, updated_at
		) VALUES ($1, $2, 1, 160000, 'scheduled', $3, $4, $4)
		ON CONFLICT DO NOTHING`,
		deliveryID, contractID, fixtures.GeneratedAt.Add(-5*time.Minute), fixtures.GeneratedAt.Add(-30*time.Minute),
	); err != nil {
		return fmt.Errorf("insert ready delivery: %w", err)
	}
	if err := insertSeedEvent(ctx, transaction, scenarioID(fixtures.NamespaceEvent, 32), &commandID,
		"company:"+SeededCompanyID, 1, "FREIGHT_READY", fixtures.GeneratedAt.Add(-5*time.Minute),
		mustJSON(map[string]any{"contractId": contractID, "deliveryId": deliveryID})); err != nil {
		return fmt.Errorf("insert freight ready event: %w", err)
	}
	return nil
}

func seedWorkspaceChatAlertsOperator(ctx context.Context, transaction *sql.Tx) error {
	device1 := scenarioID(fixtures.NamespaceDevice, 1)
	device2 := scenarioID(fixtures.NamespaceDevice, 2)
	view1 := scenarioID(fixtures.NamespaceView, 1)
	view2 := scenarioID(fixtures.NamespaceView, 2)
	views := []struct {
		id          string
		name        string
		deviceClass string
		deviceID    string
		panelID     string
		panelName   string
		message     string
	}{
		{id: view1, name: "Operations", deviceClass: "desktop", deviceID: device1,
			panelID: scenarioID(fixtures.NamespaceView, 101), panelName: "Market ribbon", message: "Water spread is CR 0.95/t"},
		{id: view2, name: "Dispatch", deviceClass: "tablet", deviceID: device2,
			panelID: scenarioID(fixtures.NamespaceView, 102), panelName: "Freight queue", message: "Delivery is ready"},
	}
	for _, view := range views {
		panels := mustJSON([]map[string]any{{
			"id": view.panelID, "name": view.panelName, "deviceId": view.deviceID,
			"status": "ready", "lastMessage": view.message,
		}})
		subscriptions := mustJSON([]string{"public", "company:" + SeededCompanyID, "player:" + SeededActorID})
		if _, err := transaction.ExecContext(ctx, `
			INSERT INTO workspace.views (
				view_id, player_id, name, device_class, panel_definitions,
				subscriptions, version, created_at, updated_at
			) VALUES ($1, $2, $3, $4, $5, $6, 1, $7, $7)
			ON CONFLICT DO NOTHING`,
			view.id, SeededActorID, view.name, view.deviceClass, panels, subscriptions, fixtures.GeneratedAt.Add(-24*time.Hour),
		); err != nil {
			return fmt.Errorf("insert workspace view %s: %w", view.name, err)
		}
	}
	devices := []struct {
		id          string
		key         string
		name        string
		deviceClass string
		viewID      string
		lastSeen    time.Time
	}{
		{id: device1, key: "seed-operations-wall", name: "Operations wall", deviceClass: "desktop", viewID: view1, lastSeen: fixtures.GeneratedAt.Add(-6 * time.Second)},
		{id: device2, key: "seed-dispatch-tablet", name: "Dispatch tablet", deviceClass: "tablet", viewID: view2, lastSeen: fixtures.GeneratedAt.Add(-2 * time.Minute)},
	}
	for _, device := range devices {
		if _, err := transaction.ExecContext(ctx, `
			INSERT INTO workspace.devices (
				device_id, player_id, device_key, name, device_class, status,
				registered_at, last_seen_at, active_view_id, created_at, updated_at
			) VALUES ($1, $2, $3, $4, $5, 'registered', $6, $7, $8, $6, $7)
			ON CONFLICT DO NOTHING`,
			device.id, SeededActorID, device.key, device.name, device.deviceClass,
			fixtures.GeneratedAt.Add(-24*time.Hour), device.lastSeen, device.viewID,
		); err != nil {
			return fmt.Errorf("insert workspace device %s: %w", device.name, err)
		}
	}

	channelID := scenarioID(fixtures.NamespaceChat, 1)
	messageID := scenarioID(fixtures.NamespaceChat, 2)
	chatCommandID := scenarioID(fixtures.NamespaceCommand, 31)
	chatEventID := scenarioID(fixtures.NamespaceEvent, 31)
	chatAt := fixtures.GeneratedAt.Add(-9 * time.Minute)
	if err := insertSeedCommand(ctx, transaction, chatCommandID, 1, "seed-company-chat",
		"chat.send", SeededCompanyID, chatAt,
		mustJSON(map[string]any{"channelId": channelID, "body": "Freight is staged for release."}),
		mustJSON(map[string]any{"messageId": messageID})); err != nil {
		return fmt.Errorf("insert chat command: %w", err)
	}
	if err := insertSeedEvent(ctx, transaction, chatEventID, &chatCommandID, "company:"+SeededCompanyID, 2,
		"CHAT_MESSAGE_SENT", chatAt, mustJSON(map[string]any{"channelId": channelID, "messageId": messageID})); err != nil {
		return fmt.Errorf("insert chat event: %w", err)
	}
	if _, err := transaction.ExecContext(ctx, `
		INSERT INTO chat.channels (
			channel_id, channel_type, name, company_id, status, created_at
		) VALUES ($1, 'company', 'Helios operations', $2, 'active', $3)
		ON CONFLICT DO NOTHING`, channelID, SeededCompanyID, fixtures.GeneratedAt.Add(-24*time.Hour)); err != nil {
		return fmt.Errorf("insert company chat channel: %w", err)
	}
	if _, err := transaction.ExecContext(ctx, `
		INSERT INTO chat.channel_members (channel_id, player_id, member_role, joined_at)
		VALUES ($1, $2, 'moderator', $3)
		ON CONFLICT DO NOTHING`, channelID, SeededActorID, fixtures.GeneratedAt.Add(-24*time.Hour)); err != nil {
		return fmt.Errorf("insert company chat member: %w", err)
	}
	if _, err := transaction.ExecContext(ctx, `
		INSERT INTO chat.messages (
			message_id, command_id, channel_id, sender_player_id, body,
			event_id, sent_at, created_at
		) VALUES ($1, $2, $3, $4, 'Freight is staged for release.', $5, $6, $6)
		ON CONFLICT DO NOTHING`, messageID, chatCommandID, channelID, SeededActorID, chatEventID, chatAt); err != nil {
		return fmt.Errorf("insert company chat message: %w", err)
	}

	alerts := []struct {
		index       int
		alertType   string
		targetType  string
		targetID    string
		comparator  string
		threshold   any
		sourceEvent string
		title       string
		body        string
		severity    string
		createdAt   time.Time
	}{
		{index: 1, alertType: "price", targetType: "market", targetID: marketID(seedWaterMarket), comparator: "above", threshold: int64(30_800),
			sourceEvent: scenarioID(fixtures.NamespaceEvent, 7), title: "Water price threshold", body: "Water ice moved above CR 308/t.", severity: "warning", createdAt: fixtures.GeneratedAt},
		{index: 2, alertType: "delivery", targetType: "delivery", targetID: scenarioID(fixtures.NamespaceFreight, 3), comparator: "due", threshold: nil,
			sourceEvent: scenarioID(fixtures.NamespaceEvent, 32), title: "Delivery ready", body: "Lunar surface freight is ready to depart.", severity: "info", createdAt: fixtures.GeneratedAt.Add(-5 * time.Minute)},
	}
	for _, alert := range alerts {
		ruleID := scenarioID(fixtures.NamespaceAlert, alert.index*10+1)
		notificationID := scenarioID(fixtures.NamespaceAlert, alert.index*10+2)
		if _, err := transaction.ExecContext(ctx, `
			INSERT INTO alerts.rules (
				alert_rule_id, player_id, company_id, alert_type, target_type, target_id,
				comparator, threshold_minor, status, cooldown_seconds, created_at, updated_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'active', 300, $9, $9)
			ON CONFLICT DO NOTHING`,
			ruleID, SeededActorID, SeededCompanyID, alert.alertType, alert.targetType,
			alert.targetID, alert.comparator, alert.threshold, fixtures.GeneratedAt.Add(-24*time.Hour),
		); err != nil {
			return fmt.Errorf("insert alert rule %d: %w", alert.index, err)
		}
		if _, err := transaction.ExecContext(ctx, `
			INSERT INTO alerts.notifications (
				notification_id, alert_rule_id, player_id, source_event_id, title, body,
				payload, created_at, delivered_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $8)
			ON CONFLICT DO NOTHING`,
			notificationID, ruleID, SeededActorID, alert.sourceEvent, alert.title, alert.body,
			mustJSON(map[string]any{"severity": alert.severity}), alert.createdAt,
		); err != nil {
			return fmt.Errorf("insert alert notification %d: %w", alert.index, err)
		}
	}
	if _, err := transaction.ExecContext(ctx, `
		INSERT INTO operator_admin.grants (
			player_id, role, granted_by_player_id, granted_at, reason
		) VALUES ($1, 'operator', $1, $2, 'Seeded first-playable operator access')
		ON CONFLICT DO NOTHING`, SeededActorID, fixtures.GeneratedAt.Add(-24*time.Hour)); err != nil {
		return fmt.Errorf("insert operator grant: %w", err)
	}
	return nil
}

func insertSeedCommand(
	ctx context.Context,
	transaction *sql.Tx,
	commandID string,
	actorIndex int,
	idempotencyKey string,
	commandType string,
	companyID string,
	committedAt time.Time,
	payload json.RawMessage,
	resultPayload json.RawMessage,
) error {
	result := protocol.CommandResultEnvelope{
		CommandId:       commandID,
		CommittedAt:     &committedAt,
		Payload:         resultPayload,
		ProtocolVersion: protocol.Version,
		Status:          protocol.CommandResultStatusCommitted,
	}
	encodedResult, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("encode command result: %w", err)
	}
	hash := sha256.Sum256([]byte("ascent-seed-command:" + commandID))
	_, err = transaction.ExecContext(ctx, `
		INSERT INTO platform.command_log (
			command_id, protocol_version, idempotency_key, command_type, actor_id,
			company_id, status, payload, result, request_hash, received_at,
			committed_at, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, 'committed', $7, $8, $9, $10, $10, $10, $10)
		ON CONFLICT DO NOTHING`,
		commandID, protocol.Version, idempotencyKey, commandType, fixtures.SeededPlayerID(actorIndex),
		companyID, payload, encodedResult, hash[:], committedAt,
	)
	return err
}

func insertSeedEvent(
	ctx context.Context,
	transaction *sql.Tx,
	eventID string,
	commandID *string,
	topic string,
	topicSequence int64,
	eventType string,
	occurredAt time.Time,
	payload json.RawMessage,
) error {
	_, err := transaction.ExecContext(ctx, `
		INSERT INTO platform.event_outbox (
			event_id, protocol_version, command_id, topic, topic_sequence,
			event_type, occurred_at, payload, published_at, publish_attempts, created_at
		)
		SELECT $1, $2, $3, $4, $5, $6, $7, $8, $7, 1, $7
		WHERE NOT EXISTS (
			SELECT 1 FROM platform.event_outbox WHERE event_id = $1
		)
		ON CONFLICT DO NOTHING`,
		eventID, protocol.Version, commandID, topic, topicSequence, eventType, occurredAt, payload,
	)
	return err
}

func openingFinancials(companyIndex int) (cash, inventoryValue, liabilities int64) {
	if companyIndex == SeededActorIndex {
		return 1_248_000_000_000, 3_491_000_000_000, 2_136_000_000_000
	}
	cash = int64(400_000_000_000 + companyIndex*2_000_000_000)
	inventoryValue = int64(700_000_000_000 + companyIndex*3_000_000_000)
	liabilities = int64(450_000_000_000 + companyIndex*1_000_000_000)
	return
}

func cashReservationMinor(priceMinor, quantityMinor int64, quantityScale int16) int64 {
	return priceMinor * quantityMinor / int64(math.Pow10(int(quantityScale)))
}

func holdingCostBasis(companyIndex, commodityIndex int) int64 {
	_, inventoryValue, _ := openingFinancials(companyIndex)
	waterBasis := inventoryValue * 3 / 5
	loxBasis := inventoryValue - waterBasis
	const seededTradeValueMinor int64 = 936_720
	if commodityIndex == seedWaterCommodity {
		return waterBasis
	}
	if companyIndex == 1 {
		loxBasis += seededTradeValueMinor
	}
	if companyIndex == 2 {
		loxBasis -= seededTradeValueMinor
	}
	return loxBasis
}

func scenarioID(namespace, index int) string {
	return fixtures.ScenarioID(namespace, index)
}

func accountID(companyIndex, accountOffset int) string {
	return scenarioID(fixtures.NamespaceAccount, companyIndex*10+accountOffset)
}

func locationID(index int) string {
	return scenarioID(fixtures.NamespaceLocation, index)
}

func commodityID(index int) string {
	return scenarioID(fixtures.NamespaceCommodity, index)
}

func holdingID(companyIndex, commodityIndex int) string {
	return scenarioID(fixtures.NamespaceHolding, companyIndex*10+commodityIndex)
}

func movementID(companyIndex, commodityIndex int) string {
	return scenarioID(fixtures.NamespaceMovement, companyIndex*10+commodityIndex)
}

func marketID(index int) string {
	return scenarioID(fixtures.NamespaceMarket, index)
}

func stringPointer(value string) *string {
	return &value
}

func mustJSON(value any) json.RawMessage {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return encoded
}
