// Package ledger provides immutable, balanced journals and balance derivation.
package ledger

import (
	"errors"
	"fmt"
	"math"
	"sort"
)

var (
	ErrInvalidJournal  = errors.New("invalid journal")
	ErrUnbalanced      = errors.New("journal is not balanced")
	ErrDuplicate       = errors.New("duplicate journal")
	ErrUnknownJournal  = errors.New("unknown journal")
	ErrAmountOverflow  = errors.New("ledger amount overflow")
	ErrInvalidCurrency = errors.New("invalid currency")
)

// Entry.Amount is a signed integer in currency minor units. Positive and
// negative entries must sum to zero for each currency in a journal.
type Entry struct {
	AccountID string `json:"accountId"`
	Currency  string `json:"currency"`
	Amount    int64  `json:"amountMinor"`
}

type JournalDraft struct {
	ID                 string
	OperationID        string
	IdempotencyKey     string
	RequestFingerprint string
	Description        string
	CompensatesID      string
	Entries            []Entry
}

// Journal hides its entry slice and exposes copies, preventing callers from
// changing a validated posting after construction.
type Journal struct {
	id                 string
	operationID        string
	idempotencyKey     string
	requestFingerprint string
	description        string
	compensatesID      string
	entries            []Entry
}

func NewJournal(draft JournalDraft) (Journal, error) {
	if draft.ID == "" || draft.OperationID == "" || draft.Description == "" {
		return Journal{}, fmt.Errorf("%w: id, operation id, and description are required", ErrInvalidJournal)
	}
	if len(draft.Entries) < 2 {
		return Journal{}, fmt.Errorf("%w: at least two entries are required", ErrInvalidJournal)
	}

	totals := make(map[string]int64)
	accounts := make(map[string]struct{})
	for index, entry := range draft.Entries {
		if entry.AccountID == "" || entry.Currency == "" || entry.Amount == 0 {
			return Journal{}, fmt.Errorf("%w: entry %d requires account, currency, and nonzero amount", ErrInvalidJournal, index)
		}
		// math.MinInt64 cannot be reversed into a compensating entry.
		if entry.Amount == math.MinInt64 {
			return Journal{}, fmt.Errorf("%w: entry %d", ErrAmountOverflow, index)
		}
		next, ok := checkedAdd(totals[entry.Currency], entry.Amount)
		if !ok {
			return Journal{}, fmt.Errorf("%w: currency %q", ErrAmountOverflow, entry.Currency)
		}
		totals[entry.Currency] = next
		accounts[entry.AccountID] = struct{}{}
	}
	if len(accounts) < 2 {
		return Journal{}, fmt.Errorf("%w: at least two accounts are required", ErrInvalidJournal)
	}
	for currency, total := range totals {
		if total != 0 {
			return Journal{}, fmt.Errorf("%w: currency %q has remainder %d", ErrUnbalanced, currency, total)
		}
	}

	return Journal{
		id:                 draft.ID,
		operationID:        draft.OperationID,
		idempotencyKey:     draft.IdempotencyKey,
		requestFingerprint: draft.RequestFingerprint,
		description:        draft.Description,
		compensatesID:      draft.CompensatesID,
		entries:            append([]Entry(nil), draft.Entries...),
	}, nil
}

func (j Journal) ID() string                 { return j.id }
func (j Journal) OperationID() string        { return j.operationID }
func (j Journal) IdempotencyKey() string     { return j.idempotencyKey }
func (j Journal) RequestFingerprint() string { return j.requestFingerprint }
func (j Journal) Description() string        { return j.description }
func (j Journal) CompensatesID() string      { return j.compensatesID }

func (j Journal) Entries() []Entry {
	return append([]Entry(nil), j.entries...)
}

type CompensationDraft struct {
	ID                 string
	OperationID        string
	IdempotencyKey     string
	RequestFingerprint string
	Description        string
}

func Compensate(original Journal, draft CompensationDraft) (Journal, error) {
	if original.ID() == "" {
		return Journal{}, fmt.Errorf("%w: original journal", ErrInvalidJournal)
	}
	entries := original.Entries()
	for index := range entries {
		if entries[index].Amount == math.MinInt64 {
			return Journal{}, ErrAmountOverflow
		}
		entries[index].Amount = -entries[index].Amount
	}
	return NewJournal(JournalDraft{
		ID:                 draft.ID,
		OperationID:        draft.OperationID,
		IdempotencyKey:     draft.IdempotencyKey,
		RequestFingerprint: draft.RequestFingerprint,
		Description:        draft.Description,
		CompensatesID:      original.ID(),
		Entries:            entries,
	})
}

type Balance struct {
	AccountID string `json:"accountId"`
	Currency  string `json:"currency"`
	Amount    int64  `json:"amountMinor"`
}

type balanceKey struct {
	accountID string
	currency  string
}

// Book is an immutable-in-use collection. Apply returns a new Book and never
// changes the receiver, so failed workflows can discard staged changes.
type Book struct {
	journals map[string]Journal
	order    []string
	balances map[balanceKey]int64
}

func NewBook(initial []Journal) (Book, error) {
	book := Book{
		journals: make(map[string]Journal, len(initial)),
		order:    make([]string, 0, len(initial)),
		balances: make(map[balanceKey]int64),
	}
	var err error
	for _, journal := range initial {
		book, err = book.Apply(journal)
		if err != nil {
			return Book{}, err
		}
	}
	return book, nil
}

func (b Book) Apply(journal Journal) (Book, error) {
	if journal.ID() == "" {
		return Book{}, fmt.Errorf("%w: empty journal", ErrInvalidJournal)
	}
	if _, exists := b.journals[journal.ID()]; exists {
		return Book{}, fmt.Errorf("%w: %q", ErrDuplicate, journal.ID())
	}

	next := b.clone()
	for _, entry := range journal.entries {
		key := balanceKey{accountID: entry.AccountID, currency: entry.Currency}
		amount, ok := checkedAdd(next.balances[key], entry.Amount)
		if !ok {
			return Book{}, fmt.Errorf("%w: account %q currency %q", ErrAmountOverflow, entry.AccountID, entry.Currency)
		}
		next.balances[key] = amount
	}
	next.journals[journal.ID()] = journal
	next.order = append(next.order, journal.ID())
	return next, nil
}

func (b Book) Balance(accountID, currency string) int64 {
	return b.balances[balanceKey{accountID: accountID, currency: currency}]
}

func (b Book) Balances() []Balance {
	result := make([]Balance, 0, len(b.balances))
	for key, amount := range b.balances {
		result = append(result, Balance{
			AccountID: key.accountID,
			Currency:  key.currency,
			Amount:    amount,
		})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].AccountID != result[j].AccountID {
			return result[i].AccountID < result[j].AccountID
		}
		return result[i].Currency < result[j].Currency
	})
	return result
}

func (b Book) Journal(id string) (Journal, bool) {
	journal, ok := b.journals[id]
	return journal, ok
}

func (b Book) Journals() []Journal {
	result := make([]Journal, 0, len(b.order))
	for _, id := range b.order {
		result = append(result, b.journals[id])
	}
	return result
}

func (b Book) clone() Book {
	next := Book{
		journals: make(map[string]Journal, len(b.journals)+1),
		order:    append([]string(nil), b.order...),
		balances: make(map[balanceKey]int64, len(b.balances)),
	}
	for id, journal := range b.journals {
		next.journals[id] = journal
	}
	for key, amount := range b.balances {
		next.balances[key] = amount
	}
	return next
}

func checkedAdd(left, right int64) (int64, bool) {
	if right > 0 && left > math.MaxInt64-right {
		return 0, false
	}
	if right < 0 && left < math.MinInt64-right {
		return 0, false
	}
	return left + right, true
}
