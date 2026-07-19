package postgres

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"ascent/internal/platform/ids"
	protocol "ascent/protocol/gen/go"
)

var (
	ErrInvalidCommand      = errors.New("command envelope is invalid")
	ErrIdempotencyConflict = errors.New("idempotency key was reused for a different request")
)

type CommandStart struct {
	Existing *protocol.CommandResultEnvelope
	Hash     [sha256.Size]byte
}

// StartCommand locks or creates an actor-scoped idempotency record inside the
// caller's domain transaction. A returned Existing result must be returned
// verbatim without applying domain work again.
func StartCommand(
	ctx context.Context,
	transaction *sql.Tx,
	actorID string,
	command protocol.CommandEnvelope,
	receivedAt time.Time,
) (CommandStart, error) {
	if transaction == nil || actorID == "" || actorID != command.ActorId ||
		!ids.IsUUID(command.CommandId) || strings.TrimSpace(command.IdempotencyKey) == "" ||
		len(command.IdempotencyKey) > 200 || strings.TrimSpace(command.Type) == "" ||
		len(command.Type) > 120 || !json.Valid(command.Payload) {
		return CommandStart{}, ErrInvalidCommand
	}
	hash := RequestHash(command)

	var (
		storedHash []byte
		storedJSON []byte
	)
	err := transaction.QueryRowContext(
		ctx,
		`SELECT request_hash, result
		   FROM platform.command_log
		  WHERE actor_id = $1 AND idempotency_key = $2
		  FOR UPDATE`,
		actorID,
		command.IdempotencyKey,
	).Scan(&storedHash, &storedJSON)
	switch {
	case err == nil:
		if !bytes.Equal(storedHash, hash[:]) {
			return CommandStart{}, ErrIdempotencyConflict
		}
		if len(storedJSON) == 0 {
			return CommandStart{}, errors.New("idempotent command has no stable result")
		}
		var result protocol.CommandResultEnvelope
		if err := json.Unmarshal(storedJSON, &result); err != nil {
			return CommandStart{}, fmt.Errorf("decode idempotent result: %w", err)
		}
		return CommandStart{Existing: &result, Hash: hash}, nil
	case !errors.Is(err, sql.ErrNoRows):
		return CommandStart{}, fmt.Errorf("lookup idempotency record: %w", err)
	}

	_, err = transaction.ExecContext(
		ctx,
		`INSERT INTO platform.command_log (
			command_id,
			protocol_version,
			idempotency_key,
			command_type,
			actor_id,
			company_id,
			expected_version,
			status,
			payload,
			request_hash,
			received_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, 'accepted', $8, $9, $10)`,
		command.CommandId,
		command.ProtocolVersion,
		command.IdempotencyKey,
		command.Type,
		actorID,
		command.CompanyId,
		command.ExpectedVersion,
		command.Payload,
		hash[:],
		receivedAt,
	)
	if err != nil {
		return CommandStart{}, fmt.Errorf("insert command record: %w", err)
	}
	return CommandStart{Hash: hash}, nil
}

func CompleteCommand(
	ctx context.Context,
	transaction *sql.Tx,
	result protocol.CommandResultEnvelope,
	errorCode *string,
) error {
	if transaction == nil || !ids.IsUUID(result.CommandId) {
		return ErrInvalidCommand
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("encode command result: %w", err)
	}
	var committedAt any
	if result.CommittedAt != nil {
		committedAt = *result.CommittedAt
	}
	tag, err := transaction.ExecContext(
		ctx,
		`UPDATE platform.command_log
		    SET status = $2,
		        result = $3,
		        error_code = $4,
		        committed_at = $5
		  WHERE command_id = $1`,
		result.CommandId,
		result.Status,
		encoded,
		errorCode,
		committedAt,
	)
	if err != nil {
		return fmt.Errorf("complete command record: %w", err)
	}
	affected, err := tag.RowsAffected()
	if err != nil {
		return fmt.Errorf("inspect command completion: %w", err)
	}
	if affected != 1 {
		return errors.New("command record was not completed")
	}
	return nil
}

func RequestHash(command protocol.CommandEnvelope) [sha256.Size]byte {
	hasher := sha256.New()
	writeHashField(hasher, command.ProtocolVersion)
	writeHashField(hasher, command.Type)
	if command.CompanyId != nil {
		writeHashField(hasher, *command.CompanyId)
	} else {
		writeHashField(hasher, "")
	}
	if command.ExpectedVersion != nil {
		var version [8]byte
		binary.BigEndian.PutUint64(version[:], uint64(*command.ExpectedVersion))
		_, _ = hasher.Write(version[:])
	} else {
		_, _ = hasher.Write(make([]byte, 8))
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, command.Payload); err == nil {
		_, _ = hasher.Write(compact.Bytes())
	} else {
		_, _ = hasher.Write(command.Payload)
	}
	var result [sha256.Size]byte
	copy(result[:], hasher.Sum(nil))
	return result
}

type hashWriter interface {
	Write([]byte) (int, error)
}

func writeHashField(writer hashWriter, value string) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(value)))
	_, _ = writer.Write(length[:])
	_, _ = writer.Write([]byte(value))
}
