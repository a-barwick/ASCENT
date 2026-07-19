package markets

import (
	"errors"
	"testing"
)

func TestPriceTimePriorityAndPartialFill(t *testing.T) {
	book := testBook(t)
	var err error
	book, _, err = book.Submit(OrderRequest{
		ID: "ask-first", OperationID: "operation-1", CompanyID: "seller-a",
		Side: SideSell, Price: 100, Quantity: 5, Sequence: 1,
	}, 5)
	if err != nil {
		t.Fatal(err)
	}
	book, _, err = book.Submit(OrderRequest{
		ID: "ask-second", OperationID: "operation-2", CompanyID: "seller-b",
		Side: SideSell, Price: 100, Quantity: 5, Sequence: 2,
	}, 5)
	if err != nil {
		t.Fatal(err)
	}
	book, execution, err := book.Submit(OrderRequest{
		ID: "bid", OperationID: "operation-3", CompanyID: "buyer",
		Side: SideBuy, Price: 105, Quantity: 7, Sequence: 3,
	}, 735)
	if err != nil {
		t.Fatal(err)
	}

	if len(execution.Trades) != 2 {
		t.Fatalf("trade count = %d, want 2", len(execution.Trades))
	}
	if execution.Trades[0].SellOrderID != "ask-first" || execution.Trades[0].Quantity != 5 {
		t.Fatalf("first trade ignored time priority: %#v", execution.Trades[0])
	}
	if execution.Trades[1].SellOrderID != "ask-second" || execution.Trades[1].Quantity != 2 {
		t.Fatalf("second trade did not partially fill: %#v", execution.Trades[1])
	}
	if execution.Trades[0].Price != 100 || execution.Trades[1].Price != 100 {
		t.Fatalf("maker price not used: %#v", execution.Trades)
	}
	second, _ := book.Order("ask-second")
	if second.Status != OrderPartiallyFilled || second.RemainingQuantity != 3 {
		t.Fatalf("unexpected resting order: %#v", second)
	}
	bid, _ := book.Order("bid")
	if bid.Status != OrderFilled || bid.ReservedRemaining != 0 {
		t.Fatalf("unexpected aggressive order: %#v", bid)
	}

	var released int64
	for _, effect := range execution.ReservationEffects {
		if effect.OrderID == "bid" {
			released += effect.Released
		}
	}
	if released != 35 {
		t.Fatalf("price-improvement release = %d, want 35", released)
	}
}

func TestSubmitRejectsOverspendAndCancelReleasesReservation(t *testing.T) {
	book := testBook(t)
	if _, _, err := book.Submit(OrderRequest{
		ID: "too-large", OperationID: "operation-1", CompanyID: "buyer",
		Side: SideBuy, Price: 11, Quantity: 10, Sequence: 1,
	}, 109); !errors.Is(err, ErrInsufficientCapacity) {
		t.Fatalf("expected overspend rejection, got %v", err)
	}
	if len(book.Orders()) != 0 {
		t.Fatal("failed submission changed book")
	}

	book, _, err := book.Submit(OrderRequest{
		ID: "bid", OperationID: "operation-2", CompanyID: "buyer",
		Side: SideBuy, Price: 11, Quantity: 10, Sequence: 2,
	}, 110)
	if err != nil {
		t.Fatal(err)
	}
	book, canceled, err := book.Cancel("buyer", "bid")
	if err != nil {
		t.Fatal(err)
	}
	if canceled.ReservationEffect.Released != 110 || canceled.ReservationEffect.Asset != ReservationQuote {
		t.Fatalf("unexpected release: %#v", canceled.ReservationEffect)
	}
	if reserved, err := book.ReservedQuote("buyer"); err != nil || reserved != 0 {
		t.Fatalf("reserved quote = %d, err = %v", reserved, err)
	}
}

func testBook(t *testing.T) Book {
	t.Helper()
	book, err := NewBook(Market{
		ID:            "market-water",
		ProductID:     "water",
		Currency:      "CR",
		LocationID:    "moon",
		QuantityScale: 1,
		LotSize:       1,
	})
	if err != nil {
		t.Fatal(err)
	}
	return book
}
