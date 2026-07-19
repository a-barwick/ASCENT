// Package production models deterministic, due facility jobs. It produces an
// inventory transformation for the caller to commit atomically with job state.
package production

import (
	"errors"
	"fmt"
	"math"
	"sort"

	"ascent/internal/inventory"
)

var (
	ErrInvalidRecipe       = errors.New("invalid production recipe")
	ErrInvalidFacility     = errors.New("invalid production facility")
	ErrInvalidJob          = errors.New("invalid production job")
	ErrUnknownJob          = errors.New("unknown production job")
	ErrNotDue              = errors.New("production job is not due")
	ErrFacilityOwnership   = errors.New("production facility belongs to another company")
	ErrIdempotencyConflict = errors.New("idempotency key reused with changed production request")
	ErrQuantityOverflow    = errors.New("production quantity overflow")
)

type Recipe struct {
	ID              string `json:"id"`
	InputProductID  string `json:"inputProductId"`
	InputQuantity   int64  `json:"inputQuantity"`
	OutputProductID string `json:"outputProductId"`
	OutputQuantity  int64  `json:"outputQuantity"`
}

type Facility struct {
	ID         string `json:"id"`
	CompanyID  string `json:"companyId"`
	LocationID string `json:"locationId"`
	RecipeID   string `json:"recipeId"`
}

type JobStatus string

const (
	JobScheduled JobStatus = "scheduled"
	JobCompleted JobStatus = "completed"
)

type Job struct {
	ID          string    `json:"id"`
	FacilityID  string    `json:"facilityId"`
	Runs        int64     `json:"runs"`
	DueTick     int64     `json:"dueTick"`
	Status      JobStatus `json:"status"`
	ExecutionID string    `json:"executionId,omitempty"`
}

type ExecuteCommand struct {
	JobID              string
	CompanyID          string
	AtTick             int64
	ExecutionID        string
	OperationID        string
	IdempotencyKey     string
	RequestFingerprint string
}

type Execution struct {
	ID             string                   `json:"id"`
	OperationID    string                   `json:"operationId"`
	JobID          string                   `json:"jobId"`
	FacilityID     string                   `json:"facilityId"`
	CompletedTick  int64                    `json:"completedTick"`
	Transformation inventory.Transformation `json:"transformation"`
	Replay         bool                     `json:"replay"`
}

type receipt struct {
	fingerprint string
	execution   Execution
}

type State struct {
	facilities map[string]Facility
	recipes    map[string]Recipe
	jobs       map[string]Job
	executions map[string]Execution
	receipts   map[string]receipt
}

func NewState(facilities []Facility, recipes []Recipe, jobs []Job) (State, error) {
	state := State{
		facilities: make(map[string]Facility, len(facilities)),
		recipes:    make(map[string]Recipe, len(recipes)),
		jobs:       make(map[string]Job, len(jobs)),
		executions: make(map[string]Execution),
		receipts:   make(map[string]receipt),
	}
	for _, recipe := range recipes {
		if recipe.ID == "" || recipe.InputProductID == "" || recipe.OutputProductID == "" ||
			recipe.InputProductID == recipe.OutputProductID ||
			recipe.InputQuantity <= 0 || recipe.OutputQuantity <= 0 {
			return State{}, ErrInvalidRecipe
		}
		if _, exists := state.recipes[recipe.ID]; exists {
			return State{}, fmt.Errorf("%w: duplicate %q", ErrInvalidRecipe, recipe.ID)
		}
		state.recipes[recipe.ID] = recipe
	}
	for _, facility := range facilities {
		if facility.ID == "" || facility.CompanyID == "" || facility.LocationID == "" || facility.RecipeID == "" {
			return State{}, ErrInvalidFacility
		}
		if _, exists := state.recipes[facility.RecipeID]; !exists {
			return State{}, fmt.Errorf("%w: unknown recipe %q", ErrInvalidFacility, facility.RecipeID)
		}
		if _, exists := state.facilities[facility.ID]; exists {
			return State{}, fmt.Errorf("%w: duplicate %q", ErrInvalidFacility, facility.ID)
		}
		state.facilities[facility.ID] = facility
	}
	for _, job := range jobs {
		if job.ID == "" || job.FacilityID == "" || job.Runs <= 0 || job.DueTick < 0 || job.Status != JobScheduled {
			return State{}, ErrInvalidJob
		}
		if _, exists := state.facilities[job.FacilityID]; !exists {
			return State{}, fmt.Errorf("%w: unknown facility %q", ErrInvalidJob, job.FacilityID)
		}
		if _, exists := state.jobs[job.ID]; exists {
			return State{}, fmt.Errorf("%w: duplicate %q", ErrInvalidJob, job.ID)
		}
		state.jobs[job.ID] = job
	}
	return state, nil
}

func (s State) ExecuteDue(command ExecuteCommand) (State, Execution, error) {
	if command.JobID == "" || command.CompanyID == "" || command.ExecutionID == "" ||
		command.OperationID == "" || command.IdempotencyKey == "" || command.RequestFingerprint == "" {
		return State{}, Execution{}, ErrInvalidJob
	}
	receiptKey := command.CompanyID + "\x00" + command.IdempotencyKey
	if previous, exists := s.receipts[receiptKey]; exists {
		if previous.fingerprint != command.RequestFingerprint {
			return State{}, Execution{}, ErrIdempotencyConflict
		}
		replay := previous.execution
		replay.Replay = true
		return s, replay, nil
	}

	job, exists := s.jobs[command.JobID]
	if !exists {
		return State{}, Execution{}, fmt.Errorf("%w: %q", ErrUnknownJob, command.JobID)
	}
	facility := s.facilities[job.FacilityID]
	if facility.CompanyID != command.CompanyID {
		return State{}, Execution{}, ErrFacilityOwnership
	}
	if job.Status == JobCompleted {
		execution := s.executions[job.ExecutionID]
		execution.Replay = true
		return s, execution, nil
	}
	if command.AtTick < job.DueTick {
		return State{}, Execution{}, ErrNotDue
	}

	recipe := s.recipes[facility.RecipeID]
	inputQuantity, ok := checkedMultiply(recipe.InputQuantity, job.Runs)
	if !ok {
		return State{}, Execution{}, ErrQuantityOverflow
	}
	outputQuantity, ok := checkedMultiply(recipe.OutputQuantity, job.Runs)
	if !ok {
		return State{}, Execution{}, ErrQuantityOverflow
	}
	execution := Execution{
		ID:            command.ExecutionID,
		OperationID:   command.OperationID,
		JobID:         job.ID,
		FacilityID:    facility.ID,
		CompletedTick: command.AtTick,
		Transformation: inventory.Transformation{
			ID:          command.ExecutionID + "-inventory",
			OperationID: command.OperationID,
			Inputs: []inventory.Item{{
				Position: inventory.Position{
					CompanyID:  facility.CompanyID,
					LocationID: facility.LocationID,
					ProductID:  recipe.InputProductID,
				},
				Quantity: inputQuantity,
			}},
			Outputs: []inventory.Item{{
				Position: inventory.Position{
					CompanyID:  facility.CompanyID,
					LocationID: facility.LocationID,
					ProductID:  recipe.OutputProductID,
				},
				Quantity: outputQuantity,
			}},
		},
	}

	next := s.clone()
	job.Status = JobCompleted
	job.ExecutionID = execution.ID
	next.jobs[job.ID] = job
	next.executions[execution.ID] = execution
	next.receipts[receiptKey] = receipt{
		fingerprint: command.RequestFingerprint,
		execution:   execution,
	}
	return next, execution, nil
}

func (s State) Facilities() []Facility {
	result := make([]Facility, 0, len(s.facilities))
	for _, facility := range s.facilities {
		result = append(result, facility)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

func (s State) Jobs() []Job {
	result := make([]Job, 0, len(s.jobs))
	for _, job := range s.jobs {
		result = append(result, job)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

func (s State) Executions() []Execution {
	result := make([]Execution, 0, len(s.executions))
	for _, execution := range s.executions {
		result = append(result, execution)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

func (s State) clone() State {
	next := State{
		facilities: make(map[string]Facility, len(s.facilities)),
		recipes:    make(map[string]Recipe, len(s.recipes)),
		jobs:       make(map[string]Job, len(s.jobs)),
		executions: make(map[string]Execution, len(s.executions)),
		receipts:   make(map[string]receipt, len(s.receipts)),
	}
	for id, facility := range s.facilities {
		next.facilities[id] = facility
	}
	for id, recipe := range s.recipes {
		next.recipes[id] = recipe
	}
	for id, job := range s.jobs {
		next.jobs[id] = job
	}
	for id, execution := range s.executions {
		next.executions[id] = execution
	}
	for key, value := range s.receipts {
		next.receipts[key] = value
	}
	return next
}

func checkedMultiply(left, right int64) (int64, bool) {
	if left <= 0 || right <= 0 {
		return 0, false
	}
	if left > math.MaxInt64/right {
		return 0, false
	}
	return left * right, true
}
