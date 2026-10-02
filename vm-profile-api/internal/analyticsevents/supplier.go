// Package analyticsevents writes supplier events to supplier_events_outbox,
// the profile part of the analytics pipeline (CLAUDE.md §5.4).
//
// Every event carries the supplier as analytics may see it AFTER the change,
// so the consumer upserts and a missed or repeated event still converges.
//
// The payload is built from the type below and nothing else. It has no field
// for the contact person, phone, email, PAN, GSTIN, street address, bank
// details or a rejection reason (admin free text): analytics names a supplier
// by its business name and town — what the storefront already shows — and
// knows whether it is registered for GST without holding the number.
package analyticsevents

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vayal-mikrogreenz/vm-go-common/logging"

	"github.com/vayal-mikrogreenz/vm-profile-api/internal/store"
)

// SchemaVersion is the payload shape below. Bump it on any change a consumer
// would have to handle differently, and teach the consumer first.
const SchemaVersion = 1

// Event types. Must match the CHECK on supplier_events_outbox.event_type.
const (
	SupplierApplied   = "supplier.applied"
	SupplierCreated   = "supplier.created"
	SupplierUpdated   = "supplier.updated"
	SupplierApproved  = "supplier.approved"
	SupplierRejected  = "supplier.rejected"
	SupplierSuspended = "supplier.suspended"
	SupplierSnapshot  = "supplier.snapshot"
)

// Actors. Must match the CHECK on supplier_events_outbox.actor_role.
const (
	ActorSupplier = "supplier"
	ActorAdmin    = "admin"
	ActorSystem   = "system"
)

// Supplier is the analytics view of a supplier.
type Supplier struct {
	SupplierID    uuid.UUID `json:"supplier_id"`
	BusinessName  string    `json:"business_name"`
	Status        string    `json:"status"`
	City          string    `json:"city,omitempty"`
	State         string    `json:"state,omitempty"`
	Pincode       string    `json:"pincode,omitempty"`
	GSTRegistered bool      `json:"gst_registered"`
	// The negotiated commission in basis points; null = platform default.
	CommissionBPS *int32     `json:"commission_bps"`
	ApprovedAt    *time.Time `json:"approved_at"`
	CreatedAt     time.Time  `json:"created_at"`
}

// Emit writes one supplier event in the caller's transaction. `q` must be
// bound to it: the supplier is re-read through it, so the payload is the
// state being committed and the event rolls back with the change.
func Emit(
	ctx context.Context, q store.Querier, supplierID uuid.UUID,
	eventType, actor string, occurredAt time.Time,
) error {
	s, err := q.GetSupplierByID(ctx, supplierID)
	if err != nil {
		return fmt.Errorf("building %s event: %w", eventType, err)
	}
	payload, err := json.Marshal(Build(s))
	if err != nil {
		return err
	}
	var requestID *string
	if id := logging.RequestIDFrom(ctx); id != "" {
		requestID = &id
	}
	if _, err := q.InsertSupplierEvent(ctx, store.InsertSupplierEventParams{
		AggregateID:   supplierID,
		EventType:     eventType,
		SchemaVersion: SchemaVersion,
		OccurredAt:    occurredAt,
		ActorRole:     actor,
		RequestID:     requestID,
		Payload:       payload,
	}); err != nil {
		return fmt.Errorf("writing %s event: %w", eventType, err)
	}
	return nil
}

// Build is the analytics view of a supplier row.
func Build(s store.Supplier) Supplier {
	out := Supplier{
		SupplierID:    s.ID,
		BusinessName:  s.BusinessName,
		Status:        s.Status,
		GSTRegistered: s.Gstin != nil && *s.Gstin != "",
		CommissionBPS: s.CommissionBps,
		ApprovedAt:    s.ApprovedAt,
		CreatedAt:     s.CreatedAt.UTC(),
	}
	if s.City != nil {
		out.City = *s.City
	}
	if s.State != nil {
		out.State = *s.State
	}
	if s.Pincode != nil {
		out.Pincode = *s.Pincode
	}
	return out
}

// RunPruner deletes supplier_events_outbox rows older than retention, hourly,
// until ctx is cancelled (OUTBOX_RETENTION_DAYS).
func RunPruner(ctx context.Context, pool *pgxpool.Pool, retention time.Duration, logger *slog.Logger) {
	queries := store.New(pool)
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			pruned, err := queries.PruneSupplierEvents(ctx, time.Now().Add(-retention))
			if err != nil {
				logger.WarnContext(ctx, "pruning supplier events failed", slog.Any("error", err))
			} else if pruned > 0 {
				logger.InfoContext(ctx, "pruned supplier events", slog.Int64("rows", pruned))
			}
		}
	}
}

// EmitSnapshots writes a supplier.snapshot event for every supplier, oldest
// first — the backfill for suppliers that predate the outbox. They reach the
// analytics schema through the same CDC path as live events.
func EmitSnapshots(ctx context.Context, pool *pgxpool.Pool) (int, error) {
	queries := store.New(pool)
	afterCreated, afterID := time.Time{}, uuid.Nil
	total := 0
	for {
		page, err := queries.ListSupplierIDsAfter(ctx, store.ListSupplierIDsAfterParams{
			AfterCreatedAt: afterCreated, AfterID: afterID, PageSize: 200,
		})
		if err != nil {
			return total, fmt.Errorf("listing suppliers: %w", err)
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
			if err := Emit(ctx, q, row.ID, SupplierSnapshot, ActorSystem, now); err != nil {
				_ = tx.Rollback(ctx)
				return total, fmt.Errorf("supplier %s: %w", row.ID, err)
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
