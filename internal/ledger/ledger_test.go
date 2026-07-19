package ledger

import (
	"errors"
	"testing"
)

func TestJournalMustBalanceAndBookDerivesBalances(t *testing.T) {
	if _, err := NewJournal(JournalDraft{
		ID:          "bad",
		OperationID: "operation-bad",
		Description: "unbalanced",
		Entries: []Entry{
			{AccountID: "cash-a", Currency: "CR", Amount: -101},
			{AccountID: "cash-b", Currency: "CR", Amount: 100},
		},
	}); !errors.Is(err, ErrUnbalanced) {
		t.Fatalf("expected unbalanced error, got %v", err)
	}

	journal := mustJournal(t, JournalDraft{
		ID:          "trade-1",
		OperationID: "operation-1",
		Description: "trade settlement",
		Entries: []Entry{
			{AccountID: "cash-a", Currency: "CR", Amount: -100},
			{AccountID: "cash-b", Currency: "CR", Amount: 100},
		},
	})
	book, err := NewBook(nil)
	if err != nil {
		t.Fatal(err)
	}
	book, err = book.Apply(journal)
	if err != nil {
		t.Fatal(err)
	}
	if got := book.Balance("cash-a", "CR"); got != -100 {
		t.Fatalf("buyer balance = %d, want -100", got)
	}
	if got := book.Balance("cash-b", "CR"); got != 100 {
		t.Fatalf("seller balance = %d, want 100", got)
	}
}

func TestJournalEntriesAreImmutableAndCompensationReversesThem(t *testing.T) {
	original := mustJournal(t, JournalDraft{
		ID:          "trade-1",
		OperationID: "operation-1",
		Description: "trade settlement",
		Entries: []Entry{
			{AccountID: "cash-a", Currency: "CR", Amount: -250},
			{AccountID: "cash-b", Currency: "CR", Amount: 250},
		},
	})

	returned := original.Entries()
	returned[0].Amount = 9_999
	if original.Entries()[0].Amount != -250 {
		t.Fatal("mutating returned entries changed the journal")
	}

	compensation, err := Compensate(original, CompensationDraft{
		ID:          "compensation-1",
		OperationID: "operation-2",
		Description: "reverse incorrect trade",
	})
	if err != nil {
		t.Fatal(err)
	}
	if compensation.CompensatesID() != original.ID() {
		t.Fatalf("compensates %q, want %q", compensation.CompensatesID(), original.ID())
	}
	if compensation.Entries()[0].Amount != 250 || compensation.Entries()[1].Amount != -250 {
		t.Fatalf("unexpected compensation entries: %#v", compensation.Entries())
	}

	book, err := NewBook([]Journal{original, compensation})
	if err != nil {
		t.Fatal(err)
	}
	if book.Balance("cash-a", "CR") != 0 || book.Balance("cash-b", "CR") != 0 {
		t.Fatalf("compensation did not restore balances: %#v", book.Balances())
	}
}

func mustJournal(t *testing.T, draft JournalDraft) Journal {
	t.Helper()
	journal, err := NewJournal(draft)
	if err != nil {
		t.Fatal(err)
	}
	return journal
}
