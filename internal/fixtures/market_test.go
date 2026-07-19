package fixtures

import (
	"encoding/json"
	"testing"
)

func TestMarketScenarioIsDeterministic(t *testing.T) {
	t.Parallel()

	first, err := json.Marshal(MarketScenario())
	if err != nil {
		t.Fatal(err)
	}
	second, err := json.Marshal(MarketScenario())
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatal("market fixture changed between calls")
	}
}

func TestMarketScenarioHasTwoSidedBook(t *testing.T) {
	t.Parallel()

	scenario := MarketScenario()
	if len(scenario.OrderBook.Bids) == 0 || len(scenario.OrderBook.Asks) == 0 {
		t.Fatal("expected bids and asks")
	}
	bestBid := scenario.OrderBook.Bids[0].Price
	bestAsk := scenario.OrderBook.Asks[0].Price
	if bestBid >= bestAsk {
		t.Fatalf("crossed seed book: best bid %.2f >= best ask %.2f", bestBid, bestAsk)
	}
}
