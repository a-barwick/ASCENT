package gamepostgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"time"

	"ascent/internal/chat"
	"ascent/internal/identity"
	"ascent/internal/ledger"
	domainoperator "ascent/internal/operator"
	"ascent/internal/platform/ids"
	protocol "ascent/protocol/gen/go"
)

type compensatePayload struct {
	TargetEventID string `json:"targetEventId"`
	Reason        string `json:"reason"`
}

type originalJournal struct {
	id              string
	companyID       string
	currency        string
	description     string
	entries         []ledger.Entry
	inventoryAmount int64
}

type originalMovement struct {
	id       string
	fromID   sql.NullString
	toID     sql.NullString
	quantity int64
}

type holdingCompensation struct {
	id        string
	companyID string
	quantity  int64
	available int64
	costBasis int64
	delta     int64
	cost      int64
}

func (s *Service) compensate(
	ctx context.Context,
	transaction *sql.Tx,
	actor identity.Actor,
	command protocol.CommandEnvelope,
) (commandOutcome, *commandRejection, error) {
	_, companyVersion, rejection, err := s.authorizeCompany(
		ctx,
		transaction,
		actor,
		command,
		"owner",
		"operator",
	)
	if rejection != nil || err != nil {
		return commandOutcome{}, rejection, err
	}
	var payload compensatePayload
	if rejection := decodePayload(command.Payload, &payload); rejection != nil {
		return commandOutcome{}, rejection, nil
	}
	reason, err := chat.NormalizeMessage(payload.Reason)
	if err != nil || !ids.IsUUID(payload.TargetEventID) {
		return commandOutcome{}, reject(
			"INVALID_COMPENSATION",
			"Choose a valid committed event and enter a plain-text reason.",
		), nil
	}
	if rejection, err := requireOperatorGrant(ctx, transaction, actor.ID); rejection != nil || err != nil {
		return commandOutcome{}, rejection, err
	}
	originalCommandID, rejection, err := resolveOriginalCommand(
		ctx,
		transaction,
		payload.TargetEventID,
	)
	if rejection != nil || err != nil {
		return commandOutcome{}, rejection, err
	}
	if originalCommandID == command.CommandId {
		return commandOutcome{}, reject(
			"INVALID_COMPENSATION",
			"A correction cannot compensate itself.",
		), nil
	}
	var alreadyCompensated bool
	if err := transaction.QueryRowContext(
		ctx,
		`SELECT EXISTS (
			SELECT 1
			  FROM operator_admin.compensations
			 WHERE original_command_id = $1
			   AND status = 'committed'
		)`,
		originalCommandID,
	).Scan(&alreadyCompensated); err != nil {
		return commandOutcome{}, nil, fmt.Errorf("check existing compensation: %w", err)
	}
	if alreadyCompensated {
		return commandOutcome{}, reject(
			"ALREADY_COMPENSATED",
			"That command already has a committed compensation.",
		), nil
	}
	journals, err := loadOriginalJournals(ctx, transaction, originalCommandID)
	if err != nil {
		return commandOutcome{}, nil, err
	}
	movements, err := loadOriginalMovements(ctx, transaction, originalCommandID)
	if err != nil {
		return commandOutcome{}, nil, err
	}
	if len(journals) == 0 && len(movements) == 0 {
		return commandOutcome{}, reject(
			"NOT_COMPENSABLE",
			"That event has no journal or inventory effect to compensate.",
		), nil
	}

	compensationID, err := ids.NewUUID()
	if err != nil {
		return commandOutcome{}, nil, err
	}
	now := s.clock()
	if _, err := transaction.ExecContext(
		ctx,
		`INSERT INTO operator_admin.compensations (
			compensation_id, command_id, original_command_id,
			operator_player_id, reason, status, requested_at
		) VALUES ($1, $2, $3, $4, $5, 'pending', $6)`,
		compensationID,
		command.CommandId,
		originalCommandID,
		actor.ID,
		reason,
		now,
	); err != nil {
		return commandOutcome{}, nil, fmt.Errorf("create operator compensation: %w", err)
	}
	if _, err := transaction.ExecContext(
		ctx,
		`INSERT INTO platform.command_relations (
			command_id, related_command_id, relation_type
		) VALUES ($1, $2, 'compensates')`,
		command.CommandId,
		originalCommandID,
	); err != nil {
		return commandOutcome{}, nil, fmt.Errorf("relate compensating command: %w", err)
	}

	affectedCompanies := make(map[string]struct{})
	inventoryLedgerDelta := make(map[string]int64)
	journalIDs := make([]string, 0, len(journals))
	for _, original := range journals {
		originalDomain, err := ledger.NewJournal(ledger.JournalDraft{
			ID:          original.id,
			OperationID: originalCommandID,
			Description: original.description,
			Entries:     original.entries,
		})
		if err != nil {
			return commandOutcome{}, nil, fmt.Errorf("validate original journal %s: %w", original.id, err)
		}
		compensatingJournalID, err := ids.NewUUID()
		if err != nil {
			return commandOutcome{}, nil, err
		}
		compensatingDomain, err := domainoperator.BuildCompensation(
			domainoperator.CompensationCommand{
				JournalID:          original.id,
				CompensationID:     compensatingJournalID,
				OperationID:        command.CommandId,
				IdempotencyKey:     command.IdempotencyKey,
				RequestFingerprint: string(command.Payload),
				Reason:             truncateRunes(reason, 260),
			},
			originalDomain,
		)
		if err != nil {
			return commandOutcome{}, nil, fmt.Errorf("build journal compensation: %w", err)
		}
		journalID, err := postJournalWithID(
			ctx,
			transaction,
			compensatingJournalID,
			command.CommandId,
			original.companyID,
			original.currency,
			"correction",
			compensationID,
			compensatingDomain.Description(),
			&original.id,
			compensatingDomain.Entries(),
			now,
		)
		if err != nil {
			return commandOutcome{}, nil, err
		}
		if _, err := transaction.ExecContext(
			ctx,
			`INSERT INTO operator_admin.compensation_journals (
				compensation_id, journal_id
			) VALUES ($1, $2)`,
			compensationID,
			journalID,
		); err != nil {
			return commandOutcome{}, nil, fmt.Errorf("link compensation journal: %w", err)
		}
		journalIDs = append(journalIDs, journalID)
		affectedCompanies[original.companyID] = struct{}{}
		inventoryLedgerDelta[original.companyID] -= original.inventoryAmount
	}
	movementIDs, movementCompanies, rejection, err := reverseInventoryMovements(
		ctx,
		transaction,
		command.CommandId,
		compensationID,
		reason,
		movements,
		inventoryLedgerDelta,
		now,
	)
	if rejection != nil || err != nil {
		return commandOutcome{}, rejection, err
	}
	for companyID := range movementCompanies {
		affectedCompanies[companyID] = struct{}{}
	}
	if len(journalIDs)+len(movementIDs) == 0 {
		return commandOutcome{}, nil, errors.New("compensation produced no durable effect")
	}
	event, err := appendEvent(
		ctx,
		transaction,
		&command.CommandId,
		"company:"+*command.CompanyId,
		"OPERATOR_COMPENSATION_COMMITTED",
		map[string]any{
			"compensationId":    compensationID,
			"originalCommandId": originalCommandID,
			"targetEventId":     payload.TargetEventID,
			"reason":            reason,
			"journalIds":        journalIDs,
			"movementIds":       movementIDs,
		},
		now,
	)
	if err != nil {
		return commandOutcome{}, nil, err
	}
	if _, err := transaction.ExecContext(
		ctx,
		`UPDATE operator_admin.compensations
		    SET status = 'committed',
		        committed_at = $2,
		        event_id = $3
		  WHERE compensation_id = $1 AND status = 'pending'`,
		compensationID,
		now,
		*event.EventId,
	); err != nil {
		return commandOutcome{}, nil, fmt.Errorf("commit operator compensation: %w", err)
	}
	if err := advanceCompensatedCompanies(
		ctx,
		transaction,
		*command.CompanyId,
		companyVersion,
		affectedCompanies,
	); err != nil {
		return commandOutcome{}, nil, err
	}
	return commandOutcome{payload: map[string]any{
		"compensationId": compensationID,
		"eventSequence":  event.Sequence,
		"journalIds":     journalIDs,
		"movementIds":    movementIDs,
	}}, nil, nil
}

func requireOperatorGrant(
	ctx context.Context,
	transaction *sql.Tx,
	playerID string,
) (*commandRejection, error) {
	var role string
	err := transaction.QueryRowContext(
		ctx,
		`SELECT role
		   FROM operator_admin.grants
		  WHERE player_id = $1
		    AND revoked_at IS NULL
		    AND role IN ('operator', 'administrator')
		  ORDER BY CASE role WHEN 'administrator' THEN 0 ELSE 1 END
		  LIMIT 1
		  FOR SHARE`,
		playerID,
	).Scan(&role)
	if errors.Is(err, sql.ErrNoRows) {
		return reject(
			"OPERATOR_GRANT_REQUIRED",
			"An active elevated operator grant is required.",
		), nil
	}
	if err != nil {
		return nil, fmt.Errorf("resolve operator grant: %w", err)
	}
	return nil, nil
}

func resolveOriginalCommand(
	ctx context.Context,
	transaction *sql.Tx,
	targetID string,
) (string, *commandRejection, error) {
	var originalCommandID sql.NullString
	err := transaction.QueryRowContext(
		ctx,
		`SELECT command_id::text
		   FROM platform.event_outbox
		  WHERE event_id = $1
		  FOR SHARE`,
		targetID,
	).Scan(&originalCommandID)
	if errors.Is(err, sql.ErrNoRows) {
		err = transaction.QueryRowContext(
			ctx,
			`SELECT command_id::text
			   FROM platform.command_log
			  WHERE command_id = $1
			    AND status = 'committed'
			  FOR SHARE`,
			targetID,
		).Scan(&originalCommandID)
	}
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !originalCommandID.Valid) {
		return "", reject(
			"TARGET_NOT_FOUND",
			"That event is not linked to a compensable committed command.",
		), nil
	}
	if err != nil {
		return "", nil, fmt.Errorf("resolve compensation target: %w", err)
	}
	var status string
	if err := transaction.QueryRowContext(
		ctx,
		`SELECT status
		   FROM platform.command_log
		  WHERE command_id = $1
		  FOR SHARE`,
		originalCommandID.String,
	).Scan(&status); errors.Is(err, sql.ErrNoRows) {
		return "", reject("TARGET_NOT_FOUND", "The target command does not exist."), nil
	} else if err != nil {
		return "", nil, fmt.Errorf("load target command: %w", err)
	}
	if status != "committed" {
		return "", reject(
			"TARGET_NOT_COMMITTED",
			"Only a committed command can be compensated.",
		), nil
	}
	return originalCommandID.String, nil, nil
}

func loadOriginalJournals(
	ctx context.Context,
	transaction *sql.Tx,
	commandID string,
) ([]originalJournal, error) {
	rows, err := transaction.QueryContext(
		ctx,
		`SELECT journal.journal_id,
		        journal.company_id,
		        journal.currency,
		        journal.description,
		        entry.account_id,
		        account.code,
		        CASE entry.side
		          WHEN 'debit' THEN entry.amount_minor
		          ELSE -entry.amount_minor
		        END AS signed_amount
		   FROM ledger.journals AS journal
		   JOIN ledger.entries AS entry
		     ON entry.journal_id = journal.journal_id
		   JOIN ledger.accounts AS account
		     ON account.account_id = entry.account_id
		  WHERE journal.command_id = $1
		  ORDER BY journal.company_id, journal.journal_id, entry.entry_id`,
		commandID,
	)
	if err != nil {
		return nil, fmt.Errorf("load original journals: %w", err)
	}
	defer rows.Close()
	var (
		result  []originalJournal
		current *originalJournal
	)
	for rows.Next() {
		var (
			journalID, companyID, currency, description string
			accountCode                                 string
			entry                                       ledger.Entry
		)
		if err := rows.Scan(
			&journalID,
			&companyID,
			&currency,
			&description,
			&entry.AccountID,
			&accountCode,
			&entry.Amount,
		); err != nil {
			return nil, err
		}
		entry.Currency = currency
		if current == nil || current.id != journalID {
			result = append(result, originalJournal{
				id:          journalID,
				companyID:   companyID,
				currency:    currency,
				description: description,
			})
			current = &result[len(result)-1]
		}
		current.entries = append(current.entries, entry)
		if accountCode == "INVENTORY" {
			current.inventoryAmount += entry.Amount
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func loadOriginalMovements(
	ctx context.Context,
	transaction *sql.Tx,
	commandID string,
) ([]originalMovement, error) {
	rows, err := transaction.QueryContext(
		ctx,
		`SELECT movement_id, from_holding_id, to_holding_id, quantity_minor
		   FROM inventory.movements
		  WHERE command_id = $1
		  ORDER BY movement_id`,
		commandID,
	)
	if err != nil {
		return nil, fmt.Errorf("load original inventory movements: %w", err)
	}
	defer rows.Close()
	var movements []originalMovement
	for rows.Next() {
		var movement originalMovement
		if err := rows.Scan(
			&movement.id,
			&movement.fromID,
			&movement.toID,
			&movement.quantity,
		); err != nil {
			return nil, err
		}
		movements = append(movements, movement)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return movements, nil
}

func reverseInventoryMovements(
	ctx context.Context,
	transaction *sql.Tx,
	commandID, compensationID, reason string,
	movements []originalMovement,
	inventoryLedgerDelta map[string]int64,
	now time.Time,
) ([]string, map[string]struct{}, *commandRejection, error) {
	deltas := make(map[string]int64)
	for _, movement := range movements {
		if movement.fromID.Valid {
			deltas[movement.fromID.String] += movement.quantity
		}
		if movement.toID.Valid {
			deltas[movement.toID.String] -= movement.quantity
		}
	}
	holdingIDs := make([]string, 0, len(deltas))
	for holdingID, delta := range deltas {
		if delta != 0 {
			holdingIDs = append(holdingIDs, holdingID)
		}
	}
	sort.Strings(holdingIDs)
	holdings := make([]holdingCompensation, 0, len(holdingIDs))
	removedQuantityByCompany := make(map[string]int64)
	addedQuantityByCompany := make(map[string]int64)
	for _, holdingID := range holdingIDs {
		var holding holdingCompensation
		holding.id = holdingID
		holding.delta = deltas[holdingID]
		if err := transaction.QueryRowContext(
			ctx,
			`SELECT company_id, quantity_minor, available_quantity_minor, cost_basis_minor
			   FROM inventory.holdings
			  WHERE holding_id = $1
			  FOR UPDATE`,
			holdingID,
		).Scan(
			&holding.companyID,
			&holding.quantity,
			&holding.available,
			&holding.costBasis,
		); err != nil {
			return nil, nil, nil, fmt.Errorf("lock compensation holding: %w", err)
		}
		if holding.delta < 0 {
			remove := -holding.delta
			if holding.available < remove {
				return nil, nil, reject(
					"COMPENSATION_BLOCKED",
					"Later inventory activity prevents a safe compensating movement.",
				), nil
			}
			cost, ok := proportionalCost(holding.costBasis, remove, holding.quantity)
			if !ok {
				return nil, nil, nil, errors.New("compensation cost basis overflow")
			}
			holding.cost = cost
			removedQuantityByCompany[holding.companyID] += remove
		} else {
			addedQuantityByCompany[holding.companyID] += holding.delta
		}
		holdings = append(holdings, holding)
	}

	for companyID, removedQuantity := range removedQuantityByCompany {
		if addedQuantityByCompany[companyID] != 0 || inventoryLedgerDelta[companyID] >= 0 {
			continue
		}
		remainingCost := -inventoryLedgerDelta[companyID]
		remainingQuantity := removedQuantity
		for index := range holdings {
			if holdings[index].companyID != companyID || holdings[index].delta >= 0 {
				continue
			}
			remove := -holdings[index].delta
			if remove == remainingQuantity {
				holdings[index].cost = remainingCost
			} else {
				allocation, ok := multiplyDivide(remainingCost, remove, remainingQuantity)
				if !ok {
					return nil, nil, nil, errors.New("compensation removal cost allocation overflow")
				}
				holdings[index].cost = allocation
			}
			if holdings[index].cost > holdings[index].costBasis {
				return nil, nil, reject(
					"COMPENSATION_BLOCKED",
					"Current inventory value no longer supports an exact compensation.",
				), nil
			}
			remainingCost -= holdings[index].cost
			remainingQuantity -= remove
		}
	}

	removedCostByCompany := make(map[string]int64)
	for _, holding := range holdings {
		if holding.delta < 0 {
			current := removedCostByCompany[holding.companyID]
			if holding.cost > 0 && current > mathMaxInt64-holding.cost {
				return nil, nil, nil, errors.New("compensation cost pool overflow")
			}
			removedCostByCompany[holding.companyID] = current + holding.cost
		}
		if holding.delta > 0 {
			current := addedQuantityByCompany[holding.companyID]
			if current > mathMaxInt64-holding.delta {
				return nil, nil, nil, errors.New("compensation quantity pool overflow")
			}
			addedQuantityByCompany[holding.companyID] = current + holding.delta
		}
	}

	companySet := make(map[string]struct{})
	for _, holding := range holdings {
		companySet[holding.companyID] = struct{}{}
	}
	for companyID := range companySet {
		removedCost := removedCostByCompany[companyID]
		ledgerDelta := inventoryLedgerDelta[companyID]
		if ledgerDelta > 0 && removedCost > mathMaxInt64-ledgerDelta {
			return nil, nil, nil, errors.New("compensation inventory value overflow")
		}
		addedCost := removedCost + ledgerDelta
		if addedCost < 0 {
			return nil, nil, reject(
				"COMPENSATION_BLOCKED",
				"Current inventory value no longer supports an exact compensation.",
			), nil
		}
		addedQuantity := addedQuantityByCompany[companyID]
		if addedQuantity == 0 && addedCost != 0 {
			return nil, nil, reject(
				"COMPENSATION_BLOCKED",
				"Current inventory lots no longer match the original journal.",
			), nil
		}
		remainingCost := addedCost
		remainingQuantity := addedQuantity
		for index := range holdings {
			if holdings[index].companyID != companyID || holdings[index].delta <= 0 {
				continue
			}
			if holdings[index].delta == remainingQuantity {
				holdings[index].cost = remainingCost
			} else {
				allocation, ok := multiplyDivide(
					remainingCost,
					holdings[index].delta,
					remainingQuantity,
				)
				if !ok {
					return nil, nil, nil, errors.New("compensation cost allocation overflow")
				}
				holdings[index].cost = allocation
			}
			remainingCost -= holdings[index].cost
			remainingQuantity -= holdings[index].delta
		}
	}

	var (
		movementIDs       []string
		affectedCompanies = make(map[string]struct{})
	)
	for _, holding := range holdings {
		if holding.delta >= 0 {
			continue
		}
		movementID, err := applyCorrectionMovement(
			ctx,
			transaction,
			commandID,
			compensationID,
			holding,
			reason,
			now,
		)
		if err != nil {
			return nil, nil, nil, err
		}
		movementIDs = append(movementIDs, movementID)
		affectedCompanies[holding.companyID] = struct{}{}
	}
	for _, holding := range holdings {
		if holding.delta <= 0 {
			continue
		}
		movementID, err := applyCorrectionMovement(
			ctx,
			transaction,
			commandID,
			compensationID,
			holding,
			reason,
			now,
		)
		if err != nil {
			return nil, nil, nil, err
		}
		movementIDs = append(movementIDs, movementID)
		affectedCompanies[holding.companyID] = struct{}{}
	}
	return movementIDs, affectedCompanies, nil, nil
}

const mathMaxInt64 = int64(^uint64(0) >> 1)

func applyCorrectionMovement(
	ctx context.Context,
	transaction *sql.Tx,
	commandID, compensationID string,
	holding holdingCompensation,
	reason string,
	now time.Time,
) (string, error) {
	movementID, err := ids.NewUUID()
	if err != nil {
		return "", err
	}
	var fromHolding, toHolding any
	if holding.delta < 0 {
		quantity := -holding.delta
		result, err := transaction.ExecContext(
			ctx,
			`UPDATE inventory.holdings
			    SET quantity_minor = quantity_minor - $2,
			        cost_basis_minor = cost_basis_minor - $3,
			        version = version + 1,
			        updated_at = clock_timestamp()
			  WHERE holding_id = $1
			    AND available_quantity_minor >= $2
			    AND cost_basis_minor >= $3`,
			holding.id,
			quantity,
			holding.cost,
		)
		if err != nil {
			return "", fmt.Errorf("remove compensated inventory: %w", err)
		}
		affected, _ := result.RowsAffected()
		if affected != 1 {
			return "", errors.New("compensated inventory changed before correction")
		}
		fromHolding = holding.id
	} else {
		if _, err := transaction.ExecContext(
			ctx,
			`UPDATE inventory.holdings
			    SET quantity_minor = quantity_minor + $2,
			        cost_basis_minor = cost_basis_minor + $3,
			        version = version + 1,
			        updated_at = clock_timestamp()
			  WHERE holding_id = $1`,
			holding.id,
			holding.delta,
			holding.cost,
		); err != nil {
			return "", fmt.Errorf("restore compensated inventory: %w", err)
		}
		toHolding = holding.id
	}
	quantity := holding.delta
	if quantity < 0 {
		quantity = -quantity
	}
	if _, err := transaction.ExecContext(
		ctx,
		`INSERT INTO inventory.movements (
			movement_id, command_id, movement_kind, source_id,
			from_holding_id, to_holding_id, quantity_minor,
			occurred_at, reason
		) VALUES ($1, $2, 'correction', $3, $4, $5, $6, $7, $8)`,
		movementID,
		commandID,
		compensationID,
		fromHolding,
		toHolding,
		quantity,
		now,
		"Operator compensation: "+truncateRunes(reason, 270),
	); err != nil {
		return "", fmt.Errorf("record inventory correction: %w", err)
	}
	if _, err := transaction.ExecContext(
		ctx,
		`INSERT INTO operator_admin.compensation_inventory_movements (
			compensation_id, movement_id
		) VALUES ($1, $2)`,
		compensationID,
		movementID,
	); err != nil {
		return "", fmt.Errorf("link compensation inventory movement: %w", err)
	}
	return movementID, nil
}

func advanceCompensatedCompanies(
	ctx context.Context,
	transaction *sql.Tx,
	actingCompanyID string,
	actingVersion int64,
	affected map[string]struct{},
) error {
	if err := incrementCompanyVersion(
		ctx,
		transaction,
		actingCompanyID,
		actingVersion,
	); err != nil {
		return err
	}
	delete(affected, actingCompanyID)
	companyIDs := make([]string, 0, len(affected))
	for companyID := range affected {
		companyIDs = append(companyIDs, companyID)
	}
	sort.Strings(companyIDs)
	for _, companyID := range companyIDs {
		if _, err := transaction.ExecContext(
			ctx,
			`UPDATE companies.companies
			    SET version = version + 1,
			        updated_at = clock_timestamp()
			  WHERE company_id = $1`,
			companyID,
		); err != nil {
			return fmt.Errorf("advance compensated company version: %w", err)
		}
	}
	return nil
}

func multiplyDivide(value, numerator, denominator int64) (int64, bool) {
	if value < 0 || numerator < 0 || denominator <= 0 {
		return 0, false
	}
	product := new(big.Int).Mul(big.NewInt(value), big.NewInt(numerator))
	product.Quo(product, big.NewInt(denominator))
	return product.Int64(), product.IsInt64()
}

func truncateRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}
