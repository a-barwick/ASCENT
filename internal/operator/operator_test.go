package operator

import (
	"testing"

	"ascent/internal/ledger"
)

func TestBuildCompensationReversesButDoesNotMutateOriginal(t *testing.T) {
	original, err := ledger.NewJournal(ledger.JournalDraft{
		ID:          "trade-1",
		OperationID: "operation-1",
		Description: "trade",
		Entries: []ledger.Entry{
			{AccountID: "buyer", Currency: "CR", Amount: -500},
			{AccountID: "seller", Currency: "CR", Amount: 500},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	compensation, err := BuildCompensation(CompensationCommand{
		JournalID:          "trade-1",
		CompensationID:     "compensation-1",
		OperationID:        "operation-2",
		IdempotencyKey:     "retry-1",
		RequestFingerprint: "fingerprint-1",
		Reason:             "incorrect source order",
	}, original)
	if err != nil {
		t.Fatal(err)
	}
	if compensation.CompensatesID() != original.ID() {
		t.Fatalf("compensation reference = %q", compensation.CompensatesID())
	}
	if compensation.Entries()[0].Amount != 500 || compensation.Entries()[1].Amount != -500 {
		t.Fatalf("unexpected compensation: %#v", compensation.Entries())
	}
	if original.Entries()[0].Amount != -500 {
		t.Fatal("operator construction mutated original journal")
	}
}
