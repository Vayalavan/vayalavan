// Package migrate wraps goose so all three Go services run migrations the
// same way, from migrations embedded in their own binary.
//
// Embedding (rather than shipping a .sql directory alongside the binary)
// means the migrations that run are always exactly the ones the binary was
// built from. There is no way to deploy code and migrations out of step.
package migrate

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"log/slog"
	"time"

	// Registers the "pgx" database/sql driver. goose needs a *sql.DB, while
	// the services themselves use the native pgx pool.
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

// Direction is a migration command.
type Direction string

const (
	// DirectionUp applies all pending migrations.
	DirectionUp Direction = "up"
	// DirectionDown rolls back exactly one migration.
	DirectionDown Direction = "down"
	// DirectionStatus prints applied/pending state without changing anything.
	DirectionStatus Direction = "status"
)

// ParseDirection validates a command string from a CLI flag.
func ParseDirection(s string) (Direction, error) {
	switch Direction(s) {
	case DirectionUp, DirectionDown, DirectionStatus:
		return Direction(s), nil
	default:
		return "", fmt.Errorf("migrate: unknown direction %q (want up, down or status)", s)
	}
}

// Config describes one service's migration setup.
type Config struct {
	// DatabaseURL is the service's own connection string.
	DatabaseURL string
	// FS holds the embedded migration files.
	FS fs.FS
	// Dir is the path within FS, e.g. "migrations".
	Dir string
	// Schema is the service's Postgres schema (profile, catalog or orders).
	// The version table is created inside it so each service tracks its own
	// migration history independently.
	Schema string
}

// migrationTimeout bounds a migration run. Generous, because a migration on
// a large table legitimately takes minutes — but bounded, so a run blocked
// on a lock does not hang a deploy forever.
const migrationTimeout = 5 * time.Minute

// Run executes the requested migration command and returns when it is done.
func Run(ctx context.Context, cfg Config, direction Direction, logger *slog.Logger) error {
	ctx, cancel := context.WithTimeout(ctx, migrationTimeout)
	defer cancel()

	db, err := sql.Open("pgx", cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("migrate: opening database: %w", err)
	}
	defer db.Close()

	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("migrate: cannot reach database: %w", err)
	}

	goose.SetBaseFS(cfg.FS)
	// Silence goose's own stdout logger; this package logs through slog so
	// migration output lands in the same structured stream as everything else.
	goose.SetLogger(goose.NopLogger())

	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("migrate: setting dialect: %w", err)
	}

	// Qualify the version table with the service's schema. Without this all
	// three services would share one goose_db_version table in whichever
	// schema search_path resolved to first, and their histories would
	// interleave into nonsense.
	goose.SetTableName(cfg.Schema + ".goose_db_version")

	logger.InfoContext(ctx, "running migrations",
		slog.String("direction", string(direction)),
		slog.String("schema", cfg.Schema),
	)

	switch direction {
	case DirectionUp:
		if err := goose.UpContext(ctx, db, cfg.Dir); err != nil {
			return fmt.Errorf("migrate: up: %w", err)
		}
	case DirectionDown:
		if err := goose.DownContext(ctx, db, cfg.Dir); err != nil {
			return fmt.Errorf("migrate: down: %w", err)
		}
	case DirectionStatus:
		if err := goose.StatusContext(ctx, db, cfg.Dir); err != nil {
			return fmt.Errorf("migrate: status: %w", err)
		}
	default:
		return fmt.Errorf("migrate: unknown direction %q", direction)
	}

	version, err := goose.GetDBVersionContext(ctx, db)
	if err != nil {
		return fmt.Errorf("migrate: reading version: %w", err)
	}

	logger.InfoContext(ctx, "migrations complete",
		slog.String("schema", cfg.Schema),
		slog.Int64("version", version),
	)
	return nil
}
