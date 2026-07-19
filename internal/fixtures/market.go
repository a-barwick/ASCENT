// Package fixtures provides deterministic scenarios shared by backend, frontend,
// and regression tests.
package fixtures

import (
	"fmt"
	"time"
)

const Seed int64 = 20770524
const ScenarioVersion = "mvp-1"
const ScenarioCompanyCount = 50

var GeneratedAt = time.Date(2077, 5, 24, 14, 38, 4, 182000000, time.UTC)

const (
	NamespacePlayer      = 0x10000000
	NamespaceCompany     = 0x20000000
	NamespaceLocation    = 0x30000000
	NamespaceCommodity   = 0x40000000
	NamespaceHolding     = 0x50000000
	NamespaceAccount     = 0x60000000
	NamespaceJournal     = 0x70000000
	NamespaceEntry       = 0x71000000
	NamespaceMovement    = 0x72000000
	NamespaceMarket      = 0x80000000
	NamespaceOrder       = 0x81000000
	NamespaceReservation = 0x82000000
	NamespaceTrade       = 0x83000000
	NamespaceEvent       = 0x84000000
	NamespaceCommand     = 0x85000000
	NamespaceFacility    = 0x86000000
	NamespaceRecipe      = 0x87000000
	NamespaceJob         = 0x88000000
	NamespaceFreight     = 0x89000000
	NamespaceDevice      = 0x8a000000
	NamespaceView        = 0x8b000000
	NamespaceChat        = 0x8c000000
	NamespaceAlert       = 0x8d000000
	NamespaceOperator    = 0x8e000000
)

func ScenarioID(namespace, index int) string {
	return fmt.Sprintf("%08x-0000-4000-8000-%012d", namespace, index)
}

func SeededPlayerID(index int) string {
	return ScenarioID(NamespacePlayer, index)
}

func SeededCompanyID(index int) string {
	return ScenarioID(NamespaceCompany, index)
}

type MarketSnapshot struct {
	Seed        int64          `json:"seed"`
	GeneratedAt time.Time      `json:"generatedAt"`
	SystemTime  string         `json:"systemTime"`
	Company     Company        `json:"company"`
	Market      Market         `json:"market"`
	OrderBook   OrderBook      `json:"orderBook"`
	Facilities  []Facility     `json:"facilities"`
	Indices     []MarketIndex  `json:"indices"`
	Incidents   []Incident     `json:"incidents"`
	Trace       []TraceNode    `json:"trace"`
	Watchlist   []WatchlistRow `json:"watchlist"`
}

type Company struct {
	ID                string  `json:"id"`
	Name              string  `json:"name"`
	Cash              float64 `json:"cash"`
	TotalAssets       float64 `json:"totalAssets"`
	TotalLiabilities  float64 `json:"totalLiabilities"`
	NetWorth          float64 `json:"netWorth"`
	CreditRating      string  `json:"creditRating"`
	AvailableCredit   float64 `json:"availableCredit"`
	DebtToEquityRatio float64 `json:"debtToEquityRatio"`
}

type Market struct {
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
	Orders   int     `json:"orders"`
}

type Facility struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Type        string  `json:"type"`
	Location    string  `json:"location"`
	Utilization float64 `json:"utilization"`
	Change      float64 `json:"change"`
	Status      string  `json:"status"`
}

type MarketIndex struct {
	Name   string  `json:"name"`
	Value  float64 `json:"value"`
	Change float64 `json:"change"`
}

type Incident struct {
	ID       string `json:"id"`
	Severity string `json:"severity"`
	Summary  string `json:"summary"`
	Time     string `json:"time"`
}

type TraceNode struct {
	Label  string  `json:"label"`
	Value  string  `json:"value"`
	Change float64 `json:"change"`
	Depth  int     `json:"depth"`
}

type WatchlistRow struct {
	Commodity string  `json:"commodity"`
	Location  string  `json:"location"`
	Unit      string  `json:"unit"`
	LastPrice float64 `json:"lastPrice"`
	Change    float64 `json:"change"`
}

func MarketScenario() MarketSnapshot {
	return MarketSnapshot{
		Seed:        Seed,
		GeneratedAt: GeneratedAt,
		SystemTime:  "2077-05-24 14:38:04 UTC",
		Company: Company{
			ID:                "company-helios",
			Name:              "Helios Industries",
			Cash:              12_480_000_000,
			TotalAssets:       47_390_000_000,
			TotalLiabilities:  21_360_000_000,
			NetWorth:          26_030_000_000,
			CreditRating:      "A-",
			AvailableCredit:   8_750_000_000,
			DebtToEquityRatio: 0.82,
		},
		Market: Market{
			ID:           "market-lunar-water",
			Location:     "Lunar south pole",
			Commodity:    "Water ice",
			Unit:         "t",
			Currency:     "CR",
			LastPrice:    308.25,
			Change24Hour: 1.7,
			Volume24Hour: 18_420,
			Spread:       0.95,
			History: []PricePoint{
				{Label: "08:00", Value: 297.40},
				{Label: "09:00", Value: 300.10},
				{Label: "10:00", Value: 299.20},
				{Label: "11:00", Value: 303.80},
				{Label: "12:00", Value: 302.70},
				{Label: "13:00", Value: 306.30},
				{Label: "14:00", Value: 308.25},
			},
		},
		OrderBook: OrderBook{
			Bids: []OrderLevel{
				{Price: 307.80, Quantity: 780.4, Orders: 14},
				{Price: 307.25, Quantity: 598.7, Orders: 9},
				{Price: 306.10, Quantity: 846.5, Orders: 11},
				{Price: 305.40, Quantity: 1_034.8, Orders: 17},
			},
			Asks: []OrderLevel{
				{Price: 308.75, Quantity: 612.0, Orders: 8},
				{Price: 309.20, Quantity: 730.4, Orders: 12},
				{Price: 310.00, Quantity: 980.2, Orders: 16},
				{Price: 311.50, Quantity: 1_205.9, Orders: 19},
			},
		},
		Facilities: []Facility{
			{ID: "facility-lmc-04", Name: "Lunar Materials Complex 04", Type: "Materials", Location: "Moon", Utilization: 67, Change: -2, Status: "operational"},
			{ID: "facility-wir-02", Name: "Water Ice Refinery 02", Type: "Refinery", Location: "Moon", Utilization: 92, Change: 3, Status: "operational"},
			{ID: "facility-pf-03", Name: "Propellant Plant 03", Type: "Fuel plant", Location: "Moon", Utilization: 81, Change: -1, Status: "constrained"},
			{ID: "facility-pg-07", Name: "Power Generation Array 07", Type: "Power", Location: "Moon", Utilization: 86, Change: 4, Status: "operational"},
		},
		Indices: []MarketIndex{
			{Name: "Lunar market", Value: 2142.7, Change: 0.83},
			{Name: "Earth market", Value: 1893.7, Change: 0.56},
			{Name: "LEO freight", Value: 1271.5, Change: -0.23},
			{Name: "Energy", Value: 682.1, Change: 1.13},
		},
		Incidents: []Incident{
			{ID: "incident-route-0042", Severity: "warning", Summary: "Lunar-orbit transfer capacity below 72%", Time: "14:31"},
			{ID: "incident-power-0017", Severity: "info", Summary: "South-pole generation forecast revised +2.4%", Time: "14:22"},
		},
		Trace: []TraceNode{
			{Label: "Company EBITDA", Value: "CR 1.32B", Change: -4.6, Depth: 0},
			{Label: "Propellant margin", Value: "22.8%", Change: -7.1, Depth: 1},
			{Label: "Sale price", Value: "CR 1,285/t", Change: -4.2, Depth: 2},
			{Label: "Unit cost", Value: "CR 992/t", Change: 7.8, Depth: 2},
			{Label: "Freight", Value: "CR 184/t", Change: 4.6, Depth: 3},
			{Label: "Lunar-orbit congestion", Value: "81%", Change: 9.4, Depth: 4},
		},
		Watchlist: []WatchlistRow{
			{Commodity: "Water ice", Location: "Moon", Unit: "t", LastPrice: 308.25, Change: 1.7},
			{Commodity: "Liquid oxygen", Location: "Lunar orbit", Unit: "t", LastPrice: 521.35, Change: 0.8},
			{Commodity: "Structural alloy", Location: "LEO", Unit: "t", LastPrice: 1625.00, Change: -0.4},
			{Commodity: "Power", Location: "Moon", Unit: "MWh", LastPrice: 88.25, Change: 0.9},
		},
	}
}
