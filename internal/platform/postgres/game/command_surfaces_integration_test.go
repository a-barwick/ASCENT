package gamepostgres

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"ascent/internal/fixtures"
	"ascent/internal/identity"
	"ascent/internal/platform/ids"
	protocol "ascent/protocol/gen/go"
	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestCommandSurfacesIntegration(t *testing.T) {
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
	now := fixtures.GeneratedAt.Add(2 * time.Hour)
	service := New(
		database,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		func() time.Time { return now },
	)
	if err := service.Seed(t.Context()); err != nil {
		t.Fatal(err)
	}
	actor := identity.Actor{ID: SeededActorID, DisplayName: SeededDisplayName}
	companyID := SeededCompanyID

	marketCommand := integrationCommand(
		t,
		database,
		actor,
		companyID,
		"market.place_order",
		fmt.Sprintf(
			`{"marketId":%q,"side":"buy","orderType":"limit","price":308.75,"quantity":1}`,
			marketID(seedWaterMarket),
		),
		now,
	)
	marketResult, err := service.Execute(t.Context(), actor, marketCommand)
	if err != nil {
		t.Fatal(err)
	}
	requireCommitted(t, marketResult)
	assertCommandEconomicTrace(t, database, marketCommand.CommandId, 2, 1, 2)

	conflict := marketCommand
	conflict.CommandId = integrationUUID(t)
	conflict.Payload = json.RawMessage(fmt.Sprintf(
		`{"marketId":%q,"side":"buy","orderType":"limit","price":308.75,"quantity":2}`,
		marketID(seedWaterMarket),
	))
	conflictResult, err := service.Execute(t.Context(), actor, conflict)
	if err != nil {
		t.Fatal(err)
	}
	if conflictResult.Status != protocol.CommandResultStatusRejected ||
		conflictResult.ErrorCode == nil || *conflictResult.ErrorCode != "IDEMPOTENCY_CONFLICT" {
		t.Fatalf("changed idempotent retry = %#v", conflictResult)
	}

	unauthorizedActor := identity.Actor{
		ID:          fixtures.SeededPlayerID(2),
		DisplayName: "Operator 02",
	}
	unauthorized := integrationCommand(
		t,
		database,
		unauthorizedActor,
		companyID,
		"market.place_order",
		fmt.Sprintf(
			`{"marketId":%q,"side":"buy","orderType":"limit","price":300,"quantity":1}`,
			marketID(seedWaterMarket),
		),
		now,
	)
	unauthorizedResult, err := service.Execute(t.Context(), unauthorizedActor, unauthorized)
	if err != nil {
		t.Fatal(err)
	}
	if unauthorizedResult.Status != protocol.CommandResultStatusRejected ||
		unauthorizedResult.ErrorCode == nil || *unauthorizedResult.ErrorCode != "FORBIDDEN" {
		t.Fatalf("unauthorized command = %#v", unauthorizedResult)
	}
	assertRowCount(t, database,
		`SELECT count(*) FROM markets.orders WHERE command_id = $1`, unauthorized.CommandId, 0)

	var quantityBefore int64
	if err := database.QueryRowContext(t.Context(), `
		SELECT quantity_minor
		FROM inventory.holdings
		WHERE company_id = $1 AND commodity_id = $2`,
		companyID, commodityID(seedWaterCommodity),
	).Scan(&quantityBefore); err != nil {
		t.Fatal(err)
	}
	oversell := integrationCommand(
		t,
		database,
		actor,
		companyID,
		"market.place_order",
		fmt.Sprintf(
			`{"marketId":%q,"side":"sell","orderType":"limit","price":400,"quantity":99999999}`,
			marketID(seedWaterMarket),
		),
		now,
	)
	oversellResult, err := service.Execute(t.Context(), actor, oversell)
	if err != nil {
		t.Fatal(err)
	}
	if oversellResult.Status != protocol.CommandResultStatusRejected ||
		oversellResult.ErrorCode == nil || *oversellResult.ErrorCode != "INSUFFICIENT_INVENTORY" {
		t.Fatalf("oversell command = %#v", oversellResult)
	}
	var quantityAfter int64
	if err := database.QueryRowContext(t.Context(), `
		SELECT quantity_minor
		FROM inventory.holdings
		WHERE company_id = $1 AND commodity_id = $2`,
		companyID, commodityID(seedWaterCommodity),
	).Scan(&quantityAfter); err != nil {
		t.Fatal(err)
	}
	if quantityAfter != quantityBefore {
		t.Fatalf("rejected oversell changed inventory: %d -> %d", quantityBefore, quantityAfter)
	}
	assertRowCount(t, database,
		`SELECT count(*) FROM markets.orders WHERE command_id = $1`, oversell.CommandId, 0)

	openOrder := integrationCommand(
		t,
		database,
		actor,
		companyID,
		"market.place_order",
		fmt.Sprintf(
			`{"marketId":%q,"side":"buy","orderType":"limit","price":300,"quantity":1}`,
			marketID(seedWaterMarket),
		),
		now,
	)
	openResult, err := service.Execute(t.Context(), actor, openOrder)
	if err != nil {
		t.Fatal(err)
	}
	requireCommitted(t, openResult)
	var opened struct {
		OrderID string `json:"orderId"`
	}
	if err := json.Unmarshal(openResult.Payload, &opened); err != nil {
		t.Fatal(err)
	}
	cancel := integrationCommand(
		t,
		database,
		actor,
		companyID,
		"market.cancel_order",
		fmt.Sprintf(`{"orderId":%q}`, opened.OrderID),
		now,
	)
	cancelResult, err := service.Execute(t.Context(), actor, cancel)
	if err != nil {
		t.Fatal(err)
	}
	requireCommitted(t, cancelResult)
	var orderStatus, reservationStatus string
	if err := database.QueryRowContext(t.Context(), `
		SELECT orders.status, reservation.status
		FROM markets.orders AS orders
		JOIN markets.order_reservations AS reservation
		  ON reservation.order_id = orders.order_id
		WHERE orders.order_id = $1`,
		opened.OrderID,
	).Scan(&orderStatus, &reservationStatus); err != nil {
		t.Fatal(err)
	}
	if orderStatus != "cancelled" || reservationStatus != "released" {
		t.Fatalf("cancelled order state = %s/%s", orderStatus, reservationStatus)
	}

	register := integrationCommand(
		t,
		database,
		actor,
		companyID,
		"device.register",
		`{"name":"Integration console","capabilities":["panel.receive"]}`,
		now,
	)
	registerResult, err := service.Execute(t.Context(), actor, register)
	if err != nil {
		t.Fatal(err)
	}
	requireCommitted(t, registerResult)

	panel := integrationCommand(
		t,
		database,
		actor,
		companyID,
		"device.panel_send",
		fmt.Sprintf(
			`{"deviceId":%q,"panelId":%q,"message":"Integration route verified"}`,
			scenarioID(fixtures.NamespaceDevice, 1),
			scenarioID(fixtures.NamespaceView, 101),
		),
		now,
	)
	panelResult, err := service.Execute(t.Context(), actor, panel)
	if err != nil {
		t.Fatal(err)
	}
	requireCommitted(t, panelResult)

	chatCommand := integrationCommand(
		t,
		database,
		actor,
		companyID,
		"chat.send",
		`{"channelId":"company-operations","body":"Integration channel verified."}`,
		now,
	)
	chatResult, err := service.Execute(t.Context(), actor, chatCommand)
	if err != nil {
		t.Fatal(err)
	}
	requireCommitted(t, chatResult)

	compensation := integrationCommand(
		t,
		database,
		actor,
		companyID,
		"operator.compensate",
		fmt.Sprintf(
			`{"targetEventId":%q,"reason":"Integration correction verifies immutable reversal."}`,
			marketCommand.CommandId,
		),
		now,
	)
	compensationResult, err := service.Execute(t.Context(), actor, compensation)
	if err != nil {
		t.Fatal(err)
	}
	requireCommitted(t, compensationResult)
	assertRowCount(t, database, `
		SELECT count(*)
		FROM operator_admin.compensations
		WHERE command_id = $1 AND status = 'committed'`, compensation.CommandId, 1)
	assertCommandBalanced(t, database, compensation.CommandId)
	assertRowCount(t, database, `
		SELECT count(*)
		FROM inventory.movements
		WHERE command_id = $1 AND movement_kind = 'correction'`, compensation.CommandId, 2)
}

func integrationCommand(
	t *testing.T,
	database *sql.DB,
	actor identity.Actor,
	companyID, commandType, payload string,
	now time.Time,
) protocol.CommandEnvelope {
	t.Helper()
	var version int64
	if err := database.QueryRowContext(t.Context(), `
		SELECT version FROM companies.companies WHERE company_id = $1`,
		companyID,
	).Scan(&version); err != nil {
		t.Fatal(err)
	}
	commandID := integrationUUID(t)
	return protocol.CommandEnvelope{
		ActorId:         actor.ID,
		CommandId:       commandID,
		CompanyId:       &companyID,
		ExpectedVersion: &version,
		IdempotencyKey:  "integration-" + commandID,
		Payload:         json.RawMessage(payload),
		ProtocolVersion: protocol.Version,
		ReceivedAt:      now,
		Type:            commandType,
	}
}

func integrationUUID(t *testing.T) string {
	t.Helper()
	value, err := ids.NewUUID()
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func requireCommitted(t *testing.T, result protocol.CommandResultEnvelope) {
	t.Helper()
	if result.Status != protocol.CommandResultStatusCommitted {
		t.Fatalf("command result = %#v", result)
	}
}

func assertCommandEconomicTrace(
	t *testing.T,
	database *sql.DB,
	commandID string,
	wantJournals, wantMovements, wantEvents int,
) {
	t.Helper()
	assertRowCount(t, database,
		`SELECT count(*) FROM ledger.journals WHERE command_id = $1`, commandID, wantJournals)
	assertRowCount(t, database,
		`SELECT count(*) FROM inventory.movements WHERE command_id = $1`, commandID, wantMovements)
	assertRowCount(t, database,
		`SELECT count(*) FROM platform.event_outbox WHERE command_id = $1`, commandID, wantEvents)
	assertCommandBalanced(t, database, commandID)
}

func assertCommandBalanced(t *testing.T, database *sql.DB, commandID string) {
	t.Helper()
	var unbalanced int
	if err := database.QueryRowContext(t.Context(), `
		SELECT count(*)
		FROM (
			SELECT journal.journal_id
			FROM ledger.journals AS journal
			JOIN ledger.entries AS entry
			  ON entry.journal_id = journal.journal_id
			WHERE journal.command_id = $1
			GROUP BY journal.journal_id
			HAVING sum(
				CASE entry.side WHEN 'debit' THEN entry.amount_minor ELSE -entry.amount_minor END
			) <> 0
		) AS unbalanced`,
		commandID,
	).Scan(&unbalanced); err != nil {
		t.Fatal(err)
	}
	if unbalanced != 0 {
		t.Fatalf("command %s has %d unbalanced journals", commandID, unbalanced)
	}
}

func assertRowCount(
	t *testing.T,
	database *sql.DB,
	query string,
	argument any,
	want int,
) {
	t.Helper()
	var count int
	if err := database.QueryRowContext(t.Context(), query, argument).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != want {
		t.Fatalf("row count = %d, want %d for %s", count, want, query)
	}
}
