package production

import (
	"errors"
	"testing"
)

func TestDueJobExecutesOnceAndChangedRetryConflicts(t *testing.T) {
	state := testState(t)
	command := ExecuteCommand{
		JobID:              "job-1",
		CompanyID:          "company-a",
		AtTick:             10,
		ExecutionID:        "execution-1",
		OperationID:        "operation-1",
		IdempotencyKey:     "retry-1",
		RequestFingerprint: "fingerprint-1",
	}
	next, execution, err := state.ExecuteDue(command)
	if err != nil {
		t.Fatal(err)
	}
	if execution.Transformation.Inputs[0].Quantity != 6 || execution.Transformation.Outputs[0].Quantity != 4 {
		t.Fatalf("unexpected recipe quantities: %#v", execution.Transformation)
	}

	retried, replay, err := next.ExecuteDue(command)
	if err != nil {
		t.Fatal(err)
	}
	if !replay.Replay || replay.ID != execution.ID {
		t.Fatalf("unexpected replay: %#v", replay)
	}
	if len(retried.Executions()) != 1 {
		t.Fatalf("duplicate execution count = %d", len(retried.Executions()))
	}

	changed := command
	changed.RequestFingerprint = "changed"
	if _, _, err := next.ExecuteDue(changed); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("expected idempotency conflict, got %v", err)
	}
}

func TestJobCannotRunBeforeDueTick(t *testing.T) {
	state := testState(t)
	if _, _, err := state.ExecuteDue(ExecuteCommand{
		JobID:              "job-1",
		CompanyID:          "company-a",
		AtTick:             9,
		ExecutionID:        "execution-1",
		OperationID:        "operation-1",
		IdempotencyKey:     "retry-1",
		RequestFingerprint: "fingerprint-1",
	}); !errors.Is(err, ErrNotDue) {
		t.Fatalf("expected not-due error, got %v", err)
	}
}

func testState(t *testing.T) State {
	t.Helper()
	state, err := NewState(
		[]Facility{{
			ID: "facility-1", CompanyID: "company-a", LocationID: "moon", RecipeID: "recipe-1",
		}},
		[]Recipe{{
			ID: "recipe-1", InputProductID: "ice", InputQuantity: 3,
			OutputProductID: "water", OutputQuantity: 2,
		}},
		[]Job{{
			ID: "job-1", FacilityID: "facility-1", Runs: 2, DueTick: 10, Status: JobScheduled,
		}},
	)
	if err != nil {
		t.Fatal(err)
	}
	return state
}
