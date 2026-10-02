package projector

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"github.com/vayal-mikrogreenz/vm-events-consumer-api/internal/store"
)

// supplierPayload mirrors vm-profile-api/internal/analyticsevents.Supplier,
// schema version 1.
type supplierPayload struct {
	SupplierID    uuid.UUID  `json:"supplier_id"`
	BusinessName  string     `json:"business_name"`
	Status        string     `json:"status"`
	City          string     `json:"city"`
	State         string     `json:"state"`
	Pincode       string     `json:"pincode"`
	GSTRegistered bool       `json:"gst_registered"`
	CommissionBPS *int32     `json:"commission_bps"`
	ApprovedAt    *time.Time `json:"approved_at"`
	CreatedAt     time.Time  `json:"created_at"`
}

func projectSupplier(ctx context.Context, q *store.Queries, ev Event) error {
	if ev.SchemaVersion != 1 {
		return permanent("supplier event schema_version %d is not supported", ev.SchemaVersion)
	}
	var p supplierPayload
	if err := json.Unmarshal(ev.Payload, &p); err != nil {
		return permanent("supplier payload: %w", err)
	}
	if p.SupplierID == uuid.Nil || p.SupplierID != ev.AggregateID {
		return permanent("supplier payload is for %s, event aggregate is %s", p.SupplierID, ev.AggregateID)
	}

	// Profile keeps approved_at; a suspension's time is the event's.
	var suspendedAt *time.Time
	if ev.EventType == "supplier.suspended" {
		at := ev.OccurredAt
		suspendedAt = &at
	}

	_, err := q.UpsertSupplier(ctx, store.UpsertSupplierParams{
		SupplierID:    p.SupplierID,
		BusinessName:  p.BusinessName,
		Status:        p.Status,
		City:          nonEmpty(p.City),
		State:         nonEmpty(p.State),
		Pincode:       nonEmpty(p.Pincode),
		GstRegistered: p.GSTRegistered,
		CommissionBps: p.CommissionBPS,
		CreatedAt:     p.CreatedAt,
		ApprovedAt:    p.ApprovedAt,
		SuspendedAt:   suspendedAt,
		LastEventID:   ev.ID,
		LastEventType: ev.EventType,
		LastEventAt:   ev.OccurredAt,
	})
	return err
}
