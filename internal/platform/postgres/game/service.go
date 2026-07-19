// Package gamepostgres adapts the first-playable domain use cases to
// PostgreSQL. Domain state, command result, and outbox facts share a transaction.
package gamepostgres

import (
	"database/sql"
	"errors"
	"log/slog"
	"time"
)

var ErrNotSeeded = errors.New("first-playable scenario is not seeded")

type Service struct {
	database *sql.DB
	logger   *slog.Logger
	clock    func() time.Time
}

func New(database *sql.DB, logger *slog.Logger, clock func() time.Time) *Service {
	if clock == nil {
		clock = time.Now
	}
	return &Service{database: database, logger: logger, clock: clock}
}
