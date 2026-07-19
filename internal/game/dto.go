package game

import (
	"encoding/json"
	"time"

	"ascent/internal/companies"
	"ascent/internal/freight"
	"ascent/internal/inventory"
	"ascent/internal/ledger"
	"ascent/internal/markets"
	"ascent/internal/production"
)

const ProtocolVersion = "1.0.0"

type CommandType string

const (
	CommandMarketPlaceOrder   CommandType = "market.place_order"
	CommandMarketCancelOrder  CommandType = "market.cancel_order"
	CommandProductionRun      CommandType = "production.run"
	CommandFreightDeliver     CommandType = "freight.deliver"
	CommandOperatorCompensate CommandType = "operator.compensate"
)

// CommandEnvelope mirrors the transport envelope. HTTP identity middleware
// must authenticate ActorID; AuthenticatedMeta deliberately replaces any
// actor value supplied by an untrusted client.
type CommandEnvelope struct {
	ProtocolVersion    string          `json:"protocolVersion"`
	CommandID          string          `json:"commandId"`
	IdempotencyKey     string          `json:"idempotencyKey"`
	Type               CommandType     `json:"type"`
	ActorID            string          `json:"actorId"`
	CompanyID          string          `json:"companyId"`
	ExpectedVersion    int64           `json:"expectedVersion"`
	ReceivedAt         time.Time       `json:"receivedAt"`
	RequestFingerprint string          `json:"requestFingerprint,omitempty"`
	Payload            json.RawMessage `json:"payload"`
}

type CommandMeta struct {
	ProtocolVersion    string
	CommandID          string
	IdempotencyKey     string
	Type               CommandType
	ActorID            string
	CompanyID          string
	ExpectedVersion    int64
	RequestFingerprint string
}

func (e CommandEnvelope) AuthenticatedMeta(authenticatedActorID, requestFingerprint string) CommandMeta {
	if requestFingerprint == "" {
		requestFingerprint = e.RequestFingerprint
	}
	return CommandMeta{
		ProtocolVersion:    e.ProtocolVersion,
		CommandID:          e.CommandID,
		IdempotencyKey:     e.IdempotencyKey,
		Type:               e.Type,
		ActorID:            authenticatedActorID,
		CompanyID:          e.CompanyID,
		ExpectedVersion:    e.ExpectedVersion,
		RequestFingerprint: requestFingerprint,
	}
}

type SubmitOrderCommand struct {
	Meta       CommandMeta  `json:"-"`
	MarketID   string       `json:"marketId"`
	OrderID    string       `json:"orderId"`
	Side       markets.Side `json:"side"`
	PriceMinor int64        `json:"priceMinor"`
	Quantity   int64        `json:"quantity"`
	Sequence   uint64       `json:"sequence"`
}

type CancelOrderCommand struct {
	Meta     CommandMeta `json:"-"`
	MarketID string      `json:"marketId"`
	OrderID  string      `json:"orderId"`
}

type RunProductionCommand struct {
	Meta        CommandMeta `json:"-"`
	JobID       string      `json:"jobId"`
	AtTick      int64       `json:"atTick"`
	ExecutionID string      `json:"executionId"`
}

type DeliverFreightCommand struct {
	Meta       CommandMeta `json:"-"`
	ContractID string      `json:"contractId"`
	AtTick     int64       `json:"atTick"`
	DeliveryID string      `json:"deliveryId"`
}

type CompensateJournalCommand struct {
	Meta           CommandMeta `json:"-"`
	JournalID      string      `json:"journalId"`
	CompensationID string      `json:"compensationId"`
	Reason         string      `json:"reason"`
}

type CommandResult struct {
	CommandID       string      `json:"commandId"`
	OperationID     string      `json:"operationId"`
	ResultID        string      `json:"resultId"`
	Type            CommandType `json:"type"`
	Replay          bool        `json:"replay"`
	SnapshotVersion int64       `json:"snapshotVersion"`
	TradeIDs        []string    `json:"tradeIds,omitempty"`
}

type MarketSnapshot struct {
	Market markets.Market  `json:"market"`
	Orders []markets.Order `json:"orders"`
	Trades []markets.Trade `json:"trades"`
}

// Snapshot contains the authoritative economy projection. Identity, realtime
// freshness, devices, chat, and operator audit are composed by their owning
// modules at the HTTP boundary.
type Snapshot struct {
	Version          int64                   `json:"version"`
	Companies        []companies.Company     `json:"companies"`
	Memberships      []companies.Membership  `json:"memberships"`
	CashAccounts     []CashAccount           `json:"cashAccounts"`
	Balances         []ledger.Balance        `json:"balances"`
	Holdings         []inventory.Holding     `json:"holdings"`
	Reservations     []inventory.Reservation `json:"reservations"`
	Markets          []MarketSnapshot        `json:"markets"`
	Facilities       []production.Facility   `json:"facilities"`
	ProductionJobs   []production.Job        `json:"productionJobs"`
	FreightContracts []freight.Contract      `json:"freightContracts"`
}
