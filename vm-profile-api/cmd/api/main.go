// Command api is the vm-profile-api entrypoint.
//
// Run the server:      go run ./cmd/api
// Run migrations:      go run ./cmd/api -migrate up
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/vayal-mikrogreenz/vm-go-common/config"
	"github.com/vayal-mikrogreenz/vm-go-common/db"
	"github.com/vayal-mikrogreenz/vm-go-common/logging"
	"github.com/vayal-mikrogreenz/vm-go-common/migrate"
	"github.com/vayal-mikrogreenz/vm-go-common/server"

	"github.com/vayal-mikrogreenz/vm-profile-api/internal/analyticsevents"
	"github.com/vayal-mikrogreenz/vm-profile-api/internal/app"
	"github.com/vayal-mikrogreenz/vm-profile-api/migrations"
)

func main() {
	if err := run(); err != nil {
		// Written to stderr rather than the structured logger: a failure
		// here usually means config never loaded, so no logger exists yet.
		fmt.Fprintf(os.Stderr, "%s: %v\n", app.ServiceName, err)
		os.Exit(1)
	}
}

func run() error {
	migrateCmd := flag.String("migrate", "",
		"run migrations (up|down|status) and exit instead of serving")
	emitSnapshots := flag.Bool("emit-snapshots", false,
		"backfill analytics: write one supplier.snapshot event per supplier, then exit")
	flag.Parse()

	// Local development convenience. Absent in deployed environments, where
	// real env vars are set by the platform — and those always win.
	if err := config.LoadRootDotEnv(); err != nil {
		return err
	}

	cfg, err := app.LoadConfig()
	if err != nil {
		return err
	}

	logger := logging.New(cfg.Base.ServiceName, cfg.Base.LogLevel)
	ctx := context.Background()

	migrationCfg := migrate.Config{
		DatabaseURL: cfg.Database.URL,
		FS:          migrations.FS,
		Dir:         migrations.Dir,
		Schema:      app.Schema,
	}

	// Migration mode: run and exit, without opening a listener.
	if *migrateCmd != "" {
		direction, err := migrate.ParseDirection(*migrateCmd)
		if err != nil {
			return err
		}
		return migrate.Run(ctx, migrationCfg, direction, logger)
	}

	pool, err := db.NewPool(ctx, cfg.Database, logger)
	if err != nil {
		return err
	}
	defer pool.Close()

	if *emitSnapshots {
		count, err := analyticsevents.EmitSnapshots(ctx, pool)
		logger.InfoContext(ctx, "supplier snapshots emitted", "count", count)
		return err
	}

	pruneCtx, stopPruner := context.WithCancel(ctx)
	defer stopPruner()
	go analyticsevents.RunPruner(pruneCtx, pool, cfg.EventRetention, logger)

	return server.Run(ctx, cfg.Base, app.NewRouter(cfg, pool, logger), logger)
}
