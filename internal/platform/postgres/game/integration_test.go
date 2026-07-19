package gamepostgres

import (
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"reflect"
	"testing"
	"time"

	"ascent/internal/fixtures"
	"ascent/internal/identity"
	protocol "ascent/protocol/gen/go"
	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestSeedSnapshotAndEventsIntegration(t *testing.T) {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		t.Skip("DATABASE_URL is not set")
	}
	database, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := database.PingContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	service := New(
		database,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		func() time.Time { return fixtures.GeneratedAt },
	)
	if err := service.Seed(t.Context()); err != nil {
		t.Fatal(err)
	}
	var firstEventCount, firstMaxSequence int64
	if err := database.QueryRowContext(t.Context(), `
		SELECT count(*), COALESCE(max(sequence), 0)
		FROM platform.event_outbox
		WHERE event_id::text LIKE '84000000-%'`).Scan(&firstEventCount, &firstMaxSequence); err != nil {
		t.Fatal(err)
	}
	if err := service.Seed(t.Context()); err != nil {
		t.Fatal(err)
	}
	var secondEventCount, secondMaxSequence int64
	if err := database.QueryRowContext(t.Context(), `
		SELECT count(*), COALESCE(max(sequence), 0)
		FROM platform.event_outbox
		WHERE event_id::text LIKE '84000000-%'`).Scan(&secondEventCount, &secondMaxSequence); err != nil {
		t.Fatal(err)
	}
	if firstEventCount != secondEventCount || firstMaxSequence != secondMaxSequence {
		t.Fatalf("repeat seed changed events: first %d/%d second %d/%d",
			firstEventCount, firstMaxSequence, secondEventCount, secondMaxSequence)
	}

	var playerCount int
	if err := database.QueryRowContext(t.Context(), `
		SELECT count(*) FROM identity.players WHERE provider_subject LIKE 'seed-player-%'`).Scan(&playerCount); err != nil {
		t.Fatal(err)
	}
	if playerCount != fixtures.ScenarioCompanyCount {
		t.Fatalf("seed player count = %d, want %d", playerCount, fixtures.ScenarioCompanyCount)
	}
	var requiredAccountCount int
	if err := database.QueryRowContext(t.Context(), `
		SELECT count(*)
		FROM ledger.accounts
		WHERE company_id = $1::uuid
		  AND code IN ('CASH', 'INVENTORY', 'SALES', 'COGS', 'PRODUCTION_WIP')`, SeededCompanyID).Scan(&requiredAccountCount); err != nil {
		t.Fatal(err)
	}
	if requiredAccountCount != 5 {
		t.Fatalf("required seeded account count = %d", requiredAccountCount)
	}
	tradeCommandID := scenarioID(fixtures.NamespaceCommand, 21)
	var tradeJournalCount, tradeMovementCount, tradeEventCount int
	if err := database.QueryRowContext(t.Context(), `
		SELECT
			(SELECT count(*) FROM ledger.journals WHERE command_id = $1::uuid),
			(SELECT count(*) FROM inventory.movements WHERE command_id = $1::uuid),
			(SELECT count(*) FROM platform.event_outbox WHERE command_id = $1::uuid)`, tradeCommandID).Scan(
		&tradeJournalCount,
		&tradeMovementCount,
		&tradeEventCount,
	); err != nil {
		t.Fatal(err)
	}
	if tradeJournalCount != 2 || tradeMovementCount != 1 || tradeEventCount != 1 {
		t.Fatalf("seeded trade trace = journals %d movements %d events %d", tradeJournalCount, tradeMovementCount, tradeEventCount)
	}
	var carrierID string
	if err := database.QueryRowContext(t.Context(), `
		SELECT carrier_company_id::text
		FROM freight.contracts
		WHERE contract_id = $1::uuid`, scenarioID(fixtures.NamespaceFreight, 2)).Scan(&carrierID); err != nil {
		t.Fatal(err)
	}
	if carrierID != SeededCompanyID {
		t.Fatalf("freight carrier = %q, want %q", carrierID, SeededCompanyID)
	}

	actor := identity.Actor{ID: SeededActorID, DisplayName: SeededDisplayName}
	envelope, err := service.Snapshot(t.Context(), actor)
	if err != nil {
		t.Fatal(err)
	}
	if envelope.ProtocolVersion != protocol.Version || envelope.Sequence <= 0 {
		t.Fatalf("snapshot envelope = %#v", envelope)
	}
	var snapshot GameSnapshot
	if err := json.Unmarshal(envelope.Payload, &snapshot); err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Markets) < 2 || len(snapshot.OpenOrders) == 0 || len(snapshot.Trades) == 0 || len(snapshot.Inventory) < 2 {
		t.Fatalf("snapshot market/economy slices are incomplete: markets %d orders %d trades %d inventory %d",
			len(snapshot.Markets), len(snapshot.OpenOrders), len(snapshot.Trades), len(snapshot.Inventory))
	}
	for _, market := range snapshot.Markets[:2] {
		if len(market.History) < 2 || market.LastPrice <= 0 || market.Volume24Hour <= 0 || market.Spread <= 0 {
			t.Fatalf("market projection is incomplete: %#v", market)
		}
	}
	if len(snapshot.Facilities) == 0 || len(snapshot.Freight) == 0 || len(snapshot.Devices) == 0 ||
		len(snapshot.Chat) == 0 || len(snapshot.Alerts) == 0 || len(snapshot.OperatorAudit) == 0 {
		t.Fatalf("snapshot operational slices are incomplete")
	}
	companyID := SeededCompanyID
	productionCommand := protocol.CommandEnvelope{
		ActorId:         actor.ID,
		CommandId:       scenarioID(fixtures.NamespaceCommand, 9001),
		CompanyId:       &companyID,
		IdempotencyKey:  "integration-production-run",
		Payload:         json.RawMessage(`{"facilityId":"` + scenarioID(fixtures.NamespaceFacility, 1) + `","quantity":1}`),
		ProtocolVersion: protocol.Version,
		ReceivedAt:      fixtures.GeneratedAt,
		Type:            "production.run",
	}
	productionResult, err := service.Execute(t.Context(), actor, productionCommand)
	if err != nil {
		t.Fatal(err)
	}
	if productionResult.Status != protocol.CommandResultStatusCommitted {
		t.Fatalf("production result = %#v", productionResult)
	}
	retriedProduction, err := service.Execute(t.Context(), actor, productionCommand)
	if err != nil {
		t.Fatal(err)
	}
	var productionPayload, retriedPayload map[string]any
	if err := json.Unmarshal(productionResult.Payload, &productionPayload); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(retriedProduction.Payload, &retriedPayload); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(retriedPayload, productionPayload) {
		t.Fatalf("production retry payload changed: %#v != %#v", retriedPayload, productionPayload)
	}
	freightCommand := protocol.CommandEnvelope{
		ActorId:         actor.ID,
		CommandId:       scenarioID(fixtures.NamespaceCommand, 9002),
		CompanyId:       &companyID,
		IdempotencyKey:  "integration-freight-delivery",
		Payload:         json.RawMessage(`{"shipmentId":"` + scenarioID(fixtures.NamespaceFreight, 3) + `"}`),
		ProtocolVersion: protocol.Version,
		ReceivedAt:      fixtures.GeneratedAt,
		Type:            "freight.deliver",
	}
	freightResult, err := service.Execute(t.Context(), actor, freightCommand)
	if err != nil {
		t.Fatal(err)
	}
	if freightResult.Status != protocol.CommandResultStatusCommitted {
		t.Fatalf("freight result = %#v", freightResult)
	}

	events, err := service.Events(t.Context(), actor, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) == 0 {
		t.Fatal("seeded actor received no events")
	}
	lastTopicSequence := make(map[string]int64)
	sawCommandID := false
	for _, event := range events {
		if event.EventId == nil || event.TopicSequence == nil || event.ProtocolVersion != protocol.Version {
			t.Fatalf("event lacks protocol 1.1 identity fields: %#v", event)
		}
		if event.CommandId != nil {
			sawCommandID = true
		}
		if previous := lastTopicSequence[event.Topic]; *event.TopicSequence <= previous {
			t.Fatalf("topic %q sequence moved from %d to %d", event.Topic, previous, *event.TopicSequence)
		}
		lastTopicSequence[event.Topic] = *event.TopicSequence
	}
	if !sawCommandID {
		t.Fatal("authorized events did not preserve any command ID")
	}
}
