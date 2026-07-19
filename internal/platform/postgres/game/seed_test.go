package gamepostgres

import (
	"testing"

	"ascent/internal/fixtures"
)

func TestSeedConstantsUseFixtureUUIDNamespaces(t *testing.T) {
	t.Parallel()

	if SeededActorID != fixtures.SeededPlayerID(1) {
		t.Fatalf("seeded actor = %q", SeededActorID)
	}
	if SeededCompanyID != fixtures.SeededCompanyID(1) {
		t.Fatalf("seeded company = %q", SeededCompanyID)
	}
	if marketID(seedWaterMarket) != fixtures.ScenarioID(fixtures.NamespaceMarket, 1) {
		t.Fatalf("water market ID = %q", marketID(seedWaterMarket))
	}
	if accountID(1, 1) == accountID(2, 1) || holdingID(1, 1) == holdingID(1, 2) {
		t.Fatal("stable seed ID helpers collided")
	}
}

func TestSeedWaterBookIsNoncrossedReservedAndLotAligned(t *testing.T) {
	t.Parallel()

	var bestBid, bestAsk int64
	for _, order := range waterBook {
		if order.quantityMin%seedLotSizeMinor != 0 {
			t.Fatalf("order %d quantity %d is not lot aligned", order.index, order.quantityMin)
		}
		if order.side == "buy" {
			if order.priceMinor > bestBid {
				bestBid = order.priceMinor
			}
			reservation := cashReservationMinor(order.priceMinor, order.quantityMin, seedQuantityScale)
			if reservation != order.priceMinor*order.quantityMin/1_000 {
				t.Fatalf("order %d quote reservation = %d", order.index, reservation)
			}
		} else if bestAsk == 0 || order.priceMinor < bestAsk {
			bestAsk = order.priceMinor
		}
	}
	if bestBid == 0 || bestAsk == 0 || bestBid >= bestAsk {
		t.Fatalf("seeded water book crosses: bid %d ask %d", bestBid, bestAsk)
	}
}

func TestSeedHasTwoAuthoritativeMarketHistories(t *testing.T) {
	t.Parallel()

	if len(seedMarketHistory) < 2 {
		t.Fatalf("history market count = %d", len(seedMarketHistory))
	}
	for marketIndex, history := range seedMarketHistory {
		if len(history) < 2 {
			t.Fatalf("market %d history points = %d", marketIndex, len(history))
		}
		for _, point := range history {
			if point.priceMinor <= 0 || point.volumeQuantity <= 0 || point.bestBidMinor >= point.bestAskMinor {
				t.Fatalf("market %d invalid price point %#v", marketIndex, point)
			}
		}
	}
}

func TestOpeningFinancialsAndHoldingCostsAreBalanced(t *testing.T) {
	t.Parallel()

	for companyIndex := 1; companyIndex <= fixtures.ScenarioCompanyCount; companyIndex++ {
		cash, inventoryValue, liabilities := openingFinancials(companyIndex)
		equity := cash + inventoryValue - liabilities
		if cash <= 0 || inventoryValue <= 0 || liabilities <= 0 || equity <= 0 {
			t.Fatalf("company %d invalid opening position", companyIndex)
		}
		currentHoldingCost := holdingCostBasis(companyIndex, seedWaterCommodity) + holdingCostBasis(companyIndex, seedLOXCommodity)
		want := inventoryValue
		if companyIndex == 1 {
			want += 936_720
		}
		if companyIndex == 2 {
			want -= 936_720
		}
		if currentHoldingCost != want {
			t.Fatalf("company %d holding cost = %d, want %d", companyIndex, currentHoldingCost, want)
		}
	}
}
