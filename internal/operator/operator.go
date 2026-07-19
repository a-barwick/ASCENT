// Package operator constructs explicit compensating economic records. It does
// not edit or delete committed journals.
package operator

import (
	"errors"
	"fmt"

	"ascent/internal/ledger"
)

var ErrInvalidCompensation = errors.New("invalid operator compensation")

type CompensationCommand struct {
	JournalID          string `json:"journalId"`
	CompensationID     string `json:"compensationId"`
	OperationID        string `json:"operationId"`
	IdempotencyKey     string `json:"idempotencyKey"`
	RequestFingerprint string `json:"requestFingerprint"`
	Reason             string `json:"reason"`
}

func BuildCompensation(command CompensationCommand, original ledger.Journal) (ledger.Journal, error) {
	if command.JournalID == "" || command.JournalID != original.ID() ||
		command.CompensationID == "" || command.OperationID == "" ||
		command.IdempotencyKey == "" || command.RequestFingerprint == "" ||
		command.Reason == "" {
		return ledger.Journal{}, ErrInvalidCompensation
	}
	compensation, err := ledger.Compensate(original, ledger.CompensationDraft{
		ID:                 command.CompensationID,
		OperationID:        command.OperationID,
		IdempotencyKey:     command.IdempotencyKey,
		RequestFingerprint: command.RequestFingerprint,
		Description:        fmt.Sprintf("operator compensation: %s", command.Reason),
	})
	if err != nil {
		return ledger.Journal{}, fmt.Errorf("%w: %v", ErrInvalidCompensation, err)
	}
	return compensation, nil
}
