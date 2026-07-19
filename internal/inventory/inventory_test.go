package inventory

import (
	"errors"
	"testing"
)

func TestReservationPreventsOversellAndSettlementConservesProduct(t *testing.T) {
	seller := Position{CompanyID: "seller", LocationID: "moon", ProductID: "water"}
	buyer := Position{CompanyID: "buyer", LocationID: "moon", ProductID: "water"}
	state, err := NewState([]Holding{{Position: seller, Quantity: 100}})
	if err != nil {
		t.Fatal(err)
	}
	before, err := state.Total("water")
	if err != nil {
		t.Fatal(err)
	}

	state, err = state.Reserve(Reservation{
		ID:          "order-1",
		OperationID: "operation-1",
		Purpose:     "sell order",
		Position:    seller,
		Quantity:    80,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.Reserve(Reservation{
		ID:          "order-2",
		OperationID: "operation-2",
		Purpose:     "sell order",
		Position:    seller,
		Quantity:    21,
	}); !errors.Is(err, ErrInsufficientQuantity) {
		t.Fatalf("expected oversell rejection, got %v", err)
	}

	state, err = state.SettleReservation("order-1", Movement{
		ID:          "trade-1",
		OperationID: "operation-3",
		From:        seller,
		To:          buyer,
		Quantity:    35,
	})
	if err != nil {
		t.Fatal(err)
	}
	after, err := state.Total("water")
	if err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatalf("movement changed total product: before %d after %d", before, after)
	}
	if state.Quantity(seller) != 65 || state.Quantity(buyer) != 35 {
		t.Fatalf("unexpected holdings: %#v", state.Holdings())
	}
	if got := state.Reservations()[0].Quantity; got != 45 {
		t.Fatalf("remaining reservation = %d, want 45", got)
	}
}

func TestFailedTransferDoesNotChangeState(t *testing.T) {
	origin := Position{CompanyID: "company", LocationID: "moon", ProductID: "water"}
	destination := Position{CompanyID: "company", LocationID: "orbit", ProductID: "water"}
	state, err := NewState([]Holding{{Position: origin, Quantity: 10}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.Transfer(Movement{
		ID:          "freight-1",
		OperationID: "operation-1",
		From:        origin,
		To:          destination,
		Quantity:    11,
	}); !errors.Is(err, ErrInsufficientQuantity) {
		t.Fatalf("expected insufficient inventory, got %v", err)
	}
	if state.Quantity(origin) != 10 || state.Quantity(destination) != 0 || len(state.Movements()) != 0 {
		t.Fatal("failed movement changed inventory")
	}
}
