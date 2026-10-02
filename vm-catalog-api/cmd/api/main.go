// Command api is the vm-catalog-api entrypoint.
//
// Run the server:      go run ./cmd/api
// Run migrations:      go run ./cmd/api -migrate up
// Prove storage works: go run ./cmd/api -verify-storage
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

	"github.com/vayal-mikrogreenz/vm-catalog-api/internal/analyticsevents"
	"github.com/vayal-mikrogreenz/vm-catalog-api/internal/app"
	"github.com/vayal-mikrogreenz/vm-catalog-api/internal/storage"
	"github.com/vayal-mikrogreenz/vm-catalog-api/migrations"
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
	verifyStorage := flag.Bool("verify-storage", false,
		"run the presigned upload/download self-test and exit")
	seedMocks := flag.String("seed-mocks", "",
		"development seed: create the products in <dir>/suppliers/*/catalog.json, uploading their media, then exit")
	emitSnapshots := flag.Bool("emit-snapshots", false,
		"backfill analytics: write one product.snapshot event per product, then exit")
	flag.Parse()

	// The single root .env, found by walking up from the working directory.
	// Absent in deployed environments, where real env vars are set by the
	// platform — and those always win.
	if err := config.LoadRootDotEnv(); err != nil {
		return err
	}

	cfg, err := app.LoadConfig()
	if err != nil {
		return err
	}

	logger := logging.New(cfg.Base.ServiceName, cfg.Base.LogLevel)
	ctx := context.Background()

	// Migration mode: run and exit, without opening a listener or touching
	// object storage.
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

	store, err := storage.New(ctx, cfg.Storage)
	if err != nil {
		return err
	}
	// CLAUDE.md §6.6: the bucket is created on startup if missing, so a
	// fresh `make dev` needs no manual console step.
	if err := store.EnsureBucket(ctx, logger); err != nil {
		return err
	}

	// Storage verification mode: prove presigned URLs work, then exit.
	if *verifyStorage {
		return store.SelfTest(ctx, logger)
	}

	pool, err := db.NewPool(ctx, cfg.Database, logger)
	if err != nil {
		return err
	}
	defer pool.Close()

	if *seedMocks != "" {
		res, err := app.NewAPI(cfg, pool, store, logger).SeedMocks(ctx, *seedMocks)
		logger.InfoContext(ctx, "mock catalogue seeded", "created", res.Created,
			"already_present", res.Skipped, "files_uploaded", res.Files)
		return err
	}

	if *emitSnapshots {
		count, err := analyticsevents.EmitSnapshots(ctx, pool)
		logger.InfoContext(ctx, "product snapshots emitted", "count", count)
		return err
	}

	pruneCtx, stopPruner := context.WithCancel(ctx)
	defer stopPruner()
	go analyticsevents.RunPruner(pruneCtx, pool, cfg.EventRetention, logger)

	return server.Run(ctx, cfg.Base, app.NewRouter(cfg, pool, store, logger), logger)
}
