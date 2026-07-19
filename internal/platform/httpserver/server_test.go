package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"ascent/internal/fixtures"
	"ascent/internal/identity"
	protocol "ascent/protocol/gen/go"
)

func TestHealth(t *testing.T) {
	t.Parallel()

	response := request(t, "/healthz")
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	if got := response.Header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q", got)
	}
}

func TestSystemReportsProtocolVersion(t *testing.T) {
	t.Parallel()

	response := request(t, "/api/v1/system")
	defer response.Body.Close()

	var body map[string]any
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if got := body["protocolVersion"]; got != protocol.Version {
		t.Fatalf("protocolVersion = %#v, want %q", got, protocol.Version)
	}
}

func TestMarketFixtureUsesStableSeed(t *testing.T) {
	t.Parallel()

	response := request(t, "/api/v1/fixtures/market")
	defer response.Body.Close()

	var body fixtures.MarketSnapshot
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Seed != fixtures.Seed {
		t.Fatalf("seed = %d, want %d", body.Seed, fixtures.Seed)
	}
}

func request(t *testing.T, path string) *http.Response {
	t.Helper()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, path, nil)
	New(logger).Handler().ServeHTTP(recorder, request)
	return recorder.Result()
}

type fakeGame struct {
	executed int
}

func (game *fakeGame) Snapshot(_ context.Context, actor identity.Actor) (protocol.SnapshotEnvelope, error) {
	payload, _ := json.Marshal(map[string]any{"actor": actor})
	return protocol.SnapshotEnvelope{
		GeneratedAt:     time.Date(2077, 5, 24, 14, 38, 5, 0, time.UTC),
		Payload:         payload,
		ProtocolVersion: protocol.Version,
		Sequence:        7,
		SnapshotId:      "snapshot-test",
		Topic:           "game.test",
	}, nil
}

func (game *fakeGame) Execute(_ context.Context, _ identity.Actor, command protocol.CommandEnvelope) (protocol.CommandResultEnvelope, error) {
	game.executed++
	committedAt := time.Date(2077, 5, 24, 14, 38, 5, 0, time.UTC)
	return protocol.CommandResultEnvelope{
		CommandId:       command.CommandId,
		CommittedAt:     &committedAt,
		Payload:         json.RawMessage(`{"orderId":"order-1"}`),
		ProtocolVersion: protocol.Version,
		Status:          protocol.CommandResultStatusCommitted,
	}, nil
}

func (game *fakeGame) Events(_ context.Context, _ identity.Actor, after int64) ([]protocol.EventEnvelope, error) {
	if after >= 8 {
		return nil, nil
	}
	return []protocol.EventEnvelope{{
		OccurredAt:      time.Date(2077, 5, 24, 14, 38, 5, 0, time.UTC),
		Payload:         json.RawMessage(`{"priceMinor":30825}`),
		ProtocolVersion: protocol.Version,
		Sequence:        8,
		Topic:           "market:lunar:water",
		Type:            "TRADE_EXECUTED",
	}}, nil
}

func TestProtectedRoutesRequireServerResolvedSession(t *testing.T) {
	t.Parallel()

	now := time.Date(2077, 5, 24, 14, 38, 4, 0, time.UTC)
	provider, err := identity.NewDevelopmentProvider(true, testSessionSecret, 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	game := &fakeGame{}
	server := NewWithOptions(testLogger(), Options{
		Game:        game,
		Identity:    provider,
		Development: provider,
		SeededActor: identity.Actor{ID: "player-helios", DisplayName: "Ari Chen"},
		Clock:       func() time.Time { return now },
	})

	missing := httptest.NewRecorder()
	server.Handler().ServeHTTP(missing, httptest.NewRequest(http.MethodGet, "/api/v1/game", nil))
	if missing.Code != http.StatusUnauthorized {
		t.Fatalf("missing status = %d", missing.Code)
	}

	login := httptest.NewRecorder()
	server.Handler().ServeHTTP(login, httptest.NewRequest(http.MethodPost, "/api/v1/dev/session", nil))
	if login.Code != http.StatusCreated {
		t.Fatalf("login status = %d, body = %s", login.Code, login.Body.String())
	}
	cookies := login.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly {
		t.Fatalf("session cookies = %#v", cookies)
	}

	protectedRequest := httptest.NewRequest(http.MethodGet, "/api/v1/game", nil)
	protectedRequest.AddCookie(cookies[0])
	protected := httptest.NewRecorder()
	server.Handler().ServeHTTP(protected, protectedRequest)
	if protected.Code != http.StatusOK {
		t.Fatalf("protected status = %d, body = %s", protected.Code, protected.Body.String())
	}
}

func TestCommandRejectsBodyActorMismatchWithoutExecution(t *testing.T) {
	t.Parallel()

	now := time.Date(2077, 5, 24, 14, 38, 4, 0, time.UTC)
	provider, err := identity.NewDevelopmentProvider(true, testSessionSecret, 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	token, err := provider.Issue(identity.Actor{ID: "player-helios", DisplayName: "Ari Chen"}, now)
	if err != nil {
		t.Fatal(err)
	}
	game := &fakeGame{}
	server := NewWithOptions(testLogger(), Options{
		Game:     game,
		Identity: provider,
		Clock:    func() time.Time { return now },
	})
	body := []byte(`{
		"protocolVersion":"` + protocol.Version + `",
		"commandId":"10000000-0000-4000-8000-000000000001",
		"idempotencyKey":"order-1",
		"type":"PLACE_ORDER",
		"actorId":"player-attacker",
		"receivedAt":"2077-05-24T14:38:04Z",
		"payload":{"side":"buy"}
	}`)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/commands", bytes.NewReader(body))
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if game.executed != 0 {
		t.Fatalf("executed = %d", game.executed)
	}
}

func TestEventsReturnOrderedFactsAfterCursor(t *testing.T) {
	t.Parallel()

	now := time.Date(2077, 5, 24, 14, 38, 4, 0, time.UTC)
	provider, err := identity.NewDevelopmentProvider(true, testSessionSecret, 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	token, err := provider.Issue(identity.Actor{ID: "player-helios", DisplayName: "Ari Chen"}, now)
	if err != nil {
		t.Fatal(err)
	}
	server := NewWithOptions(testLogger(), Options{
		Game:     &fakeGame{},
		Identity: provider,
		Clock:    func() time.Time { return now },
	})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/events?after=7", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var body struct {
		Events []protocol.EventEnvelope `json:"events"`
	}
	if err := json.NewDecoder(recorder.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Events) != 1 || body.Events[0].Sequence != 8 {
		t.Fatalf("events = %#v", body.Events)
	}
}

func TestDevelopmentIdentityEndpointIsAbsentWhenDisabled(t *testing.T) {
	t.Parallel()

	provider, err := identity.NewDevelopmentProvider(false, "", 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	server := NewWithOptions(testLogger(), Options{Development: provider})
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/dev/session", nil))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d", recorder.Code)
	}
}

const testSessionSecret = "test-only-http-session-secret-0000001"

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
