package gamepostgres

import (
	"math"
	"sort"
	"time"
)

const currencyScale int16 = 2

type GameSnapshot struct {
	SystemTime      string              `json:"systemTime"`
	Actor           SnapshotActor       `json:"actor"`
	Membership      SnapshotMembership  `json:"membership"`
	Company         SnapshotCompany     `json:"company"`
	Markets         []SnapshotMarket    `json:"markets"`
	OpenOrders      []SnapshotOpenOrder `json:"openOrders"`
	Trades          []SnapshotTrade     `json:"trades"`
	Inventory       []InventoryPosition `json:"inventory"`
	Facilities      []SnapshotFacility  `json:"facilities"`
	ProductionTrace []ProductionTrace   `json:"productionTrace"`
	Freight         []FreightShipment   `json:"freight"`
	Devices         []SnapshotDevice    `json:"devices"`
	Panels          []DevicePanel       `json:"panels"`
	Chat            []ChatMessage       `json:"chat"`
	Alerts          []SnapshotAlert     `json:"alerts"`
	OperatorAudit   []AuditEntry        `json:"operatorAudit"`
	Indices         []MarketIndex       `json:"indices"`
}

type SnapshotActor struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
	Status      string `json:"status"`
}

type SnapshotMembership struct {
	CompanyID   string   `json:"companyId"`
	Role        string   `json:"role"`
	Permissions []string `json:"permissions"`
}

type SnapshotCompany struct {
	ID                string             `json:"id"`
	Name              string             `json:"name"`
	Version           int64              `json:"version"`
	Cash              float64            `json:"cash"`
	TotalAssets       float64            `json:"totalAssets"`
	TotalLiabilities  float64            `json:"totalLiabilities"`
	NetWorth          float64            `json:"netWorth"`
	CreditRating      string             `json:"creditRating"`
	AvailableCredit   float64            `json:"availableCredit"`
	DebtToEquityRatio float64            `json:"debtToEquityRatio"`
	Statements        []CompanyStatement `json:"statements"`
}

type CompanyStatement struct {
	Label  string   `json:"label"`
	Value  float64  `json:"value"`
	Change *float64 `json:"change"`
}

type SnapshotMarket struct {
	ID           string       `json:"id"`
	Location     string       `json:"location"`
	Commodity    string       `json:"commodity"`
	Unit         string       `json:"unit"`
	Currency     string       `json:"currency"`
	LastPrice    float64      `json:"lastPrice"`
	Change24Hour float64      `json:"change24Hour"`
	Volume24Hour float64      `json:"volume24Hour"`
	Spread       float64      `json:"spread"`
	History      []PricePoint `json:"history"`
	OrderBook    OrderBook    `json:"orderBook"`
}

type PricePoint struct {
	Label string  `json:"label"`
	Value float64 `json:"value"`
}

type OrderBook struct {
	Bids []OrderLevel `json:"bids"`
	Asks []OrderLevel `json:"asks"`
}

type OrderLevel struct {
	Price    float64 `json:"price"`
	Quantity float64 `json:"quantity"`
	Orders   int64   `json:"orders"`
}

type SnapshotOpenOrder struct {
	ID             string    `json:"id"`
	MarketID       string    `json:"marketId"`
	Side           string    `json:"side"`
	OrderType      string    `json:"orderType"`
	Price          float64   `json:"price"`
	Quantity       float64   `json:"quantity"`
	FilledQuantity float64   `json:"filledQuantity"`
	Status         string    `json:"status"`
	CreatedAt      time.Time `json:"createdAt"`
}

type SnapshotTrade struct {
	ID           string    `json:"id"`
	MarketID     string    `json:"marketId"`
	Side         string    `json:"side"`
	Price        float64   `json:"price"`
	Quantity     float64   `json:"quantity"`
	Total        float64   `json:"total"`
	Counterparty string    `json:"counterparty"`
	OccurredAt   time.Time `json:"occurredAt"`
}

type InventoryPosition struct {
	ID        string  `json:"id"`
	Commodity string  `json:"commodity"`
	Location  string  `json:"location"`
	Quantity  float64 `json:"quantity"`
	Reserved  float64 `json:"reserved"`
	Unit      string  `json:"unit"`
}

type SnapshotFacility struct {
	ID              string  `json:"id"`
	Name            string  `json:"name"`
	Type            string  `json:"type"`
	Location        string  `json:"location"`
	Utilization     float64 `json:"utilization"`
	Change          float64 `json:"change"`
	Status          string  `json:"status"`
	InputCommodity  string  `json:"inputCommodity"`
	OutputCommodity string  `json:"outputCommodity"`
	Capacity        float64 `json:"capacity"`
	CapacityUnit    string  `json:"capacityUnit"`
}

type ProductionTrace struct {
	ID     string  `json:"id"`
	Label  string  `json:"label"`
	Value  string  `json:"value"`
	Change float64 `json:"change"`
	Depth  int     `json:"depth"`
	Status string  `json:"status"`
}

type FreightShipment struct {
	ID          string    `json:"id"`
	Origin      string    `json:"origin"`
	Destination string    `json:"destination"`
	Cargo       string    `json:"cargo"`
	Quantity    float64   `json:"quantity"`
	Unit        string    `json:"unit"`
	Status      string    `json:"status"`
	ETA         time.Time `json:"eta"`
}

type SnapshotDevice struct {
	ID           string     `json:"id"`
	Name         string     `json:"name"`
	Status       string     `json:"status"`
	LastSeenAt   *time.Time `json:"lastSeenAt"`
	Capabilities []string   `json:"capabilities"`
}

type DevicePanel struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	DeviceID    string  `json:"deviceId"`
	Status      string  `json:"status"`
	LastMessage *string `json:"lastMessage"`
}

type ChatMessage struct {
	ID         string    `json:"id"`
	ChannelID  string    `json:"channelId"`
	ActorID    string    `json:"actorId"`
	ActorName  string    `json:"actorName"`
	Body       string    `json:"body"`
	Kind       string    `json:"kind"`
	OccurredAt time.Time `json:"occurredAt"`
}

type SnapshotAlert struct {
	ID         string    `json:"id"`
	Severity   string    `json:"severity"`
	Summary    string    `json:"summary"`
	OccurredAt time.Time `json:"occurredAt"`
}

type AuditEntry struct {
	ID         string    `json:"id"`
	ActorName  string    `json:"actorName"`
	Action     string    `json:"action"`
	Target     string    `json:"target"`
	Outcome    string    `json:"outcome"`
	OccurredAt time.Time `json:"occurredAt"`
}

type MarketIndex struct {
	Name   string  `json:"name"`
	Value  float64 `json:"value"`
	Change float64 `json:"change"`
}

type financialPosition struct {
	Cash        int64
	Assets      int64
	Liabilities int64
	Revenue24h  int64
	Expense24h  int64
}

func fixedToDisplay(value int64, scale int16) float64 {
	if scale <= 0 {
		return float64(value)
	}
	return float64(value) / math.Pow10(int(scale))
}

func percentageChange(first, last int64) float64 {
	if first == 0 {
		return 0
	}
	return (float64(last-first) / float64(first)) * 100
}

func permissionsFor(role string, operator bool) []string {
	permissionSet := map[string]struct{}{
		"game.view": {},
		"chat.send": {},
	}
	switch role {
	case "owner":
		permissionSet["market.trade"] = struct{}{}
		permissionSet["production.run"] = struct{}{}
		permissionSet["freight.deliver"] = struct{}{}
		permissionSet["device.manage"] = struct{}{}
	case "operator":
		permissionSet["market.trade"] = struct{}{}
		permissionSet["production.run"] = struct{}{}
		permissionSet["freight.deliver"] = struct{}{}
		permissionSet["device.manage"] = struct{}{}
		permissionSet["operator.compensate"] = struct{}{}
	case "trader":
		permissionSet["market.trade"] = struct{}{}
	case "analyst", "viewer":
		// Read and chat permissions are already present.
	}
	if operator {
		permissionSet["operator.compensate"] = struct{}{}
	}
	permissions := make([]string, 0, len(permissionSet))
	for permission := range permissionSet {
		permissions = append(permissions, permission)
	}
	sort.Strings(permissions)
	return permissions
}

func companyMetrics(position financialPosition) (cash, assets, liabilities, netWorth, availableCredit, ratio float64, rating string) {
	cash = fixedToDisplay(position.Cash, currencyScale)
	assets = fixedToDisplay(position.Assets, currencyScale)
	liabilities = fixedToDisplay(position.Liabilities, currencyScale)
	netWorth = assets - liabilities
	if netWorth > 0 {
		ratio = liabilities / netWorth
		availableCredit = math.Max(0, netWorth/3)
	} else if liabilities > 0 {
		ratio = liabilities
	}
	switch {
	case ratio <= 0.5:
		rating = "A"
	case ratio <= 1:
		rating = "A-"
	case ratio <= 2:
		rating = "BBB"
	default:
		rating = "BB"
	}
	return
}

func snapshotOrderStatus(status string) string {
	if status == "partially_filled" {
		return status
	}
	return "open"
}

func snapshotFacilityStatus(status string) string {
	switch status {
	case "operational", "constrained":
		return status
	default:
		return "offline"
	}
}

func snapshotDeliveryStatus(status string) string {
	switch status {
	case "scheduled":
		return "ready"
	case "in_transit", "delivered":
		return status
	default:
		return "scheduled"
	}
}

func snapshotDeviceStatus(status string) string {
	if status == "registered" {
		return "online"
	}
	return "offline"
}

func snapshotAuditOutcome(status, commandType string) string {
	if commandType == "operator.compensate" && status == "committed" {
		return "compensated"
	}
	if status == "committed" {
		return "committed"
	}
	return "rejected"
}
