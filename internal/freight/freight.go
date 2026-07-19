// Package freight models simple due contract delivery. It emits a conservative
// inventory movement for the caller to commit atomically with contract state.
package freight

import (
	"errors"
	"fmt"
	"sort"

	"ascent/internal/inventory"
)

var (
	ErrInvalidContract     = errors.New("invalid freight contract")
	ErrUnknownContract     = errors.New("unknown freight contract")
	ErrCarrierOwnership    = errors.New("freight contract belongs to another carrier")
	ErrNotDue              = errors.New("freight contract is not due")
	ErrIdempotencyConflict = errors.New("idempotency key reused with changed freight request")
)

type ContractStatus string

const (
	ContractInTransit ContractStatus = "in_transit"
	ContractDelivered ContractStatus = "delivered"
)

type Contract struct {
	ID                    string         `json:"id"`
	OwnerCompanyID        string         `json:"ownerCompanyId"`
	CarrierCompanyID      string         `json:"carrierCompanyId"`
	ProductID             string         `json:"productId"`
	Quantity              int64          `json:"quantity"`
	OriginLocationID      string         `json:"originLocationId"`
	DestinationLocationID string         `json:"destinationLocationId"`
	DueTick               int64          `json:"dueTick"`
	Status                ContractStatus `json:"status"`
	DeliveryID            string         `json:"deliveryId,omitempty"`
}

type DeliverCommand struct {
	ContractID         string
	CarrierCompanyID   string
	AtTick             int64
	DeliveryID         string
	OperationID        string
	IdempotencyKey     string
	RequestFingerprint string
}

type Delivery struct {
	ID            string             `json:"id"`
	OperationID   string             `json:"operationId"`
	ContractID    string             `json:"contractId"`
	DeliveredTick int64              `json:"deliveredTick"`
	Movement      inventory.Movement `json:"movement"`
	Replay        bool               `json:"replay"`
}

type receipt struct {
	fingerprint string
	delivery    Delivery
}

type State struct {
	contracts  map[string]Contract
	deliveries map[string]Delivery
	receipts   map[string]receipt
}

func NewState(contracts []Contract) (State, error) {
	state := State{
		contracts:  make(map[string]Contract, len(contracts)),
		deliveries: make(map[string]Delivery),
		receipts:   make(map[string]receipt),
	}
	for _, contract := range contracts {
		if contract.ID == "" || contract.OwnerCompanyID == "" || contract.CarrierCompanyID == "" ||
			contract.ProductID == "" || contract.Quantity <= 0 ||
			contract.OriginLocationID == "" || contract.DestinationLocationID == "" ||
			contract.OriginLocationID == contract.DestinationLocationID ||
			contract.DueTick < 0 || contract.Status != ContractInTransit {
			return State{}, ErrInvalidContract
		}
		if _, exists := state.contracts[contract.ID]; exists {
			return State{}, fmt.Errorf("%w: duplicate %q", ErrInvalidContract, contract.ID)
		}
		state.contracts[contract.ID] = contract
	}
	return state, nil
}

func (s State) Deliver(command DeliverCommand) (State, Delivery, error) {
	if command.ContractID == "" || command.CarrierCompanyID == "" || command.DeliveryID == "" ||
		command.OperationID == "" || command.IdempotencyKey == "" || command.RequestFingerprint == "" {
		return State{}, Delivery{}, ErrInvalidContract
	}
	receiptKey := command.CarrierCompanyID + "\x00" + command.IdempotencyKey
	if previous, exists := s.receipts[receiptKey]; exists {
		if previous.fingerprint != command.RequestFingerprint {
			return State{}, Delivery{}, ErrIdempotencyConflict
		}
		replay := previous.delivery
		replay.Replay = true
		return s, replay, nil
	}

	contract, exists := s.contracts[command.ContractID]
	if !exists {
		return State{}, Delivery{}, fmt.Errorf("%w: %q", ErrUnknownContract, command.ContractID)
	}
	if contract.CarrierCompanyID != command.CarrierCompanyID {
		return State{}, Delivery{}, ErrCarrierOwnership
	}
	if contract.Status == ContractDelivered {
		delivery := s.deliveries[contract.DeliveryID]
		delivery.Replay = true
		return s, delivery, nil
	}
	if command.AtTick < contract.DueTick {
		return State{}, Delivery{}, ErrNotDue
	}

	delivery := Delivery{
		ID:            command.DeliveryID,
		OperationID:   command.OperationID,
		ContractID:    contract.ID,
		DeliveredTick: command.AtTick,
		Movement: inventory.Movement{
			ID:          command.DeliveryID + "-inventory",
			OperationID: command.OperationID,
			From: inventory.Position{
				CompanyID:  contract.OwnerCompanyID,
				LocationID: contract.OriginLocationID,
				ProductID:  contract.ProductID,
			},
			To: inventory.Position{
				CompanyID:  contract.OwnerCompanyID,
				LocationID: contract.DestinationLocationID,
				ProductID:  contract.ProductID,
			},
			Quantity: contract.Quantity,
		},
	}

	next := s.clone()
	contract.Status = ContractDelivered
	contract.DeliveryID = delivery.ID
	next.contracts[contract.ID] = contract
	next.deliveries[delivery.ID] = delivery
	next.receipts[receiptKey] = receipt{
		fingerprint: command.RequestFingerprint,
		delivery:    delivery,
	}
	return next, delivery, nil
}

func (s State) Contracts() []Contract {
	result := make([]Contract, 0, len(s.contracts))
	for _, contract := range s.contracts {
		result = append(result, contract)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

func (s State) Deliveries() []Delivery {
	result := make([]Delivery, 0, len(s.deliveries))
	for _, delivery := range s.deliveries {
		result = append(result, delivery)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

func (s State) clone() State {
	next := State{
		contracts:  make(map[string]Contract, len(s.contracts)),
		deliveries: make(map[string]Delivery, len(s.deliveries)),
		receipts:   make(map[string]receipt, len(s.receipts)),
	}
	for id, contract := range s.contracts {
		next.contracts[id] = contract
	}
	for id, delivery := range s.deliveries {
		next.deliveries[id] = delivery
	}
	for key, value := range s.receipts {
		next.receipts[key] = value
	}
	return next
}
