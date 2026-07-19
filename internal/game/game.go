// Package game coordinates the compact first-playable economic loop. All
// operations stage immutable domain values and return a new State only after
// every invariant and optional failure hook succeeds.
package game

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"

	"ascent/internal/companies"
	"ascent/internal/freight"
	"ascent/internal/inventory"
	"ascent/internal/ledger"
	"ascent/internal/markets"
	"ascent/internal/operator"
	"ascent/internal/production"
)

var (
	ErrInvalidCommand      = errors.New("invalid game command")
	ErrVersionConflict     = errors.New("game snapshot version conflict")
	ErrIdempotencyConflict = errors.New("idempotency key reused with changed request")
	ErrUnknownMarket       = errors.New("unknown market")
	ErrUnknownCashAccount  = errors.New("unknown company cash account")
	ErrInsufficientFunds   = errors.New("insufficient unreserved funds")
	ErrAlreadyCompensated  = errors.New("journal already compensated")
	ErrVersionOverflow     = errors.New("game snapshot version overflow")
)

const (
	FailureAfterAuthorization = "after_authorization"
	FailureAfterReservation   = "after_reservation"
	FailureAfterMatch         = "after_match"
	FailureAfterSettlement    = "after_settlement"
	FailureAfterJob           = "after_job"
	FailureAfterInventory     = "after_inventory"
	FailureAfterDelivery      = "after_delivery"
	FailureAfterCompensation  = "after_compensation"
	FailureBeforeCommit       = "before_commit"
)

type FailureHook func(stage string) error

type CashAccount struct {
	CompanyID string `json:"companyId"`
	Currency  string `json:"currency"`
	AccountID string `json:"accountId"`
}

type Seed struct {
	Version      int64
	Companies    companies.Directory
	Ledger       ledger.Book
	Inventory    inventory.State
	Markets      []markets.Book
	Production   production.State
	Freight      freight.State
	CashAccounts []CashAccount
}

type cashKey struct {
	companyID string
	currency  string
}

type commandReceipt struct {
	fingerprint string
	result      CommandResult
}

type State struct {
	version      int64
	companies    companies.Directory
	ledger       ledger.Book
	inventory    inventory.State
	markets      map[string]markets.Book
	production   production.State
	freight      freight.State
	cashAccounts map[cashKey]string
	receipts     map[string]commandReceipt
}

func NewState(seed Seed) (State, error) {
	if seed.Version < 0 {
		return State{}, fmt.Errorf("%w: negative version", ErrInvalidCommand)
	}
	state := State{
		version:      seed.Version,
		companies:    seed.Companies,
		ledger:       seed.Ledger,
		inventory:    seed.Inventory,
		markets:      make(map[string]markets.Book, len(seed.Markets)),
		production:   seed.Production,
		freight:      seed.Freight,
		cashAccounts: make(map[cashKey]string, len(seed.CashAccounts)),
		receipts:     make(map[string]commandReceipt),
	}
	for _, book := range seed.Markets {
		market := book.Market()
		if market.ID == "" {
			return State{}, fmt.Errorf("%w: market id", ErrInvalidCommand)
		}
		if _, exists := state.markets[market.ID]; exists {
			return State{}, fmt.Errorf("%w: duplicate market %q", ErrInvalidCommand, market.ID)
		}
		state.markets[market.ID] = book
	}
	for _, account := range seed.CashAccounts {
		if account.CompanyID == "" || account.Currency == "" || account.AccountID == "" {
			return State{}, fmt.Errorf("%w: cash account metadata", ErrInvalidCommand)
		}
		key := cashKey{companyID: account.CompanyID, currency: account.Currency}
		if _, exists := state.cashAccounts[key]; exists {
			return State{}, fmt.Errorf("%w: duplicate cash account", ErrInvalidCommand)
		}
		state.cashAccounts[key] = account.AccountID
	}
	return state, nil
}

func (s State) SubmitOrder(command SubmitOrderCommand, failure FailureHook) (State, CommandResult, error) {
	if replay, found, err := s.begin(command.Meta, CommandMarketPlaceOrder); found || err != nil {
		return s, replay, err
	}
	if err := s.companies.Authorize(command.Meta.ActorID, command.Meta.CompanyID, companies.PermissionTrade); err != nil {
		return s, CommandResult{}, err
	}
	if err := invokeFailure(failure, FailureAfterAuthorization); err != nil {
		return s, CommandResult{}, err
	}
	book, exists := s.markets[command.MarketID]
	if !exists {
		return s, CommandResult{}, fmt.Errorf("%w: %q", ErrUnknownMarket, command.MarketID)
	}
	market := book.Market()
	orderRequest := markets.OrderRequest{
		ID:          command.OrderID,
		OperationID: command.Meta.CommandID,
		CompanyID:   command.Meta.CompanyID,
		Side:        command.Side,
		Price:       command.PriceMinor,
		Quantity:    command.Quantity,
		Sequence:    command.Sequence,
	}

	stage := s.clone()
	var available int64
	var err error
	if command.Side == markets.SideBuy {
		available, err = stage.availableCash(command.Meta.CompanyID, market.Currency)
		if err != nil {
			return s, CommandResult{}, err
		}
	} else {
		position := inventory.Position{
			CompanyID:  command.Meta.CompanyID,
			LocationID: market.LocationID,
			ProductID:  market.ProductID,
		}
		available = stage.inventory.Available(position)
		stage.inventory, err = stage.inventory.Reserve(inventory.Reservation{
			ID:          command.OrderID,
			OperationID: command.Meta.CommandID,
			Purpose:     "market sell order",
			Position:    position,
			Quantity:    command.Quantity,
		})
		if err != nil {
			return s, CommandResult{}, err
		}
	}
	if err := invokeFailure(failure, FailureAfterReservation); err != nil {
		return s, CommandResult{}, err
	}

	nextBook, execution, err := book.Submit(orderRequest, available)
	if err != nil {
		return s, CommandResult{}, err
	}
	stage.markets[market.ID] = nextBook
	if err := invokeFailure(failure, FailureAfterMatch); err != nil {
		return s, CommandResult{}, err
	}

	tradeIDs := make([]string, 0, len(execution.Trades))
	buyers := make(map[string]struct{})
	for _, trade := range execution.Trades {
		stage.inventory, err = stage.inventory.SettleReservation(trade.SellOrderID, inventory.Movement{
			ID:          trade.ID + "-inventory",
			OperationID: command.Meta.CommandID,
			From: inventory.Position{
				CompanyID:  trade.SellerID,
				LocationID: market.LocationID,
				ProductID:  market.ProductID,
			},
			To: inventory.Position{
				CompanyID:  trade.BuyerID,
				LocationID: market.LocationID,
				ProductID:  market.ProductID,
			},
			Quantity: trade.Quantity,
		})
		if err != nil {
			return s, CommandResult{}, err
		}
		buyerAccount, err := stage.cashAccount(trade.BuyerID, market.Currency)
		if err != nil {
			return s, CommandResult{}, err
		}
		sellerAccount, err := stage.cashAccount(trade.SellerID, market.Currency)
		if err != nil {
			return s, CommandResult{}, err
		}
		journal, err := ledger.NewJournal(ledger.JournalDraft{
			ID:                 trade.ID + "-settlement",
			OperationID:        command.Meta.CommandID,
			IdempotencyKey:     command.Meta.IdempotencyKey,
			RequestFingerprint: command.Meta.RequestFingerprint,
			Description:        "market trade settlement",
			Entries: []ledger.Entry{
				{AccountID: buyerAccount, Currency: market.Currency, Amount: -trade.QuoteAmount},
				{AccountID: sellerAccount, Currency: market.Currency, Amount: trade.QuoteAmount},
			},
		})
		if err != nil {
			return s, CommandResult{}, err
		}
		stage.ledger, err = stage.ledger.Apply(journal)
		if err != nil {
			return s, CommandResult{}, err
		}
		buyers[trade.BuyerID] = struct{}{}
		tradeIDs = append(tradeIDs, trade.ID)
	}
	for buyerID := range buyers {
		available, err := stage.availableCash(buyerID, market.Currency)
		if err != nil {
			return s, CommandResult{}, err
		}
		if available < 0 {
			return s, CommandResult{}, ErrInsufficientFunds
		}
	}
	if err := invokeFailure(failure, FailureAfterSettlement); err != nil {
		return s, CommandResult{}, err
	}

	result := CommandResult{
		CommandID:   command.Meta.CommandID,
		OperationID: command.Meta.CommandID,
		ResultID:    command.OrderID,
		Type:        CommandMarketPlaceOrder,
		TradeIDs:    tradeIDs,
	}
	return stage.finish(s, command.Meta, result, failure)
}

func (s State) CancelOrder(command CancelOrderCommand, failure FailureHook) (State, CommandResult, error) {
	if replay, found, err := s.begin(command.Meta, CommandMarketCancelOrder); found || err != nil {
		return s, replay, err
	}
	if err := s.companies.Authorize(command.Meta.ActorID, command.Meta.CompanyID, companies.PermissionTrade); err != nil {
		return s, CommandResult{}, err
	}
	if err := invokeFailure(failure, FailureAfterAuthorization); err != nil {
		return s, CommandResult{}, err
	}
	book, exists := s.markets[command.MarketID]
	if !exists {
		return s, CommandResult{}, fmt.Errorf("%w: %q", ErrUnknownMarket, command.MarketID)
	}
	nextBook, canceled, err := book.Cancel(command.Meta.CompanyID, command.OrderID)
	if err != nil {
		return s, CommandResult{}, err
	}
	stage := s.clone()
	stage.markets[command.MarketID] = nextBook
	if canceled.ReservationEffect.Asset == markets.ReservationBase && canceled.ReservationEffect.Released > 0 {
		stage.inventory, err = stage.inventory.Release(command.OrderID, canceled.ReservationEffect.Released)
		if err != nil {
			return s, CommandResult{}, err
		}
	}
	result := CommandResult{
		CommandID:   command.Meta.CommandID,
		OperationID: command.Meta.CommandID,
		ResultID:    command.OrderID,
		Type:        CommandMarketCancelOrder,
	}
	return stage.finish(s, command.Meta, result, failure)
}

func (s State) RunProduction(command RunProductionCommand, failure FailureHook) (State, CommandResult, error) {
	if replay, found, err := s.begin(command.Meta, CommandProductionRun); found || err != nil {
		return s, replay, err
	}
	if err := s.companies.Authorize(command.Meta.ActorID, command.Meta.CompanyID, companies.PermissionProduce); err != nil {
		return s, CommandResult{}, err
	}
	if err := invokeFailure(failure, FailureAfterAuthorization); err != nil {
		return s, CommandResult{}, err
	}
	nextProduction, execution, err := s.production.ExecuteDue(production.ExecuteCommand{
		JobID:              command.JobID,
		CompanyID:          command.Meta.CompanyID,
		AtTick:             command.AtTick,
		ExecutionID:        command.ExecutionID,
		OperationID:        command.Meta.CommandID,
		IdempotencyKey:     command.Meta.IdempotencyKey,
		RequestFingerprint: command.Meta.RequestFingerprint,
	})
	if err != nil {
		return s, CommandResult{}, err
	}
	if execution.Replay {
		return s, CommandResult{
			CommandID:       command.Meta.CommandID,
			OperationID:     execution.OperationID,
			ResultID:        execution.ID,
			Type:            CommandProductionRun,
			Replay:          true,
			SnapshotVersion: s.version,
		}, nil
	}
	if err := invokeFailure(failure, FailureAfterJob); err != nil {
		return s, CommandResult{}, err
	}
	stage := s.clone()
	stage.production = nextProduction
	stage.inventory, err = stage.inventory.ApplyTransformation(execution.Transformation)
	if err != nil {
		return s, CommandResult{}, err
	}
	if err := invokeFailure(failure, FailureAfterInventory); err != nil {
		return s, CommandResult{}, err
	}
	result := CommandResult{
		CommandID:   command.Meta.CommandID,
		OperationID: command.Meta.CommandID,
		ResultID:    execution.ID,
		Type:        CommandProductionRun,
	}
	return stage.finish(s, command.Meta, result, failure)
}

func (s State) DeliverFreight(command DeliverFreightCommand, failure FailureHook) (State, CommandResult, error) {
	if replay, found, err := s.begin(command.Meta, CommandFreightDeliver); found || err != nil {
		return s, replay, err
	}
	if err := s.companies.Authorize(command.Meta.ActorID, command.Meta.CompanyID, companies.PermissionFreight); err != nil {
		return s, CommandResult{}, err
	}
	if err := invokeFailure(failure, FailureAfterAuthorization); err != nil {
		return s, CommandResult{}, err
	}
	nextFreight, delivery, err := s.freight.Deliver(freight.DeliverCommand{
		ContractID:         command.ContractID,
		CarrierCompanyID:   command.Meta.CompanyID,
		AtTick:             command.AtTick,
		DeliveryID:         command.DeliveryID,
		OperationID:        command.Meta.CommandID,
		IdempotencyKey:     command.Meta.IdempotencyKey,
		RequestFingerprint: command.Meta.RequestFingerprint,
	})
	if err != nil {
		return s, CommandResult{}, err
	}
	if delivery.Replay {
		return s, CommandResult{
			CommandID:       command.Meta.CommandID,
			OperationID:     delivery.OperationID,
			ResultID:        delivery.ID,
			Type:            CommandFreightDeliver,
			Replay:          true,
			SnapshotVersion: s.version,
		}, nil
	}
	stage := s.clone()
	stage.freight = nextFreight
	stage.inventory, err = stage.inventory.Transfer(delivery.Movement)
	if err != nil {
		return s, CommandResult{}, err
	}
	if err := invokeFailure(failure, FailureAfterDelivery); err != nil {
		return s, CommandResult{}, err
	}
	result := CommandResult{
		CommandID:   command.Meta.CommandID,
		OperationID: command.Meta.CommandID,
		ResultID:    delivery.ID,
		Type:        CommandFreightDeliver,
	}
	return stage.finish(s, command.Meta, result, failure)
}

func (s State) CompensateJournal(command CompensateJournalCommand, failure FailureHook) (State, CommandResult, error) {
	if replay, found, err := s.begin(command.Meta, CommandOperatorCompensate); found || err != nil {
		return s, replay, err
	}
	if err := s.companies.Authorize(command.Meta.ActorID, command.Meta.CompanyID, companies.PermissionCompensate); err != nil {
		return s, CommandResult{}, err
	}
	if err := invokeFailure(failure, FailureAfterAuthorization); err != nil {
		return s, CommandResult{}, err
	}
	original, exists := s.ledger.Journal(command.JournalID)
	if !exists {
		return s, CommandResult{}, fmt.Errorf("%w: %q", ledger.ErrUnknownJournal, command.JournalID)
	}
	if original.CompensatesID() != "" {
		return s, CommandResult{}, ErrAlreadyCompensated
	}
	for _, journal := range s.ledger.Journals() {
		if journal.CompensatesID() == original.ID() {
			return s, CommandResult{}, ErrAlreadyCompensated
		}
	}
	compensation, err := operator.BuildCompensation(operator.CompensationCommand{
		JournalID:          command.JournalID,
		CompensationID:     command.CompensationID,
		OperationID:        command.Meta.CommandID,
		IdempotencyKey:     command.Meta.IdempotencyKey,
		RequestFingerprint: command.Meta.RequestFingerprint,
		Reason:             command.Reason,
	}, original)
	if err != nil {
		return s, CommandResult{}, err
	}
	stage := s.clone()
	stage.ledger, err = stage.ledger.Apply(compensation)
	if err != nil {
		return s, CommandResult{}, err
	}
	if err := invokeFailure(failure, FailureAfterCompensation); err != nil {
		return s, CommandResult{}, err
	}
	result := CommandResult{
		CommandID:   command.Meta.CommandID,
		OperationID: command.Meta.CommandID,
		ResultID:    command.CompensationID,
		Type:        CommandOperatorCompensate,
	}
	return stage.finish(s, command.Meta, result, failure)
}

func (s State) Snapshot(playerID string) Snapshot {
	marketSnapshots := make([]MarketSnapshot, 0, len(s.markets))
	for _, book := range s.markets {
		marketSnapshots = append(marketSnapshots, MarketSnapshot{
			Market: book.Market(),
			Orders: book.Orders(),
			Trades: book.Trades(),
		})
	}
	sort.Slice(marketSnapshots, func(i, j int) bool {
		return marketSnapshots[i].Market.ID < marketSnapshots[j].Market.ID
	})
	accounts := make([]CashAccount, 0, len(s.cashAccounts))
	for key, accountID := range s.cashAccounts {
		accounts = append(accounts, CashAccount{
			CompanyID: key.companyID,
			Currency:  key.currency,
			AccountID: accountID,
		})
	}
	sort.Slice(accounts, func(i, j int) bool {
		if accounts[i].CompanyID != accounts[j].CompanyID {
			return accounts[i].CompanyID < accounts[j].CompanyID
		}
		return accounts[i].Currency < accounts[j].Currency
	})
	return Snapshot{
		Version:          s.version,
		Companies:        s.companies.Companies(),
		Memberships:      s.companies.Memberships(playerID),
		CashAccounts:     accounts,
		Balances:         s.ledger.Balances(),
		Holdings:         s.inventory.Holdings(),
		Reservations:     s.inventory.Reservations(),
		Markets:          marketSnapshots,
		Facilities:       s.production.Facilities(),
		ProductionJobs:   s.production.Jobs(),
		FreightContracts: s.freight.Contracts(),
	}
}

func (s State) Version() int64                 { return s.version }
func (s State) Ledger() ledger.Book            { return s.ledger }
func (s State) Inventory() inventory.State     { return s.inventory }
func (s State) Production() production.State   { return s.production }
func (s State) Freight() freight.State         { return s.freight }
func (s State) Companies() companies.Directory { return s.companies }

func (s State) Market(id string) (markets.Book, bool) {
	book, exists := s.markets[id]
	return book, exists
}

func (s State) CashBalance(companyID, currency string) (int64, error) {
	accountID, err := s.cashAccount(companyID, currency)
	if err != nil {
		return 0, err
	}
	return s.ledger.Balance(accountID, currency), nil
}

func (s State) begin(meta CommandMeta, expectedType CommandType) (CommandResult, bool, error) {
	if meta.ProtocolVersion != ProtocolVersion || meta.CommandID == "" || meta.IdempotencyKey == "" ||
		meta.Type != expectedType || meta.ActorID == "" || meta.CompanyID == "" ||
		meta.ExpectedVersion < 0 || meta.RequestFingerprint == "" {
		return CommandResult{}, false, ErrInvalidCommand
	}
	if previous, exists := s.receipts[receiptKey(meta)]; exists {
		if previous.fingerprint != meta.RequestFingerprint {
			return CommandResult{}, true, ErrIdempotencyConflict
		}
		replay := previous.result
		replay.Replay = true
		return replay, true, nil
	}
	if meta.ExpectedVersion != s.version {
		return CommandResult{}, false, fmt.Errorf("%w: expected %d, current %d", ErrVersionConflict, meta.ExpectedVersion, s.version)
	}
	return CommandResult{}, false, nil
}

func (s State) finish(original State, meta CommandMeta, result CommandResult, failure FailureHook) (State, CommandResult, error) {
	if err := invokeFailure(failure, FailureBeforeCommit); err != nil {
		return original, CommandResult{}, err
	}
	if s.version == math.MaxInt64 {
		return s, CommandResult{}, ErrVersionOverflow
	}
	s.version++
	result.SnapshotVersion = s.version
	s.receipts[receiptKey(meta)] = commandReceipt{
		fingerprint: meta.RequestFingerprint,
		result:      result,
	}
	return s, result, nil
}

func (s State) availableCash(companyID, currency string) (int64, error) {
	accountID, err := s.cashAccount(companyID, currency)
	if err != nil {
		return 0, err
	}
	available := s.ledger.Balance(accountID, currency)
	for _, book := range s.markets {
		if book.Market().Currency != currency {
			continue
		}
		reserved, err := book.ReservedQuote(companyID)
		if err != nil {
			return 0, err
		}
		if reserved > 0 && available < math.MinInt64+reserved {
			return 0, ErrInsufficientFunds
		}
		available -= reserved
	}
	return available, nil
}

func (s State) cashAccount(companyID, currency string) (string, error) {
	accountID, exists := s.cashAccounts[cashKey{companyID: companyID, currency: currency}]
	if !exists {
		return "", fmt.Errorf("%w: company %q currency %q", ErrUnknownCashAccount, companyID, currency)
	}
	return accountID, nil
}

func (s State) clone() State {
	next := s
	next.markets = make(map[string]markets.Book, len(s.markets))
	for id, book := range s.markets {
		next.markets[id] = book
	}
	next.cashAccounts = make(map[cashKey]string, len(s.cashAccounts))
	for key, account := range s.cashAccounts {
		next.cashAccounts[key] = account
	}
	next.receipts = make(map[string]commandReceipt, len(s.receipts)+1)
	for key, value := range s.receipts {
		next.receipts[key] = value
	}
	return next
}

func receiptKey(meta CommandMeta) string {
	return strings.Join([]string{meta.ActorID, meta.CompanyID, string(meta.Type), meta.IdempotencyKey}, "\x00")
}

func invokeFailure(hook FailureHook, stage string) error {
	if hook == nil {
		return nil
	}
	return hook(stage)
}

func DeterministicID(namespace string, parts ...string) string {
	digest := sha256.Sum256([]byte(namespace + "\x00" + strings.Join(parts, "\x00")))
	// RFC 4122-compatible version/variant bits make IDs convenient for UUID
	// columns while keeping fixture generation deterministic.
	digest[6] = (digest[6] & 0x0f) | 0x50
	digest[8] = (digest[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		digest[0:4], digest[4:6], digest[6:8], digest[8:10], digest[10:16])
}
