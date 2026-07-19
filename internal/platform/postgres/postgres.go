// Package postgres owns construction of the PostgreSQL connection pool. Domain
// packages receive database interfaces and do not construct global connections.
package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

const DriverName = "pgx"

type Config struct {
	URL              string
	MaxOpen          int
	MaxIdle          int
	ConnectionMaxAge time.Duration
}

func Open(ctx context.Context, config Config) (*sql.DB, error) {
	if config.URL == "" {
		return nil, errors.New("database URL is required")
	}
	database, err := sql.Open(DriverName, config.URL)
	if err != nil {
		return nil, fmt.Errorf("open PostgreSQL: %w", err)
	}
	if config.MaxOpen <= 0 {
		config.MaxOpen = 12
	}
	if config.MaxIdle < 0 {
		config.MaxIdle = 0
	}
	if config.MaxIdle == 0 {
		config.MaxIdle = 4
	}
	if config.ConnectionMaxAge <= 0 {
		config.ConnectionMaxAge = 30 * time.Minute
	}
	database.SetMaxOpenConns(config.MaxOpen)
	database.SetMaxIdleConns(config.MaxIdle)
	database.SetConnMaxLifetime(config.ConnectionMaxAge)

	pingContext, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := database.PingContext(pingContext); err != nil {
		_ = database.Close()
		return nil, fmt.Errorf("ping PostgreSQL: %w", err)
	}
	return database, nil
}
