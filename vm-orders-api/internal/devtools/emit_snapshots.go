package main

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vayal-mikrogreenz/vm-orders-api/internal/analyticsevents"
	"github.com/vayal-mikrogreenz/vm-orders-api/internal/store"
)

// snapshotPage bounds one page of the backfill: one transaction per page, so
// a long backfill holds no lock for long and can be stopped and re-run.
const snapshotPage = 200

// emitSnapshots writes an order.snapshot event for every order, oldest first.
//
// For orders that predate the analytics outbox (CLAUDE.md §5.4): they reach
// the analytics schema through the same CDC path as any live event, so there
// is no second loader to keep in step. Re-running is harmless — a snapshot is
// the order's current state, and the consumer upserts.
func emitSnapshots(ctx context.Context, pool *pgxpool.Pool) {
	queries := store.New(pool)
	afterPlaced, afterID := time.Time{}, uuid.Nil
	total := 0

	for {
		page, err := queries.ListOrderIDsAfter(ctx, store.ListOrderIDsAfterParams{
			AfterPlacedAt: afterPlaced, AfterID: afterID, PageSize: snapshotPage,
		})
		if err != nil {
			fmt.Println("listing orders:", err)
			return
		}
		if len(page) == 0 {
			break
		}

		tx, err := pool.Begin(ctx)
		if err != nil {
			fmt.Println("begin:", err)
			return
		}
		q := queries.WithTx(tx)
		now := time.Now()
		for _, row := range page {
			if err := analyticsevents.Emit(ctx, q, row.ID, analyticsevents.OrderSnapshot,
				analyticsevents.ActorSystem, now, analyticsevents.Options{}); err != nil {
				_ = tx.Rollback(ctx)
				fmt.Printf("order %s: %v\n", row.ID, err)
				return
			}
		}
		if err := tx.Commit(ctx); err != nil {
			fmt.Println("commit:", err)
			return
		}

		total += len(page)
		last := page[len(page)-1]
		afterPlaced, afterID = last.PlacedAt, last.ID
		fmt.Printf("  %d orders\n", total)
	}
	fmt.Printf("emitted %d order.snapshot events\n", total)
}
