package gamepostgres

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"ascent/internal/identity"
	"ascent/internal/ledger"
	"ascent/internal/platform/ids"
	domainproduction "ascent/internal/production"
	protocol "ascent/protocol/gen/go"
)

type productionRunPayload struct {
	FacilityID string      `json:"facilityId"`
	Quantity   json.Number `json:"quantity"`
}

type productionRecord struct {
	facilityID        string
	companyID         string
	currency          string
	locationID        string
	recipeID          string
	ruleVersion       string
	inputCommodityID  string
	outputCommodityID string
	inputPerRun       int64
	outputPerRun      int64
	cycleSeconds      int
	nominalCapacity   int64
	utilizationBPS    int64
	conditionBPS      int64
	facilityVersion   int64
	status            string
}

func (s *Service) runProduction(
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
	var payload productionRunPayload
	if rejection := decodePayload(command.Payload, &payload); rejection != nil {
		return commandOutcome{}, rejection, nil
	}
	runs, ok := wholePositiveNumber(payload.Quantity, 1_000)
	if !ids.IsUUID(payload.FacilityID) || !ok {
		return commandOutcome{}, reject(
			"INVALID_PRODUCTION_RUN",
			"Choose a valid facility and a whole run quantity between 1 and 1,000.",
		), nil
	}
	record, rejection, err := loadProductionRecord(
		ctx,
		transaction,
		payload.FacilityID,
		*command.CompanyId,
	)
	if rejection != nil || err != nil {
		return commandOutcome{}, rejection, err
	}
	inputQuantity, ok := checkedMultiply(record.inputPerRun, runs)
	if !ok {
		return commandOutcome{}, reject("PRODUCTION_OVERFLOW", "The requested run is too large."), nil
	}
	outputQuantity, ok := checkedMultiply(record.outputPerRun, runs)
	if !ok {
		return commandOutcome{}, reject("PRODUCTION_OVERFLOW", "The requested run is too large."), nil
	}
	effectiveCapacity := record.nominalCapacity
	effectiveCapacity = effectiveCapacity * min64(record.utilizationBPS, record.conditionBPS) / 10_000
	if outputQuantity > effectiveCapacity {
		return commandOutcome{}, reject(
			"FACILITY_CAPACITY",
			"The requested run exceeds the facility's effective cycle capacity.",
		), nil
	}

	jobID, err := ids.NewUUID()
	if err != nil {
		return commandOutcome{}, nil, err
	}
	now := s.clock()
	state, err := domainproduction.NewState(
		[]domainproduction.Facility{{
			ID:         record.facilityID,
			CompanyID:  record.companyID,
			LocationID: record.locationID,
			RecipeID:   record.recipeID,
		}},
		[]domainproduction.Recipe{{
			ID:              record.recipeID,
			InputProductID:  record.inputCommodityID,
			InputQuantity:   record.inputPerRun,
			OutputProductID: record.outputCommodityID,
			OutputQuantity:  record.outputPerRun,
		}},
		[]domainproduction.Job{{
			ID:         jobID,
			FacilityID: record.facilityID,
			Runs:       runs,
			DueTick:    now.Unix(),
			Status:     domainproduction.JobScheduled,
		}},
	)
	if err != nil {
		return commandOutcome{}, nil, fmt.Errorf("construct production authority state: %w", err)
	}
	_, execution, err := state.ExecuteDue(domainproduction.ExecuteCommand{
		JobID:              jobID,
		CompanyID:          record.companyID,
		AtTick:             now.Unix(),
		ExecutionID:        command.CommandId,
		OperationID:        command.CommandId,
		IdempotencyKey:     command.IdempotencyKey,
		RequestFingerprint: string(command.Payload),
	})
	if err != nil {
		return commandOutcome{}, nil, fmt.Errorf("validate production execution: %w", err)
	}
	if len(execution.Transformation.Inputs) != 1 ||
		len(execution.Transformation.Outputs) != 1 {
		return commandOutcome{}, nil, errors.New("MVP production recipe must have one input and one output")
	}

	randomSeed := deterministicSeed(command.CommandId)
	leaseExpiresAt := now.Add(time.Minute)
	if _, err := transaction.ExecContext(
		ctx,
		`INSERT INTO production.jobs (
			job_id, facility_id, recipe_id, rule_version, due_at,
			status, attempt_count, random_seed, worker_id,
			claimed_at, lease_expires_at
		) VALUES ($1, $2, $3, $4, $5, 'running', 1, $6, $7, $5, $8)`,
		jobID,
		record.facilityID,
		record.recipeID,
		record.ruleVersion,
		now,
		randomSeed,
		"command:"+command.CommandId,
		leaseExpiresAt,
	); err != nil {
		return commandOutcome{}, nil, fmt.Errorf("create production job: %w", err)
	}

	inputHoldingID, inputCost, rejection, err := consumeProductionInput(
		ctx,
		transaction,
		command.CommandId,
		jobID,
		record,
		inputQuantity,
		now,
	)
	if rejection != nil || err != nil {
		return commandOutcome{}, rejection, err
	}
	outputHoldingID, err := produceOutput(
		ctx,
		transaction,
		command.CommandId,
		jobID,
		record,
		outputQuantity,
		inputCost,
		now,
	)
	if err != nil {
		return commandOutcome{}, nil, err
	}

	accounts, err := loadNamedAccounts(
		ctx,
		transaction,
		record.companyID,
		"INVENTORY",
		"PRODUCTION_WIP",
	)
	if err != nil {
		return commandOutcome{}, nil, err
	}
	journalID, err := postJournal(
		ctx,
		transaction,
		command.CommandId,
		record.companyID,
		record.currency,
		"production",
		jobID,
		"Move input cost through production work in progress",
		nil,
		[]ledger.Entry{
			{AccountID: accounts["PRODUCTION_WIP"], Currency: record.currency, Amount: inputCost},
			{AccountID: accounts["INVENTORY"], Currency: record.currency, Amount: -inputCost},
			{AccountID: accounts["INVENTORY"], Currency: record.currency, Amount: inputCost},
			{AccountID: accounts["PRODUCTION_WIP"], Currency: record.currency, Amount: -inputCost},
		},
		now,
	)
	if err != nil {
		return commandOutcome{}, nil, err
	}
	if _, err := transaction.ExecContext(
		ctx,
		`INSERT INTO production.job_journals (job_id, journal_id)
		 VALUES ($1, $2)`,
		jobID,
		journalID,
	); err != nil {
		return commandOutcome{}, nil, fmt.Errorf("link production journal: %w", err)
	}
	event, err := appendEvent(
		ctx,
		transaction,
		&command.CommandId,
		"company:"+record.companyID,
		"PRODUCTION_COMMITTED",
		map[string]any{
			"jobId":               jobID,
			"facilityId":          record.facilityID,
			"inputHoldingId":      inputHoldingID,
			"outputHoldingId":     outputHoldingID,
			"inputQuantityMinor":  inputQuantity,
			"outputQuantityMinor": outputQuantity,
			"ruleVersion":         record.ruleVersion,
		},
		now,
	)
	if err != nil {
		return commandOutcome{}, nil, err
	}
	if _, err := transaction.ExecContext(
		ctx,
		`UPDATE production.jobs
		    SET status = 'committed',
		        completed_at = $2,
		        event_id = $3,
		        updated_at = clock_timestamp()
		  WHERE job_id = $1 AND status = 'running'`,
		jobID,
		now,
		*event.EventId,
	); err != nil {
		return commandOutcome{}, nil, fmt.Errorf("commit production job: %w", err)
	}
	if _, err := transaction.ExecContext(
		ctx,
		`UPDATE production.facilities
		    SET next_execution_at = $2,
		        version = version + 1,
		        updated_at = clock_timestamp()
		  WHERE facility_id = $1 AND version = $3`,
		record.facilityID,
		now.Add(time.Duration(record.cycleSeconds)*time.Second),
		record.facilityVersion,
	); err != nil {
		return commandOutcome{}, nil, fmt.Errorf("advance production facility: %w", err)
	}
	if err := incrementCompanyVersion(
		ctx,
		transaction,
		record.companyID,
		companyVersion,
	); err != nil {
		return commandOutcome{}, nil, err
	}
	return commandOutcome{payload: map[string]any{
		"jobId":         jobID,
		"journalId":     journalID,
		"eventSequence": event.Sequence,
	}}, nil, nil
}

func loadProductionRecord(
	ctx context.Context,
	transaction *sql.Tx,
	facilityID, companyID string,
) (productionRecord, *commandRejection, error) {
	var record productionRecord
	err := transaction.QueryRowContext(
		ctx,
		`SELECT facility.facility_id,
		        facility.company_id,
		        company.base_currency,
		        facility.location_id,
		        recipe.recipe_id,
		        recipe.rule_version,
		        input.commodity_id,
		        recipe.output_commodity_id,
		        input.quantity_minor,
		        recipe.output_quantity_minor,
		        recipe.cycle_seconds,
		        facility.nominal_capacity_minor,
		        facility.utilization_basis_points,
		        facility.condition_basis_points,
		        facility.version,
		        facility.status
		   FROM production.facilities AS facility
		   JOIN companies.companies AS company
		     ON company.company_id = facility.company_id
		   JOIN production.recipes AS recipe
		     ON recipe.recipe_id = facility.active_recipe_id
		   JOIN production.recipe_inputs AS input
		     ON input.recipe_id = recipe.recipe_id
		  WHERE facility.facility_id = $1
		    AND facility.company_id = $2
		  FOR UPDATE OF facility`,
		facilityID,
		companyID,
	).Scan(
		&record.facilityID,
		&record.companyID,
		&record.currency,
		&record.locationID,
		&record.recipeID,
		&record.ruleVersion,
		&record.inputCommodityID,
		&record.outputCommodityID,
		&record.inputPerRun,
		&record.outputPerRun,
		&record.cycleSeconds,
		&record.nominalCapacity,
		&record.utilizationBPS,
		&record.conditionBPS,
		&record.facilityVersion,
		&record.status,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return productionRecord{}, reject(
			"FACILITY_NOT_FOUND",
			"That facility is not owned by the operating company.",
		), nil
	}
	if err != nil {
		return productionRecord{}, nil, fmt.Errorf("load production facility: %w", err)
	}
	if record.status != "operational" && record.status != "constrained" {
		return productionRecord{}, reject(
			"FACILITY_UNAVAILABLE",
			"That facility is not available for production.",
		), nil
	}
	return record, nil, nil
}

func consumeProductionInput(
	ctx context.Context,
	transaction *sql.Tx,
	commandID, jobID string,
	record productionRecord,
	quantity int64,
	now time.Time,
) (string, int64, *commandRejection, error) {
	var (
		holdingID string
		onHand    int64
		available int64
		costBasis int64
	)
	err := transaction.QueryRowContext(
		ctx,
		`SELECT holding_id, quantity_minor, available_quantity_minor, cost_basis_minor
		   FROM inventory.holdings
		  WHERE company_id = $1
		    AND location_id = $2
		    AND commodity_id = $3
		  FOR UPDATE`,
		record.companyID,
		record.locationID,
		record.inputCommodityID,
	).Scan(&holdingID, &onHand, &available, &costBasis)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && available < quantity) {
		return "", 0, reject(
			"INSUFFICIENT_INPUT",
			"Available input inventory is below the production requirement.",
		), nil
	}
	if err != nil {
		return "", 0, nil, fmt.Errorf("lock production input: %w", err)
	}
	inputCost, ok := proportionalCost(costBasis, quantity, onHand)
	if !ok || inputCost <= 0 {
		return "", 0, reject(
			"UNVALUED_INPUT",
			"The input inventory has no traceable book value for this run.",
		), nil
	}
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
		holdingID,
		quantity,
		inputCost,
	)
	if err != nil {
		return "", 0, nil, fmt.Errorf("consume production input: %w", err)
	}
	affected, _ := result.RowsAffected()
	if affected != 1 {
		return "", 0, nil, errors.New("production input changed before settlement")
	}
	movementID, err := ids.NewUUID()
	if err != nil {
		return "", 0, nil, err
	}
	if _, err := transaction.ExecContext(
		ctx,
		`INSERT INTO inventory.movements (
			movement_id, command_id, movement_kind, source_id,
			from_holding_id, quantity_minor, occurred_at, reason
		) VALUES ($1, $2, 'consumption', $3, $4, $5, $6, $7)`,
		movementID,
		commandID,
		jobID,
		holdingID,
		quantity,
		now,
		"Consumed by versioned production recipe "+record.ruleVersion,
	); err != nil {
		return "", 0, nil, fmt.Errorf("record production consumption: %w", err)
	}
	if _, err := transaction.ExecContext(
		ctx,
		`INSERT INTO production.job_inventory_movements (job_id, movement_id, role)
		 VALUES ($1, $2, 'input')`,
		jobID,
		movementID,
	); err != nil {
		return "", 0, nil, fmt.Errorf("link production input movement: %w", err)
	}
	return holdingID, inputCost, nil, nil
}

func produceOutput(
	ctx context.Context,
	transaction *sql.Tx,
	commandID, jobID string,
	record productionRecord,
	quantity, cost int64,
	now time.Time,
) (string, error) {
	holdingID, err := ensureHolding(
		ctx,
		transaction,
		record.companyID,
		record.locationID,
		record.outputCommodityID,
	)
	if err != nil {
		return "", err
	}
	if _, err := transaction.ExecContext(
		ctx,
		`UPDATE inventory.holdings
		    SET quantity_minor = quantity_minor + $2,
		        cost_basis_minor = cost_basis_minor + $3,
		        version = version + 1,
		        updated_at = clock_timestamp()
		  WHERE holding_id = $1`,
		holdingID,
		quantity,
		cost,
	); err != nil {
		return "", fmt.Errorf("store production output: %w", err)
	}
	movementID, err := ids.NewUUID()
	if err != nil {
		return "", err
	}
	if _, err := transaction.ExecContext(
		ctx,
		`INSERT INTO inventory.movements (
			movement_id, command_id, movement_kind, source_id,
			to_holding_id, quantity_minor, occurred_at, reason
		) VALUES ($1, $2, 'production', $3, $4, $5, $6, $7)`,
		movementID,
		commandID,
		jobID,
		holdingID,
		quantity,
		now,
		"Produced by versioned recipe "+record.ruleVersion,
	); err != nil {
		return "", fmt.Errorf("record production output: %w", err)
	}
	if _, err := transaction.ExecContext(
		ctx,
		`INSERT INTO production.job_inventory_movements (job_id, movement_id, role)
		 VALUES ($1, $2, 'output')`,
		jobID,
		movementID,
	); err != nil {
		return "", fmt.Errorf("link production output movement: %w", err)
	}
	return holdingID, nil
}

func ensureHolding(
	ctx context.Context,
	transaction *sql.Tx,
	companyID, locationID, commodityID string,
) (string, error) {
	var holdingID string
	err := transaction.QueryRowContext(
		ctx,
		`SELECT holding_id
		   FROM inventory.holdings
		  WHERE company_id = $1
		    AND location_id = $2
		    AND commodity_id = $3
		  FOR UPDATE`,
		companyID,
		locationID,
		commodityID,
	).Scan(&holdingID)
	if err == nil {
		return holdingID, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("load inventory holding: %w", err)
	}
	holdingID, err = ids.NewUUID()
	if err != nil {
		return "", err
	}
	if _, err := transaction.ExecContext(
		ctx,
		`INSERT INTO inventory.holdings (
			holding_id, company_id, location_id, commodity_id,
			quantity_minor, reserved_quantity_minor, cost_basis_minor
		) VALUES ($1, $2, $3, $4, 0, 0, 0)`,
		holdingID,
		companyID,
		locationID,
		commodityID,
	); err != nil {
		return "", fmt.Errorf("create inventory holding: %w", err)
	}
	return holdingID, nil
}

func wholePositiveNumber(value json.Number, limit int64) (int64, bool) {
	whole, err := value.Int64()
	if err != nil || whole <= 0 || whole > limit {
		return 0, false
	}
	return whole, true
}

func checkedMultiply(left, right int64) (int64, bool) {
	if left <= 0 || right <= 0 || left > math.MaxInt64/right {
		return 0, false
	}
	return left * right, true
}

func proportionalCost(cost, quantity, totalQuantity int64) (int64, bool) {
	if cost < 0 || quantity <= 0 || totalQuantity <= 0 || quantity > totalQuantity {
		return 0, false
	}
	if quantity == totalQuantity {
		return cost, true
	}
	if cost > math.MaxInt64/quantity {
		return 0, false
	}
	return cost * quantity / totalQuantity, true
}

func deterministicSeed(value string) int64 {
	hash := sha256.Sum256([]byte(strings.TrimSpace(value)))
	return int64(binary.BigEndian.Uint64(hash[:8]) & math.MaxInt64)
}

func min64(left, right int64) int64 {
	if left < right {
		return left
	}
	return right
}
