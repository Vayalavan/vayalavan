package analyticsevents

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vayal-mikrogreenz/vm-catalog-api/internal/store"
)

// pruneInterval is how often old event rows are deleted. Hourly is plenty
// for a retention measured in days.
const pruneInterval = time.Hour

// RunPruner deletes product_events_outbox rows older than retention until ctx
// is cancelled (OUTBOX_RETENTION_DAYS). CDC reads the WAL, so by then the rows
// are only a replay window. Safe on every replica: the DELETE is idempotent.
func RunPruner(ctx context.Context, pool *pgxpool.Pool, retention time.Duration, logger *slog.Logger) {
	queries := store.New(pool)
	ticker := time.NewTicker(pruneInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			pruned, err := queries.PruneProductEvents(ctx, time.Now().Add(-retention))
			if err != nil {
				logger.WarnContext(ctx, "pruning product events failed", slog.Any("error", err))
				continue
			}
			if pruned > 0 {
				logger.InfoContext(ctx, "pruned product events", slog.Int64("rows", pruned))
			}
		}
	}
}

// snapshotPage bounds one page of the backfill: one transaction per page.
const snapshotPage = 200

// EmitSnapshots writes a product.snapshot event for every product, oldest
// first — for products that predate the analytics outbox. They reach the
// analytics schema through the same CDC path as live events, so there is no
// second loader to keep in step. Re-running is harmless: the consumer
// upserts, and an unchanged price opens no new pack version.
func EmitSnapshots(ctx context.Context, pool *pgxpool.Pool) (int, error) {
	queries := store.New(pool)
	afterCreated, afterID := time.Time{}, uuid.Nil
	total := 0
	for {
		page, err := queries.ListProductIDsAfter(ctx, store.ListProductIDsAfterParams{
			AfterCreatedAt: afterCreated, AfterID: afterID, PageSize: snapshotPage,
		})
		if err != nil {
			return total, fmt.Errorf("listing products: %w", err)
		}
		if len(page) == 0 {
			return total, nil
		}

		tx, err := pool.Begin(ctx)
		if err != nil {
			return total, err
		}
		q := queries.WithTx(tx)
		now := time.Now()
		for _, row := range page {
			if err := Emit(ctx, q, row.ID, ProductSnapshot, ActorSystem, SourceBackfill, now); err != nil {
				_ = tx.Rollback(ctx)
				return total, fmt.Errorf("product %s: %w", row.ID, err)
			}
		}
		if err := tx.Commit(ctx); err != nil {
			return total, err
		}
		total += len(page)
		last := page[len(page)-1]
		afterCreated, afterID = last.CreatedAt, last.ID
	}
}
