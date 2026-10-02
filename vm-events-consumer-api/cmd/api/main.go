// Command api is the vm-events-consumer-api entrypoint.
//
// Run the consumer:    go run ./cmd/api
// Run migrations:      go run ./cmd/api -migrate up
//
// The process does two things: it reads the analytics events that Postgres
// logical replication streams from the catalog and orders outbox tables and
// projects them into the analytics schema (CLAUDE.md §5.4), and it serves
// /healthz, /readyz and /status for whoever is watching it do that.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"

	"github.com/vayal-mikrogreenz/vm-go-common/config"
	"github.com/vayal-mikrogreenz/vm-go-common/db"
	"github.com/vayal-mikrogreenz/vm-go-common/logging"
	"github.com/vayal-mikrogreenz/vm-go-common/migrate"
	"github.com/vayal-mikrogreenz/vm-go-common/server"

	"github.com/vayal-mikrogreenz/vm-events-consumer-api/internal/app"
	"github.com/vayal-mikrogreenz/vm-events-consumer-api/internal/cdc"
	"github.com/vayal-mikrogreenz/vm-events-consumer-api/internal/projector"
	"github.com/vayal-mikrogreenz/vm-events-consumer-api/migrations"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", app.ServiceName, err)
		os.Exit(1)
	}
}

func run() error {
	migrateCmd := flag.String("migrate", "",
		"run migrations (up|down|status) and exit instead of consuming")
	flag.Parse()

	if err := config.LoadRootDotEnv(); err != nil {
		return err
	}
	cfg, err := app.LoadConfig()
	if err != nil {
		return err
	}
	logger := logging.New(cfg.Base.ServiceName, cfg.Base.LogLevel)
	ctx := context.Background()

	if *migrateCmd != "" {
		direction, err := migrate.ParseDirection(*migrateCmd)
		if err != nil {
			return err
		}
		return migrate.Run(ctx, migrate.Config{
			DatabaseURL: cfg.Database.URL,
			FS:          migrations.FS,
			Dir:         migrations.Dir,
			Schema:      app.Schema,
		}, direction, logger)
	}

	pool, err := db.NewPool(ctx, cfg.Database, logger)
	if err != nil {
		return err
	}
	defer pool.Close()

	proj := projector.New(pool, logger)
	stream := cdc.New(cdc.Config{
		ReplicationURL: cfg.CDC.ReplicationURL,
		SlotName:       cfg.CDC.SlotName,
		Publication:    cfg.CDC.Publication,
		StatusInterval: cfg.CDC.StatusInterval,
	}, func(ctx context.Context, events []projector.Event) error {
		res, err := proj.ApplyTransaction(ctx, events)
		if err != nil {
			return err
		}
		logger.DebugContext(ctx, "applied events",
			slog.Int("applied", res.Applied), slog.Int("duplicates", res.Duplicates),
			slog.Int("dead_lettered", res.DeadLettered))
		return nil
	}, logger)

	workCtx, stop := context.WithCancel(ctx)
	defer stop()
	go stream.Run(workCtx)
	go proj.RunPruner(workCtx, cfg.ProcessedRetention)

	return server.Run(ctx, cfg.Base, app.NewRouter(cfg, pool, stream, logger), logger)
}
