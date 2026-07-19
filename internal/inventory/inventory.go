// Package inventory provides fixed-scale holdings, reservations, conservative
// transfers, and explicitly recorded production transformations.
package inventory

import (
	"errors"
	"fmt"
	"math"
	"sort"
)

var (
	ErrInvalidPosition      = errors.New("invalid inventory position")
	ErrInvalidQuantity      = errors.New("invalid inventory quantity")
	ErrInsufficientQuantity = errors.New("insufficient available inventory")
	ErrDuplicateReservation = errors.New("duplicate inventory reservation")
	ErrUnknownReservation   = errors.New("unknown inventory reservation")
	ErrDuplicateMovement    = errors.New("duplicate inventory movement")
	ErrDuplicateTransform   = errors.New("duplicate inventory transformation")
	ErrConservation         = errors.New("inventory movement does not conserve product")
	ErrQuantityOverflow     = errors.New("inventory quantity overflow")
)

// Position identifies a holding. Quantity scale is defined by the product
// catalog; all quantities here are fixed-scale int64 values.
type Position struct {
	CompanyID  string `json:"companyId"`
	LocationID string `json:"locationId"`
	ProductID  string `json:"productId"`
}

type Holding struct {
	Position Position `json:"position"`
	Quantity int64    `json:"quantity"`
}

type Reservation struct {
	ID          string   `json:"id"`
	OperationID string   `json:"operationId"`
	Purpose     string   `json:"purpose"`
	Position    Position `json:"position"`
	Quantity    int64    `json:"quantity"`
}

// Movement is conservative: From and To must reference the same product and
// exactly Quantity is removed and added.
type Movement struct {
	ID            string   `json:"id"`
	OperationID   string   `json:"operationId"`
	ReservationID string   `json:"reservationId,omitempty"`
	From          Position `json:"from"`
	To            Position `json:"to"`
	Quantity      int64    `json:"quantity"`
}

type Item struct {
	Position Position `json:"position"`
	Quantity int64    `json:"quantity"`
}

// Transformation explicitly records a recipe conversion. Unlike a Movement,
// it may change product identity and quantity according to the owning recipe.
type Transformation struct {
	ID          string `json:"id"`
	OperationID string `json:"operationId"`
	Inputs      []Item `json:"inputs"`
	Outputs     []Item `json:"outputs"`
}

type State struct {
	holdings        map[Position]int64
	reservations    map[string]Reservation
	movements       []Movement
	movementIDs     map[string]struct{}
	transformations []Transformation
	transformIDs    map[string]struct{}
}

func NewState(initial []Holding) (State, error) {
	state := State{
		holdings:     make(map[Position]int64, len(initial)),
		reservations: make(map[string]Reservation),
		movementIDs:  make(map[string]struct{}),
		transformIDs: make(map[string]struct{}),
	}
	for _, holding := range initial {
		if err := validatePosition(holding.Position); err != nil {
			return State{}, err
		}
		if holding.Quantity < 0 {
			return State{}, fmt.Errorf("%w: initial holding", ErrInvalidQuantity)
		}
		if _, exists := state.holdings[holding.Position]; exists {
			return State{}, fmt.Errorf("%w: duplicate initial position", ErrInvalidPosition)
		}
		state.holdings[holding.Position] = holding.Quantity
	}
	return state, nil
}

func (s State) Quantity(position Position) int64 {
	return s.holdings[position]
}

func (s State) Available(position Position) int64 {
	available := s.holdings[position]
	for _, reservation := range s.reservations {
		if reservation.Position == position {
			available -= reservation.Quantity
		}
	}
	return available
}

func (s State) Total(productID string) (int64, error) {
	var total int64
	for position, quantity := range s.holdings {
		if position.ProductID != productID {
			continue
		}
		next, ok := checkedAdd(total, quantity)
		if !ok {
			return 0, ErrQuantityOverflow
		}
		total = next
	}
	return total, nil
}

func (s State) Reserve(reservation Reservation) (State, error) {
	if reservation.ID == "" || reservation.OperationID == "" || reservation.Purpose == "" {
		return State{}, fmt.Errorf("%w: reservation metadata is required", ErrInvalidQuantity)
	}
	if err := validatePosition(reservation.Position); err != nil {
		return State{}, err
	}
	if reservation.Quantity <= 0 {
		return State{}, ErrInvalidQuantity
	}
	if _, exists := s.reservations[reservation.ID]; exists {
		return State{}, fmt.Errorf("%w: %q", ErrDuplicateReservation, reservation.ID)
	}
	if s.Available(reservation.Position) < reservation.Quantity {
		return State{}, fmt.Errorf("%w: position %#v", ErrInsufficientQuantity, reservation.Position)
	}
	next := s.clone()
	next.reservations[reservation.ID] = reservation
	return next, nil
}

func (s State) Release(reservationID string, quantity int64) (State, error) {
	reservation, exists := s.reservations[reservationID]
	if !exists {
		return State{}, fmt.Errorf("%w: %q", ErrUnknownReservation, reservationID)
	}
	if quantity <= 0 || quantity > reservation.Quantity {
		return State{}, ErrInvalidQuantity
	}
	next := s.clone()
	reservation.Quantity -= quantity
	if reservation.Quantity == 0 {
		delete(next.reservations, reservationID)
	} else {
		next.reservations[reservationID] = reservation
	}
	return next, nil
}

func (s State) Transfer(movement Movement) (State, error) {
	if err := validateMovement(movement); err != nil {
		return State{}, err
	}
	if movement.ReservationID != "" {
		return State{}, fmt.Errorf("%w: reserved movement must use SettleReservation", ErrInvalidQuantity)
	}
	if _, exists := s.movementIDs[movement.ID]; exists {
		return State{}, fmt.Errorf("%w: %q", ErrDuplicateMovement, movement.ID)
	}
	if s.Available(movement.From) < movement.Quantity {
		return State{}, fmt.Errorf("%w: position %#v", ErrInsufficientQuantity, movement.From)
	}
	return s.applyMovement(movement)
}

func (s State) SettleReservation(reservationID string, movement Movement) (State, error) {
	reservation, exists := s.reservations[reservationID]
	if !exists {
		return State{}, fmt.Errorf("%w: %q", ErrUnknownReservation, reservationID)
	}
	movement.ReservationID = reservationID
	if err := validateMovement(movement); err != nil {
		return State{}, err
	}
	if _, exists := s.movementIDs[movement.ID]; exists {
		return State{}, fmt.Errorf("%w: %q", ErrDuplicateMovement, movement.ID)
	}
	if movement.From != reservation.Position || movement.Quantity > reservation.Quantity {
		return State{}, fmt.Errorf("%w: movement exceeds or changes reservation", ErrInvalidQuantity)
	}
	if s.Quantity(movement.From) < movement.Quantity {
		return State{}, fmt.Errorf("%w: reserved position %#v", ErrInsufficientQuantity, movement.From)
	}

	next, err := s.applyMovement(movement)
	if err != nil {
		return State{}, err
	}
	reservation.Quantity -= movement.Quantity
	if reservation.Quantity == 0 {
		delete(next.reservations, reservationID)
	} else {
		next.reservations[reservationID] = reservation
	}
	return next, nil
}

func (s State) ApplyTransformation(transformation Transformation) (State, error) {
	if transformation.ID == "" || transformation.OperationID == "" || len(transformation.Inputs) == 0 || len(transformation.Outputs) == 0 {
		return State{}, fmt.Errorf("%w: transformation metadata, inputs, and outputs are required", ErrInvalidQuantity)
	}
	if _, exists := s.transformIDs[transformation.ID]; exists {
		return State{}, fmt.Errorf("%w: %q", ErrDuplicateTransform, transformation.ID)
	}

	inputs, err := aggregateItems(transformation.Inputs)
	if err != nil {
		return State{}, err
	}
	outputs, err := aggregateItems(transformation.Outputs)
	if err != nil {
		return State{}, err
	}
	for position, quantity := range inputs {
		if s.Available(position) < quantity {
			return State{}, fmt.Errorf("%w: production input %#v", ErrInsufficientQuantity, position)
		}
	}

	next := s.clone()
	for position, quantity := range inputs {
		next.holdings[position] -= quantity
	}
	for position, quantity := range outputs {
		updated, ok := checkedAdd(next.holdings[position], quantity)
		if !ok {
			return State{}, fmt.Errorf("%w: production output %#v", ErrQuantityOverflow, position)
		}
		next.holdings[position] = updated
	}
	next.transformations = append(next.transformations, cloneTransformation(transformation))
	next.transformIDs[transformation.ID] = struct{}{}
	return next, nil
}

func (s State) Holdings() []Holding {
	result := make([]Holding, 0, len(s.holdings))
	for position, quantity := range s.holdings {
		result = append(result, Holding{Position: position, Quantity: quantity})
	}
	sort.Slice(result, func(i, j int) bool {
		return lessPosition(result[i].Position, result[j].Position)
	})
	return result
}

func (s State) Reservations() []Reservation {
	result := make([]Reservation, 0, len(s.reservations))
	for _, reservation := range s.reservations {
		result = append(result, reservation)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].ID < result[j].ID
	})
	return result
}

func (s State) Movements() []Movement {
	return append([]Movement(nil), s.movements...)
}

func (s State) Transformations() []Transformation {
	result := make([]Transformation, len(s.transformations))
	for index, transformation := range s.transformations {
		result[index] = cloneTransformation(transformation)
	}
	return result
}

func (s State) applyMovement(movement Movement) (State, error) {
	next := s.clone()
	destination, ok := checkedAdd(next.holdings[movement.To], movement.Quantity)
	if !ok {
		return State{}, fmt.Errorf("%w: destination %#v", ErrQuantityOverflow, movement.To)
	}
	next.holdings[movement.From] -= movement.Quantity
	next.holdings[movement.To] = destination
	next.movements = append(next.movements, movement)
	next.movementIDs[movement.ID] = struct{}{}
	return next, nil
}

func (s State) clone() State {
	next := State{
		holdings:        make(map[Position]int64, len(s.holdings)),
		reservations:    make(map[string]Reservation, len(s.reservations)),
		movements:       append([]Movement(nil), s.movements...),
		movementIDs:     make(map[string]struct{}, len(s.movementIDs)),
		transformations: make([]Transformation, len(s.transformations)),
		transformIDs:    make(map[string]struct{}, len(s.transformIDs)),
	}
	for position, quantity := range s.holdings {
		next.holdings[position] = quantity
	}
	for id, reservation := range s.reservations {
		next.reservations[id] = reservation
	}
	for id := range s.movementIDs {
		next.movementIDs[id] = struct{}{}
	}
	for index, transformation := range s.transformations {
		next.transformations[index] = cloneTransformation(transformation)
	}
	for id := range s.transformIDs {
		next.transformIDs[id] = struct{}{}
	}
	return next
}

func validatePosition(position Position) error {
	if position.CompanyID == "" || position.LocationID == "" || position.ProductID == "" {
		return ErrInvalidPosition
	}
	return nil
}

func validateMovement(movement Movement) error {
	if movement.ID == "" || movement.OperationID == "" || movement.Quantity <= 0 {
		return fmt.Errorf("%w: movement metadata and positive quantity are required", ErrInvalidQuantity)
	}
	if err := validatePosition(movement.From); err != nil {
		return err
	}
	if err := validatePosition(movement.To); err != nil {
		return err
	}
	if movement.From == movement.To || movement.From.ProductID != movement.To.ProductID {
		return ErrConservation
	}
	return nil
}

func aggregateItems(items []Item) (map[Position]int64, error) {
	result := make(map[Position]int64, len(items))
	for _, item := range items {
		if err := validatePosition(item.Position); err != nil {
			return nil, err
		}
		if item.Quantity <= 0 {
			return nil, ErrInvalidQuantity
		}
		quantity, ok := checkedAdd(result[item.Position], item.Quantity)
		if !ok {
			return nil, ErrQuantityOverflow
		}
		result[item.Position] = quantity
	}
	return result, nil
}

func cloneTransformation(transformation Transformation) Transformation {
	transformation.Inputs = append([]Item(nil), transformation.Inputs...)
	transformation.Outputs = append([]Item(nil), transformation.Outputs...)
	return transformation
}

func lessPosition(left, right Position) bool {
	if left.CompanyID != right.CompanyID {
		return left.CompanyID < right.CompanyID
	}
	if left.LocationID != right.LocationID {
		return left.LocationID < right.LocationID
	}
	return left.ProductID < right.ProductID
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
