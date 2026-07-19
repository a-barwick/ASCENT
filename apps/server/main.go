package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"ascent/internal/fixtures"
	"ascent/internal/identity"
	"ascent/internal/platform/httpserver"
	platformpostgres "ascent/internal/platform/postgres"
	gamepostgres "ascent/internal/platform/postgres/game"
	"ascent/internal/realtime"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(os.Args[1:], logger); err != nil {
		logger.Error("command failed", "error", err)
		os.Exit(1)
	}
}

func run(args []string, logger *slog.Logger) error {
	command := "serve"
	if len(args) > 0 {
		command = args[0]
		args = args[1:]
	}

	switch command {
	case "serve":
		return serve(args, logger)
	case "seed":
		return seed(args)
	case "seed-db":
		return seedDatabase(args, logger)
	default:
		return fmt.Errorf("unknown command %q; expected serve, seed, or seed-db", command)
	}
}

func serve(args []string, logger *slog.Logger) error {
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	address := flags.String("http-addr", envOr("ASCENT_HTTP_ADDR", ":8080"), "HTTP listen address")
	databaseURL := flags.String("database-url", envOr(
		"DATABASE_URL",
		"postgres://ascent:ascent@localhost:5432/ascent?sslmode=disable",
	), "PostgreSQL connection URL")
	developmentIdentity := flags.Bool(
		"dev-identity",
		envTrue("ASCENT_DEV_IDENTITY"),
		"enable the signed local development identity",
	)
	seedScenario := flags.Bool(
		"seed-scenario",
		envTrue("ASCENT_SEED_SCENARIO"),
		"install the deterministic first-playable scenario",
	)
	allowedOrigin := flags.String(
		"allowed-origin",
		envOr("ASCENT_ALLOWED_ORIGIN", "http://localhost:5173"),
		"browser origin allowed to call the API directly",
	)
	if err := flags.Parse(args); err != nil {
		return err
	}

	startupContext, cancelStartup := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancelStartup()
	database, err := platformpostgres.Open(startupContext, platformpostgres.Config{URL: *databaseURL})
	if err != nil {
		return err
	}
	defer database.Close()

	scenarioStartedAt := time.Now()
	simulationClock := func() time.Time {
		return fixtures.GeneratedAt.Add(time.Since(scenarioStartedAt))
	}
	game := gamepostgres.New(database, logger, simulationClock)
	if *seedScenario {
		if err := game.Seed(startupContext); err != nil {
			return err
		}
	}
	developmentProvider, err := identity.NewDevelopmentProvider(
		*developmentIdentity,
		os.Getenv("ASCENT_DEV_SESSION_SECRET"),
		12*time.Hour,
	)
	if err != nil {
		return fmt.Errorf("configure development identity: %w", err)
	}
	broker := realtime.NewBroker()
	server := &http.Server{
		Addr: *address,
		Handler: httpserver.NewWithOptions(logger, httpserver.Options{
			Game:        game,
			Identity:    developmentProvider,
			Development: developmentProvider,
			SeededActor: identity.Actor{
				ID:          gamepostgres.SeededActorID,
				DisplayName: gamepostgres.SeededDisplayName,
			},
			Broker:        broker,
			AllowedOrigin: *allowedOrigin,
		}).Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errorsChannel := make(chan error, 1)
	go func() {
		logger.Info("server listening", "address", *address)
		errorsChannel <- server.ListenAndServe()
	}()

	select {
	case err := <-errorsChannel:
		if !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serve HTTP: %w", err)
		}
		return nil
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return server.Shutdown(shutdownCtx)
	}
}

func seedDatabase(args []string, logger *slog.Logger) error {
	flags := flag.NewFlagSet("seed-db", flag.ContinueOnError)
	databaseURL := flags.String("database-url", envOr(
		"DATABASE_URL",
		"postgres://ascent:ascent@localhost:5432/ascent?sslmode=disable",
	), "PostgreSQL connection URL")
	if err := flags.Parse(args); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	database, err := platformpostgres.Open(ctx, platformpostgres.Config{URL: *databaseURL})
	if err != nil {
		return err
	}
	defer database.Close()
	game := gamepostgres.New(database, logger, func() time.Time { return fixtures.GeneratedAt })
	return game.Seed(ctx)
}

func seed(args []string) error {
	flags := flag.NewFlagSet("seed", flag.ContinueOnError)
	output := flags.String("output", "-", "write deterministic fixture JSON to this path, or - for stdout")
	if err := flags.Parse(args); err != nil {
		return err
	}

	content, err := json.MarshalIndent(fixtures.MarketScenario(), "", "  ")
	if err != nil {
		return fmt.Errorf("encode fixture: %w", err)
	}
	content = append(content, '\n')

	if *output == "-" {
		_, err = os.Stdout.Write(content)
		return err
	}
	if err := os.MkdirAll(filepath.Dir(*output), 0o755); err != nil {
		return fmt.Errorf("create fixture directory: %w", err)
	}
	if err := os.WriteFile(*output, content, 0o644); err != nil {
		return fmt.Errorf("write fixture: %w", err)
	}
	return nil
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func envTrue(key string) bool {
	switch os.Getenv(key) {
	case "1", "true", "TRUE", "yes", "YES", "on", "ON":
		return true
	default:
		return false
	}
}
