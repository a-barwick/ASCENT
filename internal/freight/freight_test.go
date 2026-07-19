package freight

import (
	"errors"
	"testing"
)

func TestDeliveryIsIdempotentAndChangedRetryConflicts(t *testing.T) {
	state, err := NewState([]Contract{{
		ID:                    "contract-1",
		OwnerCompanyID:        "shipper",
		CarrierCompanyID:      "carrier",
		ProductID:             "water",
		Quantity:              20,
		OriginLocationID:      "moon",
		DestinationLocationID: "orbit",
		DueTick:               10,
		Status:                ContractInTransit,
	}})
	if err != nil {
		t.Fatal(err)
	}
	command := DeliverCommand{
		ContractID:         "contract-1",
		CarrierCompanyID:   "carrier",
		AtTick:             10,
		DeliveryID:         "delivery-1",
		OperationID:        "operation-1",
		IdempotencyKey:     "retry-1",
		RequestFingerprint: "fingerprint-1",
	}
	next, delivery, err := state.Deliver(command)
	if err != nil {
		t.Fatal(err)
	}
	if delivery.Movement.Quantity != 20 {
		t.Fatalf("delivery quantity = %d, want 20", delivery.Movement.Quantity)
	}

	retried, replay, err := next.Deliver(command)
	if err != nil {
		t.Fatal(err)
	}
	if !replay.Replay || len(retried.Deliveries()) != 1 {
		t.Fatalf("duplicate delivery was not replayed: %#v", replay)
	}

	changed := command
	changed.RequestFingerprint = "changed"
	if _, _, err := next.Deliver(changed); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("expected idempotency conflict, got %v", err)
	}
}
