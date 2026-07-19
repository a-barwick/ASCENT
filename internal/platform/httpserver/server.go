// Package httpserver owns the process-level HTTP boundary. Domain mutation
// handlers belong to their owning internal modules.
package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"ascent/internal/fixtures"
	"ascent/internal/identity"
	"ascent/internal/platform/ids"
	"ascent/internal/realtime"
	protocol "ascent/protocol/gen/go"
)

const (
	sessionCookieName = "ascent_session"
	maxRequestBytes   = 1 << 20
)

type Game interface {
	Snapshot(context.Context, identity.Actor) (protocol.SnapshotEnvelope, error)
	Execute(context.Context, identity.Actor, protocol.CommandEnvelope) (protocol.CommandResultEnvelope, error)
	Events(context.Context, identity.Actor, int64) ([]protocol.EventEnvelope, error)
}

type Options struct {
	Game          Game
	Identity      identity.Provider
	Development   *identity.DevelopmentProvider
	SeededActor   identity.Actor
	Broker        *realtime.Broker
	AllowedOrigin string
	Clock         func() time.Time
}

type Server struct {
	logger  *slog.Logger
	mux     *http.ServeMux
	options Options
}

func New(logger *slog.Logger) *Server {
	return NewWithOptions(logger, Options{})
}

func NewWithOptions(logger *slog.Logger, options Options) *Server {
	if options.Clock == nil {
		options.Clock = time.Now
	}
	server := &Server{
		logger:  logger,
		mux:     http.NewServeMux(),
		options: options,
	}
	server.routes()
	return server
}

func (s *Server) Handler() http.Handler {
	return s.logRequests(s.securityHeaders(s.mux))
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /healthz", s.health)
	s.mux.HandleFunc("GET /api/v1/system", s.system)
	s.mux.HandleFunc("GET /api/v1/fixtures/market", s.marketFixture)
	s.mux.HandleFunc("POST /api/v1/dev/session", s.developmentSession)
	s.mux.HandleFunc("DELETE /api/v1/session", s.endSession)
	s.mux.HandleFunc("GET /api/v1/game", s.gameSnapshot)
	s.mux.HandleFunc("POST /api/v1/commands", s.command)
	s.mux.HandleFunc("GET /api/v1/events", s.events)
	s.mux.HandleFunc("OPTIONS /api/v1/{path...}", s.preflight)
}

func (s *Server) health(writer http.ResponseWriter, _ *http.Request) {
	writeJSON(writer, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) system(writer http.ResponseWriter, _ *http.Request) {
	mode := "foundation"
	if s.options.Game != nil {
		mode = "first-playable"
	}
	writeJSON(writer, http.StatusOK, map[string]any{
		"service":         "ascent-server",
		"protocolVersion": protocol.Version,
		"authority":       "server",
		"database":        "postgresql",
		"mode":            mode,
	})
}

func (s *Server) marketFixture(writer http.ResponseWriter, _ *http.Request) {
	writeJSON(writer, http.StatusOK, fixtures.MarketScenario())
}

func (s *Server) developmentSession(writer http.ResponseWriter, request *http.Request) {
	if s.options.Development == nil || !s.options.Development.Enabled() || s.options.SeededActor.ID == "" {
		writeProblem(writer, http.StatusNotFound, "DEVELOPMENT_IDENTITY_DISABLED", "Development identity is disabled.")
		return
	}
	now := s.options.Clock()
	token, err := s.options.Development.Issue(s.options.SeededActor, now)
	if err != nil {
		s.logger.Error("issue development session", "error", err)
		writeProblem(writer, http.StatusInternalServerError, "SESSION_ISSUE_FAILED", "The session could not be created.")
		return
	}
	sessionID, err := ids.NewUUID()
	if err != nil {
		s.logger.Error("generate development session id", "error", err)
		writeProblem(writer, http.StatusInternalServerError, "SESSION_ISSUE_FAILED", "The session could not be created.")
		return
	}
	expiresAt := s.options.Development.ExpiresAt(now)
	http.SetCookie(writer, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   int(expiresAt.Sub(now).Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   request.TLS != nil,
	})
	writeJSON(writer, http.StatusCreated, map[string]any{
		"protocolVersion": protocol.Version,
		"session": map[string]any{
			"id":        sessionID,
			"actorId":   s.options.SeededActor.ID,
			"expiresAt": expiresAt,
		},
		"actor": map[string]any{
			"id":          s.options.SeededActor.ID,
			"displayName": s.options.SeededActor.DisplayName,
			"status":      "authenticated",
		},
	})
}

func (s *Server) endSession(writer http.ResponseWriter, request *http.Request) {
	http.SetCookie(writer, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   request.TLS != nil,
	})
	writer.WriteHeader(http.StatusNoContent)
}

func (s *Server) gameSnapshot(writer http.ResponseWriter, request *http.Request) {
	actor, ok := s.requireActor(writer, request)
	if !ok {
		return
	}
	if s.options.Game == nil {
		writeProblem(writer, http.StatusServiceUnavailable, "GAME_UNAVAILABLE", "The authoritative economy is not available.")
		return
	}
	snapshot, err := s.options.Game.Snapshot(request.Context(), actor)
	if err != nil {
		s.logger.Error("load game snapshot", "actor_id", actor.ID, "error", err)
		writeProblem(writer, http.StatusInternalServerError, "SNAPSHOT_FAILED", "The economy snapshot could not be loaded safely.")
		return
	}
	writeJSON(writer, http.StatusOK, snapshot)
}

func (s *Server) command(writer http.ResponseWriter, request *http.Request) {
	actor, ok := s.requireActor(writer, request)
	if !ok {
		return
	}
	if s.options.Game == nil {
		writeProblem(writer, http.StatusServiceUnavailable, "GAME_UNAVAILABLE", "The authoritative economy is not available.")
		return
	}
	var command protocol.CommandEnvelope
	if err := decodeJSON(writer, request, &command); err != nil {
		return
	}
	if command.ProtocolVersion != protocol.Version {
		writeProblem(writer, http.StatusConflict, "PROTOCOL_VERSION_MISMATCH", "Refresh the terminal before retrying this command.")
		return
	}
	if command.ActorId != actor.ID {
		writeProblem(writer, http.StatusForbidden, "ACTOR_MISMATCH", "The command actor does not match the authenticated session.")
		return
	}

	started := time.Now()
	result, err := s.options.Game.Execute(request.Context(), actor, command)
	duration := time.Since(started)
	if err != nil {
		s.logger.Error("command failed",
			"command_id", command.CommandId,
			"command_type", command.Type,
			"actor_id", actor.ID,
			"duration_ms", duration.Milliseconds(),
			"error", err,
		)
		writeProblem(writer, http.StatusInternalServerError, "COMMAND_FAILED", "The command failed and no partial result should be assumed.")
		return
	}
	s.logger.Info("command completed",
		"command_id", command.CommandId,
		"command_type", command.Type,
		"actor_id", actor.ID,
		"company_id", command.CompanyId,
		"status", result.Status,
		"duration_ms", duration.Milliseconds(),
	)
	if result.Status == protocol.CommandResultStatusCommitted && s.options.Broker != nil {
		s.options.Broker.NotifyCommitted()
	}
	status := http.StatusOK
	if result.Status == protocol.CommandResultStatusRejected {
		status = http.StatusUnprocessableEntity
	}
	writeJSON(writer, status, result)
}

func (s *Server) events(writer http.ResponseWriter, request *http.Request) {
	actor, ok := s.requireActor(writer, request)
	if !ok {
		return
	}
	if s.options.Game == nil {
		writeProblem(writer, http.StatusServiceUnavailable, "GAME_UNAVAILABLE", "The authoritative economy is not available.")
		return
	}
	after, err := parseCursor(request.URL.Query().Get("after"))
	if err != nil {
		writeProblem(writer, http.StatusBadRequest, "INVALID_CURSOR", "The event cursor must be a nonnegative integer.")
		return
	}
	if strings.Contains(request.Header.Get("Accept"), "text/event-stream") {
		s.streamEvents(writer, request, actor, after)
		return
	}
	events, err := s.options.Game.Events(request.Context(), actor, after)
	if err != nil {
		s.logger.Error("load events", "actor_id", actor.ID, "after", after, "error", err)
		writeProblem(writer, http.StatusInternalServerError, "EVENTS_FAILED", "Committed events could not be loaded.")
		return
	}
	latestSequence := after
	if len(events) > 0 {
		latestSequence = events[len(events)-1].Sequence
	}
	writeJSON(writer, http.StatusOK, map[string]any{
		"protocolVersion": protocol.Version,
		"after":           after,
		"latestSequence":  latestSequence,
		"events":          events,
	})
}

func (s *Server) streamEvents(writer http.ResponseWriter, request *http.Request, actor identity.Actor, after int64) {
	flusher, ok := writer.(http.Flusher)
	if !ok {
		writeProblem(writer, http.StatusNotImplemented, "STREAM_UNAVAILABLE", "Streaming is unavailable on this connection.")
		return
	}
	writer.Header().Set("Content-Type", "text/event-stream")
	writer.Header().Set("Cache-Control", "no-cache, no-transform")
	writer.Header().Set("Connection", "keep-alive")
	writer.WriteHeader(http.StatusOK)
	flusher.Flush()

	var wakeups <-chan struct{}
	if s.options.Broker != nil {
		wakeups = s.options.Broker.Subscribe(request.Context())
	}
	deadline := time.NewTimer(25 * time.Second)
	defer deadline.Stop()
	heartbeat := time.NewTicker(10 * time.Second)
	defer heartbeat.Stop()

	for {
		events, err := s.options.Game.Events(request.Context(), actor, after)
		if err != nil {
			s.logger.Error("stream events", "actor_id", actor.ID, "after", after, "error", err)
			_, _ = io.WriteString(writer, "event: error\ndata: {\"safeMessage\":\"Reconciliation required.\"}\n\n")
			flusher.Flush()
			return
		}
		for _, event := range events {
			encoded, marshalErr := json.Marshal(event)
			if marshalErr != nil {
				continue
			}
			_, _ = fmt.Fprintf(writer, "id: %d\nevent: committed\ndata: %s\n\n", event.Sequence, encoded)
			after = event.Sequence
		}
		if len(events) > 0 {
			flusher.Flush()
		}

		select {
		case <-request.Context().Done():
			return
		case <-deadline.C:
			return
		case <-heartbeat.C:
			_, _ = io.WriteString(writer, ": keepalive\n\n")
			flusher.Flush()
		case _, open := <-wakeups:
			if !open {
				wakeups = nil
			}
		}
	}
}

func (s *Server) requireActor(writer http.ResponseWriter, request *http.Request) (identity.Actor, bool) {
	if s.options.Identity == nil {
		writeProblem(writer, http.StatusUnauthorized, "SESSION_REQUIRED", "Authenticate before opening the terminal.")
		return identity.Actor{}, false
	}
	token := ""
	if cookie, err := request.Cookie(sessionCookieName); err == nil {
		token = cookie.Value
	}
	if token == "" {
		scheme, value, found := strings.Cut(request.Header.Get("Authorization"), " ")
		if found && strings.EqualFold(scheme, "Bearer") {
			token = value
		}
	}
	if token == "" {
		writeProblem(writer, http.StatusUnauthorized, "SESSION_REQUIRED", "Authenticate before opening the terminal.")
		return identity.Actor{}, false
	}
	actor, err := s.options.Identity.Resolve(token, s.options.Clock())
	if err != nil {
		writeProblem(writer, http.StatusUnauthorized, "SESSION_INVALID", "The session is missing, expired, or invalid.")
		return identity.Actor{}, false
	}
	return actor, true
}

func (s *Server) preflight(writer http.ResponseWriter, _ *http.Request) {
	writer.WriteHeader(http.StatusNoContent)
}

func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		started := time.Now()
		next.ServeHTTP(writer, request)
		s.logger.Info("http request",
			"method", request.Method,
			"path", request.URL.Path,
			"duration_ms", time.Since(started).Milliseconds(),
		)
	})
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		writer.Header().Set("Referrer-Policy", "no-referrer")
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		if s.options.AllowedOrigin != "" && request.Header.Get("Origin") == s.options.AllowedOrigin {
			writer.Header().Set("Access-Control-Allow-Origin", s.options.AllowedOrigin)
			writer.Header().Set("Access-Control-Allow-Credentials", "true")
			writer.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Last-Event-ID")
			writer.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
			writer.Header().Set("Vary", "Origin")
		}
		next.ServeHTTP(writer, request)
	})
}

func decodeJSON(writer http.ResponseWriter, request *http.Request, destination any) error {
	request.Body = http.MaxBytesReader(writer, request.Body, maxRequestBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		writeProblem(writer, http.StatusBadRequest, "INVALID_JSON", "The request body is not valid for this command.")
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeProblem(writer, http.StatusBadRequest, "INVALID_JSON", "The request must contain exactly one JSON value.")
		return errors.New("request contains trailing JSON")
	}
	return nil
}

func parseCursor(value string) (int64, error) {
	if value == "" {
		return 0, nil
	}
	cursor, err := strconv.ParseInt(value, 10, 64)
	if err != nil || cursor < 0 {
		return 0, errors.New("invalid cursor")
	}
	return cursor, nil
}

func writeProblem(writer http.ResponseWriter, status int, code, safeMessage string) {
	writeJSON(writer, status, map[string]any{
		"errorCode":    code,
		"safeMessage":  safeMessage,
		"stateChanged": false,
	})
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	if err := json.NewEncoder(writer).Encode(value); err != nil {
		slog.Error("encode response", "error", err)
	}
}
