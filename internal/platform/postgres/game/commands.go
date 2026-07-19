package gamepostgres

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"ascent/internal/identity"
	"ascent/internal/platform/ids"
	platformpostgres "ascent/internal/platform/postgres"
	protocol "ascent/protocol/gen/go"
	"github.com/jackc/pgx/v5/pgconn"
)

type commandRejection struct {
	code        string
	safeMessage string
}

type commandOutcome struct {
	payload any
}

type commandHandler func(
	context.Context,
	*sql.Tx,
	identity.Actor,
	protocol.CommandEnvelope,
) (commandOutcome, *commandRejection, error)

func (s *Service) Execute(
	ctx context.Context,
	actor identity.Actor,
	command protocol.CommandEnvelope,
) (protocol.CommandResultEnvelope, error) {
	const maxAttempts = 3
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		result, err := s.executeOnce(ctx, actor, command)
		if err == nil {
			return result, nil
		}
		lastErr = err
		if !retryableTransactionError(err) || ctx.Err() != nil {
			return protocol.CommandResultEnvelope{}, err
		}
	}
	return protocol.CommandResultEnvelope{}, fmt.Errorf(
		"command transaction exhausted retries: %w",
		lastErr,
	)
}

func (s *Service) executeOnce(
	ctx context.Context,
	actor identity.Actor,
	command protocol.CommandEnvelope,
) (protocol.CommandResultEnvelope, error) {
	if command.ProtocolVersion != protocol.Version {
		return rejectedResult(command.CommandId, "PROTOCOL_VERSION_MISMATCH", "Refresh the terminal before retrying."), nil
	}
	handler := s.handler(command.Type)
	if handler == nil {
		return s.recordRejection(ctx, actor, command, commandRejection{
			code:        "UNKNOWN_COMMAND",
			safeMessage: "This command type is not supported by the current server.",
		})
	}

	transaction, err := s.database.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return protocol.CommandResultEnvelope{}, fmt.Errorf("begin command transaction: %w", err)
	}
	defer transaction.Rollback()

	start, err := platformpostgres.StartCommand(ctx, transaction, actor.ID, command, s.clock())
	if errors.Is(err, platformpostgres.ErrIdempotencyConflict) {
		return rejectedResult(command.CommandId, "IDEMPOTENCY_CONFLICT", "That retry key already belongs to a different command."), nil
	}
	if err != nil {
		return protocol.CommandResultEnvelope{}, err
	}
	if start.Existing != nil {
		return *start.Existing, nil
	}

	outcome, rejection, err := handler(ctx, transaction, actor, command)
	if err != nil {
		return protocol.CommandResultEnvelope{}, err
	}
	if rejection != nil {
		if err := transaction.Rollback(); err != nil {
			return protocol.CommandResultEnvelope{}, fmt.Errorf("roll back rejected command: %w", err)
		}
		return s.recordRejection(ctx, actor, command, *rejection)
	}

	payload, err := json.Marshal(outcome.payload)
	if err != nil {
		return protocol.CommandResultEnvelope{}, fmt.Errorf("encode command payload: %w", err)
	}
	committedAt := s.clock()
	result := protocol.CommandResultEnvelope{
		CommandId:       command.CommandId,
		CommittedAt:     &committedAt,
		Payload:         payload,
		ProtocolVersion: protocol.Version,
		Status:          protocol.CommandResultStatusCommitted,
	}
	if err := platformpostgres.CompleteCommand(ctx, transaction, result, nil); err != nil {
		return protocol.CommandResultEnvelope{}, err
	}
	if err := transaction.Commit(); err != nil {
		return protocol.CommandResultEnvelope{}, fmt.Errorf("commit command: %w", err)
	}
	return result, nil
}

func retryableTransactionError(err error) bool {
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) {
		return false
	}
	switch postgresError.Code {
	case "40001", "40P01":
		return true
	case "23505":
		return postgresError.ConstraintName == "command_log_actor_idempotency_unique"
	default:
		return false
	}
}

func (s *Service) handler(commandType string) commandHandler {
	switch commandType {
	case "market.place_order":
		return s.placeOrder
	case "market.cancel_order":
		return s.cancelOrder
	case "production.run":
		return s.runProduction
	case "freight.deliver":
		return s.deliverFreight
	case "device.register":
		return s.registerDevice
	case "device.panel_send":
		return s.sendPanel
	case "chat.send":
		return s.sendChat
	case "operator.compensate":
		return s.compensate
	default:
		return nil
	}
}

func (s *Service) recordRejection(
	ctx context.Context,
	actor identity.Actor,
	command protocol.CommandEnvelope,
	rejection commandRejection,
) (protocol.CommandResultEnvelope, error) {
	result := rejectedResult(command.CommandId, rejection.code, rejection.safeMessage)
	transaction, err := s.database.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return protocol.CommandResultEnvelope{}, fmt.Errorf("begin rejection transaction: %w", err)
	}
	defer transaction.Rollback()
	start, err := platformpostgres.StartCommand(ctx, transaction, actor.ID, command, s.clock())
	if errors.Is(err, platformpostgres.ErrIdempotencyConflict) {
		return rejectedResult(command.CommandId, "IDEMPOTENCY_CONFLICT", "That retry key already belongs to a different command."), nil
	}
	if err != nil {
		return protocol.CommandResultEnvelope{}, err
	}
	if start.Existing != nil {
		return *start.Existing, nil
	}
	if err := platformpostgres.CompleteCommand(ctx, transaction, result, &rejection.code); err != nil {
		return protocol.CommandResultEnvelope{}, err
	}
	if err := transaction.Commit(); err != nil {
		return protocol.CommandResultEnvelope{}, fmt.Errorf("commit command rejection: %w", err)
	}
	return result, nil
}

func rejectedResult(commandID, code, safeMessage string) protocol.CommandResultEnvelope {
	return protocol.CommandResultEnvelope{
		CommandId:       commandID,
		CommittedAt:     nil,
		ErrorCode:       &code,
		Payload:         json.RawMessage(`{}`),
		ProtocolVersion: protocol.Version,
		SafeMessage:     &safeMessage,
		Status:          protocol.CommandResultStatusRejected,
	}
}

func (s *Service) authorizeCompany(
	ctx context.Context,
	transaction *sql.Tx,
	actor identity.Actor,
	command protocol.CommandEnvelope,
	roles ...string,
) (string, int64, *commandRejection, error) {
	if command.CompanyId == nil || !ids.IsUUID(*command.CompanyId) || !ids.IsUUID(actor.ID) {
		return "", 0, reject("INVALID_COMPANY", "Choose a valid operating company."), nil
	}
	var (
		role    string
		version int64
	)
	err := transaction.QueryRowContext(
		ctx,
		`SELECT membership.role, company.version
		   FROM companies.memberships AS membership
		   JOIN companies.companies AS company
		     ON company.company_id = membership.company_id
		  WHERE membership.player_id = $1
		    AND membership.company_id = $2
		    AND membership.left_at IS NULL
		    AND company.status = 'active'
		  FOR SHARE OF membership
		  FOR UPDATE OF company`,
		actor.ID,
		*command.CompanyId,
	).Scan(&role, &version)
	if errors.Is(err, sql.ErrNoRows) {
		return "", 0, reject("FORBIDDEN", "Your player does not have authority in this company."), nil
	}
	if err != nil {
		return "", 0, nil, fmt.Errorf("resolve company authority: %w", err)
	}
	if !slices.Contains(roles, role) {
		return "", 0, reject("FORBIDDEN", "Your company role does not permit this command."), nil
	}
	if command.ExpectedVersion != nil && *command.ExpectedVersion != version {
		return "", 0, reject("VERSION_CONFLICT", "Company state changed; refresh before retrying."), nil
	}
	return role, version, nil, nil
}

func incrementCompanyVersion(
	ctx context.Context,
	transaction *sql.Tx,
	companyID string,
	version int64,
) error {
	result, err := transaction.ExecContext(
		ctx,
		`UPDATE companies.companies
		    SET version = version + 1,
		        updated_at = clock_timestamp()
		  WHERE company_id = $1 AND version = $2`,
		companyID,
		version,
	)
	if err != nil {
		return fmt.Errorf("advance company version: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("inspect company version update: %w", err)
	}
	if affected != 1 {
		return errors.New("company version changed during command")
	}
	return nil
}

func appendEvent(
	ctx context.Context,
	transaction *sql.Tx,
	commandID *string,
	topic string,
	eventType string,
	payload any,
	occurredAt time.Time,
) (protocol.EventEnvelope, error) {
	eventID, err := ids.NewUUID()
	if err != nil {
		return protocol.EventEnvelope{}, err
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return protocol.EventEnvelope{}, fmt.Errorf("encode event payload: %w", err)
	}
	var sequence, topicSequence int64
	err = transaction.QueryRowContext(
		ctx,
		`INSERT INTO platform.event_outbox (
			event_id,
			protocol_version,
			command_id,
			topic,
			event_type,
			occurred_at,
			payload
		) VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING sequence, topic_sequence`,
		eventID,
		protocol.Version,
		commandID,
		topic,
		eventType,
		occurredAt,
		encoded,
	).Scan(&sequence, &topicSequence)
	if err != nil {
		return protocol.EventEnvelope{}, fmt.Errorf("append committed event: %w", err)
	}
	return protocol.EventEnvelope{
		CommandId:       commandID,
		EventId:         &eventID,
		OccurredAt:      occurredAt,
		Payload:         encoded,
		ProtocolVersion: protocol.Version,
		Sequence:        sequence,
		Topic:           topic,
		TopicSequence:   &topicSequence,
		Type:            eventType,
	}, nil
}

func decodePayload(raw json.RawMessage, destination any) *commandRejection {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	if err := decoder.Decode(destination); err != nil {
		return reject("INVALID_PAYLOAD", "The command fields are malformed.")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return reject("INVALID_PAYLOAD", "The command must contain one payload object.")
	}
	return nil
}

func minorUnits(value float64, scale int64) (int64, bool) {
	if math.IsNaN(value) || math.IsInf(value, 0) || value <= 0 || scale <= 0 {
		return 0, false
	}
	scaled := value * float64(scale)
	if scaled > math.MaxInt64 || scaled < 1 {
		return 0, false
	}
	rounded := math.Round(scaled)
	if math.Abs(scaled-rounded) > 0.000001 {
		return 0, false
	}
	return int64(rounded), true
}

func decimalMinorUnits(value json.Number, scale int64) (int64, bool) {
	raw := strings.TrimSpace(value.String())
	if raw == "" || scale <= 0 || strings.HasPrefix(raw, "-") ||
		strings.HasPrefix(raw, "+") || strings.ContainsAny(raw, "eE") {
		return 0, false
	}
	parts := strings.Split(raw, ".")
	if len(parts) > 2 || parts[0] == "" {
		return 0, false
	}
	whole, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || whole < 0 {
		return 0, false
	}
	fraction := ""
	if len(parts) == 2 {
		fraction = parts[1]
		if fraction == "" {
			return 0, false
		}
	}
	precision := 0
	for divisor := scale; divisor > 1; divisor /= 10 {
		if divisor%10 != 0 {
			return 0, false
		}
		precision++
	}
	if powerOfTen(precision) != scale || len(fraction) > precision {
		return 0, false
	}
	for len(fraction) < precision {
		fraction += "0"
	}
	fractionMinor := int64(0)
	if fraction != "" {
		fractionMinor, err = strconv.ParseInt(fraction, 10, 64)
		if err != nil || fractionMinor < 0 || fractionMinor >= scale {
			return 0, false
		}
	}
	if whole > (math.MaxInt64-fractionMinor)/scale {
		return 0, false
	}
	result := whole*scale + fractionMinor
	return result, result > 0
}

func reject(code, safeMessage string) *commandRejection {
	return &commandRejection{code: code, safeMessage: safeMessage}
}

func jsonPayload(value any) json.RawMessage {
	encoded, err := json.Marshal(value)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return encoded
}
