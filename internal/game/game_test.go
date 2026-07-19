package game

import (
	"errors"
	"testing"

	"ascent/internal/companies"
	"ascent/internal/freight"
	"ascent/internal/inventory"
	"ascent/internal/ledger"
	"ascent/internal/markets"
	"ascent/internal/production"
)

func TestOrderAuthorizationIdempotencyAndChangedPayloadConflict(t *testing.T) {
	state := seededState(t)
	unauthorized := SubmitOrderCommand{
		Meta:       orderMeta("command-unauthorized", "retry-unauthorized", "fingerprint-a", "player-auditor", "buyer", 0),
		MarketID:   "market-water",
		OrderID:    "bid-unauthorized",
		Side:       markets.SideBuy,
		PriceMinor: 100,
		Quantity:   1,
		Sequence:   1,
	}
	returned, _, err := state.SubmitOrder(unauthorized, nil)
	if !errors.Is(err, companies.ErrUnauthorized) {
		t.Fatalf("expected authorization failure, got %v", err)
	}
	if returned.Version() != 0 || len(mustMarket(t, returned).Orders()) != 0 {
		t.Fatal("unauthorized command changed state")
	}

	sell := SubmitOrderCommand{
		Meta:       orderMeta("command-sell", "retry-sell", "fingerprint-sell", "player-seller", "seller", 0),
		MarketID:   "market-water",
		OrderID:    "ask-1",
		Side:       markets.SideSell,
		PriceMinor: 100,
		Quantity:   5,
		Sequence:   2,
	}
	next, first, err := state.SubmitOrder(sell, nil)
	if err != nil {
		t.Fatal(err)
	}
	retried, replay, err := next.SubmitOrder(sell, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !replay.Replay || replay.ResultID != first.ResultID || retried.Version() != next.Version() {
		t.Fatalf("unexpected replay: %#v", replay)
	}
	if len(mustMarket(t, retried).Orders()) != 1 || len(retried.Inventory().Reservations()) != 1 {
		t.Fatal("identical retry duplicated order or reservation")
	}

	changed := sell
	changed.Meta.RequestFingerprint = "changed"
	if _, _, err := next.SubmitOrder(changed, nil); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("expected changed-payload conflict, got %v", err)
	}
}

func TestCrossingOrderSettlesAtomicallyAndInjectedFailureRollsBack(t *testing.T) {
	state := seededState(t)
	state, _, err := state.SubmitOrder(SubmitOrderCommand{
		Meta:       orderMeta("command-sell", "retry-sell", "fingerprint-sell", "player-seller", "seller", 0),
		MarketID:   "market-water",
		OrderID:    "ask-1",
		Side:       markets.SideSell,
		PriceMinor: 100,
		Quantity:   5,
		Sequence:   1,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	buy := SubmitOrderCommand{
		Meta:       orderMeta("command-buy", "retry-buy", "fingerprint-buy", "player-buyer", "buyer", 1),
		MarketID:   "market-water",
		OrderID:    "bid-1",
		Side:       markets.SideBuy,
		PriceMinor: 105,
		Quantity:   3,
		Sequence:   2,
	}
	injected := errors.New("injected settlement failure")
	rolledBack, _, err := state.SubmitOrder(buy, func(stage string) error {
		if stage == FailureAfterMatch {
			return injected
		}
		return nil
	})
	if !errors.Is(err, injected) {
		t.Fatalf("expected injected error, got %v", err)
	}
	if rolledBack.Version() != 1 || len(mustMarket(t, rolledBack).Trades()) != 0 {
		t.Fatal("failed match changed market state")
	}
	if balance, _ := rolledBack.CashBalance("buyer", "CR"); balance != 1_000 {
		t.Fatalf("failed match changed cash to %d", balance)
	}
	sellerPosition := inventory.Position{CompanyID: "seller", LocationID: "moon", ProductID: "water"}
	if rolledBack.Inventory().Quantity(sellerPosition) != 10 ||
		rolledBack.Inventory().Reservations()[0].Quantity != 5 {
		t.Fatal("failed match changed inventory")
	}

	settled, result, err := state.SubmitOrder(buy, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.TradeIDs) != 1 {
		t.Fatalf("trade ids = %#v", result.TradeIDs)
	}
	buyerCash, _ := settled.CashBalance("buyer", "CR")
	sellerCash, _ := settled.CashBalance("seller", "CR")
	if buyerCash != 700 || sellerCash != 300 {
		t.Fatalf("unexpected cash settlement buyer=%d seller=%d", buyerCash, sellerCash)
	}
	buyerPosition := inventory.Position{CompanyID: "buyer", LocationID: "moon", ProductID: "water"}
	if settled.Inventory().Quantity(sellerPosition) != 7 || settled.Inventory().Quantity(buyerPosition) != 3 {
		t.Fatalf("unexpected inventory settlement: %#v", settled.Inventory().Holdings())
	}
}

func TestBuyOrderCannotReserveBeyondCash(t *testing.T) {
	state := seededState(t)
	returned, _, err := state.SubmitOrder(SubmitOrderCommand{
		Meta:       orderMeta("command-buy", "retry-buy", "fingerprint-buy", "player-buyer", "buyer", 0),
		MarketID:   "market-water",
		OrderID:    "bid-too-large",
		Side:       markets.SideBuy,
		PriceMinor: 101,
		Quantity:   10,
		Sequence:   1,
	}, nil)
	if !errors.Is(err, markets.ErrInsufficientCapacity) {
		t.Fatalf("expected overspend rejection, got %v", err)
	}
	if returned.Version() != 0 || len(mustMarket(t, returned).Orders()) != 0 {
		t.Fatal("overspend changed state")
	}
}

func TestProductionFailureRollsBackJobAndInventory(t *testing.T) {
	state := seededState(t)
	command := RunProductionCommand{
		Meta:        commandMeta(CommandProductionRun, "command-production", "retry-production", "fingerprint-production", "player-producer", "producer", 0),
		JobID:       "job-1",
		AtTick:      10,
		ExecutionID: "execution-1",
	}
	injected := errors.New("injected inventory failure")
	rolledBack, _, err := state.RunProduction(command, func(stage string) error {
		if stage == FailureAfterInventory {
			return injected
		}
		return nil
	})
	if !errors.Is(err, injected) {
		t.Fatalf("expected injected error, got %v", err)
	}
	if rolledBack.Production().Jobs()[0].Status != production.JobScheduled {
		t.Fatal("failed production completed job")
	}
	ice := inventory.Position{CompanyID: "producer", LocationID: "moon", ProductID: "ice"}
	if rolledBack.Inventory().Quantity(ice) != 6 {
		t.Fatal("failed production consumed input")
	}

	completed, _, err := state.RunProduction(command, nil)
	if err != nil {
		t.Fatal(err)
	}
	water := inventory.Position{CompanyID: "producer", LocationID: "moon", ProductID: "water"}
	if completed.Inventory().Quantity(ice) != 0 || completed.Inventory().Quantity(water) != 4 {
		t.Fatalf("unexpected production holdings: %#v", completed.Inventory().Holdings())
	}
}

func TestDeterministicIDIsStableAndNamespaced(t *testing.T) {
	first := DeterministicID("trade", "market-a", "1")
	if first != DeterministicID("trade", "market-a", "1") {
		t.Fatal("deterministic id changed for identical input")
	}
	if first == DeterministicID("job", "market-a", "1") {
		t.Fatal("deterministic id ignored namespace")
	}
}

func seededState(t *testing.T) State {
	t.Helper()
	directory, err := companies.NewDirectory(
		[]companies.Company{
			{ID: "buyer", Name: "Buyer"},
			{ID: "seller", Name: "Seller"},
			{ID: "producer", Name: "Producer"},
			{ID: "carrier", Name: "Carrier"},
			{ID: "shipper", Name: "Shipper"},
			{ID: "ops", Name: "Operations"},
		},
		[]companies.Membership{
			{PlayerID: "player-buyer", CompanyID: "buyer", Roles: []companies.Role{companies.RoleTrader}},
			{PlayerID: "player-seller", CompanyID: "seller", Roles: []companies.Role{companies.RoleTrader}},
			{PlayerID: "player-auditor", CompanyID: "buyer", Roles: []companies.Role{companies.RoleAuditor}},
			{PlayerID: "player-producer", CompanyID: "producer", Roles: []companies.Role{companies.RoleProducer}},
			{PlayerID: "player-carrier", CompanyID: "carrier", Roles: []companies.Role{companies.RoleLogistics}},
			{PlayerID: "player-operator", CompanyID: "ops", Roles: []companies.Role{companies.RoleOperator}},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	capital, err := ledger.NewJournal(ledger.JournalDraft{
		ID:          "seed-capital",
		OperationID: "seed",
		Description: "seed buyer capital",
		Entries: []ledger.Entry{
			{AccountID: "cash-buyer", Currency: "CR", Amount: 1_000},
			{AccountID: "seed-equity", Currency: "CR", Amount: -1_000},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	book, err := ledger.NewBook([]ledger.Journal{capital})
	if err != nil {
		t.Fatal(err)
	}
	stock, err := inventory.NewState([]inventory.Holding{
		{
			Position: inventory.Position{CompanyID: "seller", LocationID: "moon", ProductID: "water"},
			Quantity: 10,
		},
		{
			Position: inventory.Position{CompanyID: "producer", LocationID: "moon", ProductID: "ice"},
			Quantity: 6,
		},
		{
			Position: inventory.Position{CompanyID: "shipper", LocationID: "moon", ProductID: "water"},
			Quantity: 20,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	market, err := markets.NewBook(markets.Market{
		ID: "market-water", ProductID: "water", Currency: "CR", LocationID: "moon",
		QuantityScale: 1, LotSize: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	productionState, err := production.NewState(
		[]production.Facility{{
			ID: "facility-1", CompanyID: "producer", LocationID: "moon", RecipeID: "recipe-1",
		}},
		[]production.Recipe{{
			ID: "recipe-1", InputProductID: "ice", InputQuantity: 3,
			OutputProductID: "water", OutputQuantity: 2,
		}},
		[]production.Job{{
			ID: "job-1", FacilityID: "facility-1", Runs: 2, DueTick: 10, Status: production.JobScheduled,
		}},
	)
	if err != nil {
		t.Fatal(err)
	}
	freightState, err := freight.NewState([]freight.Contract{{
		ID: "contract-1", OwnerCompanyID: "shipper", CarrierCompanyID: "carrier",
		ProductID: "water", Quantity: 20, OriginLocationID: "moon",
		DestinationLocationID: "orbit", DueTick: 10, Status: freight.ContractInTransit,
	}})
	if err != nil {
		t.Fatal(err)
	}
	state, err := NewState(Seed{
		Companies:  directory,
		Ledger:     book,
		Inventory:  stock,
		Markets:    []markets.Book{market},
		Production: productionState,
		Freight:    freightState,
		CashAccounts: []CashAccount{
			{CompanyID: "buyer", Currency: "CR", AccountID: "cash-buyer"},
			{CompanyID: "seller", Currency: "CR", AccountID: "cash-seller"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func orderMeta(commandID, retry, fingerprint, player, company string, version int64) CommandMeta {
	return commandMeta(CommandMarketPlaceOrder, commandID, retry, fingerprint, player, company, version)
}

func commandMeta(commandType CommandType, commandID, retry, fingerprint, player, company string, version int64) CommandMeta {
	return CommandMeta{
		ProtocolVersion:    ProtocolVersion,
		CommandID:          commandID,
		IdempotencyKey:     retry,
		Type:               commandType,
		ActorID:            player,
		CompanyID:          company,
		ExpectedVersion:    version,
		RequestFingerprint: fingerprint,
	}
}

func mustMarket(t *testing.T, state State) markets.Book {
	t.Helper()
	book, exists := state.Market("market-water")
	if !exists {
		t.Fatal("missing market")
	}
	return book
}
