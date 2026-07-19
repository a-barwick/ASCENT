package gamepostgres

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"ascent/internal/ledger"
	"ascent/internal/platform/ids"
)

func postJournal(
	ctx context.Context,
	transaction *sql.Tx,
	commandID, companyID, currency, sourceType, sourceID, description string,
	reversalOf *string,
	entries []ledger.Entry,
	now time.Time,
) (string, error) {
	journalID, err := ids.NewUUID()
	if err != nil {
		return "", err
	}
	return postJournalWithID(
		ctx,
		transaction,
		journalID,
		commandID,
		companyID,
		currency,
		sourceType,
		sourceID,
		description,
		reversalOf,
		entries,
		now,
	)
}

func postJournalWithID(
	ctx context.Context,
	transaction *sql.Tx,
	journalID, commandID, companyID, currency, sourceType, sourceID, description string,
	reversalOf *string,
	entries []ledger.Entry,
	now time.Time,
) (string, error) {
	journal, err := ledger.NewJournal(ledger.JournalDraft{
		ID:          journalID,
		OperationID: commandID,
		Description: description,
		Entries:     entries,
	})
	if err != nil {
		return "", fmt.Errorf("validate %s journal: %w", sourceType, err)
	}
	if _, err := transaction.ExecContext(
		ctx,
		`INSERT INTO ledger.journals (
			journal_id, company_id, currency, command_id, source_type,
			source_id, description, occurred_at, posted_at,
			reversal_of_journal_id
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $8, $9)`,
		journalID,
		companyID,
		currency,
		commandID,
		sourceType,
		sourceID,
		journal.Description(),
		now,
		reversalOf,
	); err != nil {
		return "", fmt.Errorf("insert %s journal: %w", sourceType, err)
	}
	for _, entry := range journal.Entries() {
		entryID, err := ids.NewUUID()
		if err != nil {
			return "", err
		}
		side := "debit"
		amount := entry.Amount
		if amount < 0 {
			side = "credit"
			amount = -amount
		}
		if _, err := transaction.ExecContext(
			ctx,
			`INSERT INTO ledger.entries (
				entry_id, journal_id, account_id, company_id,
				currency, side, amount_minor, memo
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
			entryID,
			journalID,
			entry.AccountID,
			companyID,
			entry.Currency,
			side,
			amount,
			description,
		); err != nil {
			return "", fmt.Errorf("insert %s journal entry: %w", sourceType, err)
		}
	}
	return journalID, nil
}

func loadNamedAccounts(
	ctx context.Context,
	transaction *sql.Tx,
	companyID string,
	codes ...string,
) (map[string]string, error) {
	rows, err := transaction.QueryContext(
		ctx,
		`SELECT code, account_id
		   FROM ledger.accounts
		  WHERE company_id = $1
		    AND status = 'active'`,
		companyID,
	)
	if err != nil {
		return nil, fmt.Errorf("load named accounts: %w", err)
	}
	defer rows.Close()
	accounts := make(map[string]string, len(codes))
	for rows.Next() {
		var code, accountID string
		if err := rows.Scan(&code, &accountID); err != nil {
			return nil, err
		}
		accounts[code] = accountID
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, code := range codes {
		if accounts[code] == "" {
			return nil, fmt.Errorf("company %s is missing account %s", companyID, code)
		}
	}
	return accounts, nil
}
