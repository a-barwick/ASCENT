package postgres

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	protocol "ascent/protocol/gen/go"
)

func TestRequestHashIsStableAndDetectsChangedPayload(t *testing.T) {
	t.Parallel()

	companyID := "20000000-0000-4000-8000-000000000001"
	version := int64(7)
	base := protocol.CommandEnvelope{
		ActorId:         "10000000-0000-4000-8000-000000000001",
		CommandId:       "90000000-0000-4000-8000-000000000001",
		CompanyId:       &companyID,
		ExpectedVersion: &version,
		IdempotencyKey:  "retry-1",
		Payload:         json.RawMessage(`{"price":308.25,"quantity":40}`),
		ProtocolVersion: protocol.Version,
		ReceivedAt:      time.Now(),
		Type:            "market.place_order",
	}
	retry := base
	retry.CommandId = "90000000-0000-4000-8000-000000000002"
	retry.ReceivedAt = retry.ReceivedAt.Add(time.Minute)

	if RequestHash(base) != RequestHash(retry) {
		t.Fatal("transport retry metadata changed the request hash")
	}
	retry.Payload = json.RawMessage(`{"price":309.25,"quantity":40}`)
	if RequestHash(base) == RequestHash(retry) {
		t.Fatal("changed payload reused the request hash")
	}
}

func TestStartCommandRejectsInvalidEnvelopeBeforeDatabaseAccess(t *testing.T) {
	t.Parallel()

	_, err := StartCommand(t.Context(), nil, "", protocol.CommandEnvelope{}, time.Now())
	if !errors.Is(err, ErrInvalidCommand) {
		t.Fatalf("error = %v", err)
	}
}
