package gamepostgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	domainfreight "ascent/internal/freight"
	"ascent/internal/identity"
	"ascent/internal/ledger"
	"ascent/internal/platform/ids"
	protocol "ascent/protocol/gen/go"
)

type freightDeliverPayload struct {
	ShipmentID string `json:"shipmentId"`
}

type freightRecord struct {
	deliveryID            string
	deliveryStatus        string
	deliveryQuantity      int64
	scheduledDeparture    time.Time
	departedAt            sql.NullTime
	contractID            string
	contractStatus        string
	contractQuantity      int64
	deliveredQuantity     int64
	unitPrice             int64
	currency              string
	shipperCompanyID      string
	carrierCompanyID      string
	commodityID           string
	quantityScale         int64
	originLocationID      string
	destinationLocationID string
	contractVersion       int64
}

func (s *Service) deliverFreight(
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
	var payload freightDeliverPayload
	if rejection := decodePayload(command.Payload, &payload); rejection != nil {
		return commandOutcome{}, rejection, nil
	}
	if !ids.IsUUID(payload.ShipmentID) {
		return commandOutcome{}, reject("INVALID_DELIVERY", "Choose a valid freight delivery."), nil
	}
	record, rejection, err := loadFreightRecord(
		ctx,
		transaction,
		payload.ShipmentID,
		*command.CompanyId,
	)
	if rejection != nil || err != nil {
		return commandOutcome{}, rejection, err
	}
	now := s.clock()
	if record.deliveryStatus == "scheduled" && now.Before(record.scheduledDeparture) {
		return commandOutcome{}, reject("DELIVERY_NOT_DUE", "That delivery is not yet due to depart."), nil
	}
	state, err := domainfreight.NewState([]domainfreight.Contract{{
		ID:                    record.contractID,
		OwnerCompanyID:        record.shipperCompanyID,
		CarrierCompanyID:      record.carrierCompanyID,
		ProductID:             record.commodityID,
		Quantity:              record.deliveryQuantity,
		OriginLocationID:      record.originLocationID,
		DestinationLocationID: record.destinationLocationID,
		DueTick:               record.scheduledDeparture.Unix(),
		Status:                domainfreight.ContractInTransit,
	}})
	if err != nil {
		return commandOutcome{}, nil, fmt.Errorf("construct freight authority state: %w", err)
	}
	_, delivery, err := state.Deliver(domainfreight.DeliverCommand{
		ContractID:         record.contractID,
		CarrierCompanyID:   record.carrierCompanyID,
		AtTick:             now.Unix(),
		DeliveryID:         record.deliveryID,
		OperationID:        command.CommandId,
		IdempotencyKey:     command.IdempotencyKey,
		RequestFingerprint: string(command.Payload),
	})
	if err != nil {
		return commandOutcome{}, nil, fmt.Errorf("validate freight delivery: %w", err)
	}

	if record.deliveryStatus == "scheduled" {
		if _, err := transaction.ExecContext(
			ctx,
			`UPDATE freight.deliveries
			    SET status = 'in_transit',
			        departed_at = $2
			  WHERE delivery_id = $1 AND status = 'scheduled'`,
			record.deliveryID,
			now,
		); err != nil {
			return commandOutcome{}, nil, fmt.Errorf("depart freight delivery: %w", err)
		}
		record.departedAt = sql.NullTime{Time: now, Valid: true}
	}
	movementID, transferCost, rejection, err := settleFreightInventory(
		ctx,
		transaction,
		command.CommandId,
		record,
		delivery.Movement.Quantity,
		now,
	)
	if rejection != nil || err != nil {
		return commandOutcome{}, rejection, err
	}
	payment, ok := quoteAmount(
		record.unitPrice,
		record.deliveryQuantity,
		record.quantityScale,
	)
	if !ok || payment <= 0 {
		return commandOutcome{}, reject(
			"INVALID_FREIGHT_PRICE",
			"The freight contract must have a positive, exact settlement value.",
		), nil
	}
	if _, rejection, err := reserveCashCapacity(
		ctx,
		transaction,
		record.shipperCompanyID,
		record.currency,
		payment,
	); rejection != nil || err != nil {
		return commandOutcome{}, rejection, err
	}
	shipperAccounts, err := loadNamedAccounts(
		ctx,
		transaction,
		record.shipperCompanyID,
		"CASH",
		"COGS",
	)
	if err != nil {
		return commandOutcome{}, nil, err
	}
	carrierAccounts, err := loadNamedAccounts(
		ctx,
		transaction,
		record.carrierCompanyID,
		"CASH",
		"SALES",
	)
	if err != nil {
		return commandOutcome{}, nil, err
	}
	shipperJournalID, err := postJournal(
		ctx,
		transaction,
		command.CommandId,
		record.shipperCompanyID,
		record.currency,
		"freight",
		record.deliveryID,
		"Settle freight service expense",
		nil,
		[]ledger.Entry{
			{AccountID: shipperAccounts["COGS"], Currency: record.currency, Amount: payment},
			{AccountID: shipperAccounts["CASH"], Currency: record.currency, Amount: -payment},
		},
		now,
	)
	if err != nil {
		return commandOutcome{}, nil, err
	}
	carrierJournalID, err := postJournal(
		ctx,
		transaction,
		command.CommandId,
		record.carrierCompanyID,
		record.currency,
		"freight",
		record.deliveryID,
		"Settle freight service revenue",
		nil,
		[]ledger.Entry{
			{AccountID: carrierAccounts["CASH"], Currency: record.currency, Amount: payment},
			{AccountID: carrierAccounts["SALES"], Currency: record.currency, Amount: -payment},
		},
		now,
	)
	if err != nil {
		return commandOutcome{}, nil, err
	}
	event, err := appendEvent(
		ctx,
		transaction,
		&command.CommandId,
		"contract:"+record.contractID,
		"FREIGHT_DELIVERED",
		map[string]any{
			"deliveryId":           record.deliveryID,
			"contractId":           record.contractID,
			"quantityMinor":        record.deliveryQuantity,
			"transferredCostMinor": transferCost,
			"paymentMinor":         payment,
			"carrierCompanyId":     record.carrierCompanyID,
			"shipperCompanyId":     record.shipperCompanyID,
		},
		now,
	)
	if err != nil {
		return commandOutcome{}, nil, err
	}
	departedAt := record.departedAt.Time
	if !record.departedAt.Valid {
		return commandOutcome{}, nil, errors.New("in-transit delivery is missing departure time")
	}
	if _, err := transaction.ExecContext(
		ctx,
		`UPDATE freight.deliveries
		    SET status = 'delivered',
		        departed_at = $2,
		        arrived_at = $3,
		        inventory_movement_id = $4,
		        shipper_journal_id = $5,
		        carrier_journal_id = $6,
		        event_id = $7
		  WHERE delivery_id = $1 AND status = 'in_transit'`,
		record.deliveryID,
		departedAt,
		now,
		movementID,
		shipperJournalID,
		carrierJournalID,
		*event.EventId,
	); err != nil {
		return commandOutcome{}, nil, fmt.Errorf("commit freight delivery: %w", err)
	}
	deliveredTotal := record.deliveredQuantity + record.deliveryQuantity
	contractStatus := "active"
	if deliveredTotal == record.contractQuantity {
		contractStatus = "completed"
	}
	result, err := transaction.ExecContext(
		ctx,
		`UPDATE freight.contracts
		    SET delivered_quantity_minor = $2,
		        status = $3,
		        version = version + 1,
		        updated_at = clock_timestamp()
		  WHERE contract_id = $1
		    AND version = $4
		    AND delivered_quantity_minor = $5`,
		record.contractID,
		deliveredTotal,
		contractStatus,
		record.contractVersion,
		record.deliveredQuantity,
	)
	if err != nil {
		return commandOutcome{}, nil, fmt.Errorf("advance freight contract: %w", err)
	}
	affected, _ := result.RowsAffected()
	if affected != 1 {
		return commandOutcome{}, nil, errors.New("freight contract changed before settlement")
	}
	if err := incrementCompanyVersion(
		ctx,
		transaction,
		record.carrierCompanyID,
		companyVersion,
	); err != nil {
		return commandOutcome{}, nil, err
	}
	return commandOutcome{payload: map[string]any{
		"deliveryId":      record.deliveryID,
		"movementId":      movementID,
		"eventSequence":   event.Sequence,
		"deliveredStatus": contractStatus,
	}}, nil, nil
}

func loadFreightRecord(
	ctx context.Context,
	transaction *sql.Tx,
	deliveryID, actingCompanyID string,
) (freightRecord, *commandRejection, error) {
	var (
		record        freightRecord
		quantityScale int
	)
	err := transaction.QueryRowContext(
		ctx,
		`SELECT delivery.delivery_id,
		        delivery.status,
		        delivery.quantity_minor,
		        delivery.scheduled_departure_at,
		        delivery.departed_at,
		        contract.contract_id,
		        contract.status,
		        contract.quantity_minor,
		        contract.delivered_quantity_minor,
		        contract.unit_price_minor,
		        contract.currency,
		        contract.shipper_company_id,
		        contract.carrier_company_id,
		        contract.commodity_id,
		        commodity.quantity_scale,
		        route.origin_location_id,
		        route.destination_location_id,
		        contract.version
		   FROM freight.deliveries AS delivery
		   JOIN freight.contracts AS contract
		     ON contract.contract_id = delivery.contract_id
		   JOIN freight.routes AS route
		     ON route.route_id = contract.route_id
		   JOIN inventory.commodities AS commodity
		     ON commodity.commodity_id = contract.commodity_id
		  WHERE delivery.delivery_id = $1
		  FOR UPDATE OF delivery, contract`,
		deliveryID,
	).Scan(
		&record.deliveryID,
		&record.deliveryStatus,
		&record.deliveryQuantity,
		&record.scheduledDeparture,
		&record.departedAt,
		&record.contractID,
		&record.contractStatus,
		&record.contractQuantity,
		&record.deliveredQuantity,
		&record.unitPrice,
		&record.currency,
		&record.shipperCompanyID,
		&record.carrierCompanyID,
		&record.commodityID,
		&quantityScale,
		&record.originLocationID,
		&record.destinationLocationID,
		&record.contractVersion,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return freightRecord{}, reject("DELIVERY_NOT_FOUND", "That freight delivery does not exist."), nil
	}
	if err != nil {
		return freightRecord{}, nil, fmt.Errorf("load freight delivery: %w", err)
	}
	if record.carrierCompanyID != actingCompanyID {
		return freightRecord{}, reject(
			"FORBIDDEN",
			"Only the assigned carrier may commit this delivery.",
		), nil
	}
	if record.contractStatus != "active" ||
		(record.deliveryStatus != "scheduled" && record.deliveryStatus != "in_transit") {
		return freightRecord{}, reject(
			"DELIVERY_CLOSED",
			"That freight obligation is no longer deliverable.",
		), nil
	}
	if record.deliveredQuantity > record.contractQuantity-record.deliveryQuantity {
		return freightRecord{}, nil, errors.New("delivery would exceed contracted quantity")
	}
	record.quantityScale = powerOfTen(quantityScale)
	if record.quantityScale == 0 {
		return freightRecord{}, nil, errors.New("freight commodity scale is invalid")
	}
	return record, nil, nil
}

func settleFreightInventory(
	ctx context.Context,
	transaction *sql.Tx,
	commandID string,
	record freightRecord,
	quantity int64,
	now time.Time,
) (string, int64, *commandRejection, error) {
	var (
		sourceHoldingID string
		onHand          int64
		available       int64
		costBasis       int64
	)
	err := transaction.QueryRowContext(
		ctx,
		`SELECT holding_id, quantity_minor, available_quantity_minor, cost_basis_minor
		   FROM inventory.holdings
		  WHERE company_id = $1
		    AND location_id = $2
		    AND commodity_id = $3
		  FOR UPDATE`,
		record.shipperCompanyID,
		record.originLocationID,
		record.commodityID,
	).Scan(&sourceHoldingID, &onHand, &available, &costBasis)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && available < quantity) {
		return "", 0, reject(
			"INSUFFICIENT_CARGO",
			"The shipper does not have enough available cargo at the route origin.",
		), nil
	}
	if err != nil {
		return "", 0, nil, fmt.Errorf("lock freight cargo: %w", err)
	}
	transferCost, ok := proportionalCost(costBasis, quantity, onHand)
	if !ok {
		return "", 0, nil, errors.New("freight cargo cost basis overflow")
	}
	destinationHoldingID, err := ensureHolding(
		ctx,
		transaction,
		record.shipperCompanyID,
		record.destinationLocationID,
		record.commodityID,
	)
	if err != nil {
		return "", 0, nil, err
	}
	if _, err := transaction.ExecContext(
		ctx,
		`UPDATE inventory.holdings
		    SET quantity_minor = quantity_minor - $2,
		        cost_basis_minor = cost_basis_minor - $3,
		        version = version + 1,
		        updated_at = clock_timestamp()
		  WHERE holding_id = $1`,
		sourceHoldingID,
		quantity,
		transferCost,
	); err != nil {
		return "", 0, nil, fmt.Errorf("remove freight cargo at origin: %w", err)
	}
	if _, err := transaction.ExecContext(
		ctx,
		`UPDATE inventory.holdings
		    SET quantity_minor = quantity_minor + $2,
		        cost_basis_minor = cost_basis_minor + $3,
		        version = version + 1,
		        updated_at = clock_timestamp()
		  WHERE holding_id = $1`,
		destinationHoldingID,
		quantity,
		transferCost,
	); err != nil {
		return "", 0, nil, fmt.Errorf("store freight cargo at destination: %w", err)
	}
	movementID, err := ids.NewUUID()
	if err != nil {
		return "", 0, nil, err
	}
	if _, err := transaction.ExecContext(
		ctx,
		`INSERT INTO inventory.movements (
			movement_id, command_id, movement_kind, source_id,
			from_holding_id, to_holding_id, quantity_minor,
			occurred_at, reason
		) VALUES ($1, $2, 'transfer', $3, $4, $5, $6, $7, $8)`,
		movementID,
		commandID,
		record.deliveryID,
		sourceHoldingID,
		destinationHoldingID,
		quantity,
		now,
		"Committed freight delivery "+record.deliveryID,
	); err != nil {
		return "", 0, nil, fmt.Errorf("record freight inventory transfer: %w", err)
	}
	return movementID, transferCost, nil, nil
}
