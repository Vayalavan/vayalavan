package cdc

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pglogrepl"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/vayal-mikrogreenz/vm-go-common/config"

	"github.com/vayal-mikrogreenz/vm-events-consumer-api/internal/projector"
)

// TestStreamDeliversOutboxInserts is the pipeline end to end, minus the
// projection: a row inserted into orders.order_events_outbox by the ORDERS
// role arrives at the handler, decoded, in its own transaction — read through
// the real publication by the CDC role, on a throwaway slot so the running
// consumer's position is untouched.
//
// The publication is shared, so a consumer running against the same database
// sees this event too and parks it in analytics.dead_letter_events (its
// payload is not an order). The payload names this test so that row explains
// itself.
func TestStreamDeliversOutboxInserts(t *testing.T) {
	if err := config.LoadRootDotEnv(); err != nil {
		t.Skipf("no root .env: %v", err)
	}
	replURL := os.Getenv("EVENTS_CONSUMER_REPLICATION_URL")
	ordersURL := os.Getenv("ORDERS_DATABASE_URL")
	publication := os.Getenv("CDC_PUBLICATION")
	if replURL == "" || ordersURL == "" || publication == "" {
		t.Skip("replication env not set; run `make -C infra migrate-up` first")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	orders, err := pgx.Connect(ctx, ordersURL)
	if err != nil {
		t.Skipf("orders database unreachable: %v", err)
	}
	defer orders.Close(ctx)

	slot := "vayal_analytics_test_" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	t.Cleanup(func() {
		conn, err := pgconn.Connect(context.Background(), replURL)
		if err != nil {
			return
		}
		defer conn.Close(context.Background())
		_ = pglogrepl.DropReplicationSlot(context.Background(), conn, slot,
			pglogrepl.DropReplicationSlotOptions{Wait: true})
	})

	got := make(chan []projector.Event, 4)
	stream := New(Config{
		ReplicationURL: replURL, SlotName: slot, Publication: publication,
		StatusInterval: time.Second,
	}, func(_ context.Context, events []projector.Event) error {
		got <- events
		return nil
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	streamCtx, stop := context.WithCancel(ctx)
	defer stop()
	go stream.Run(streamCtx)

	// The slot must exist before the insert, or the insert is before its
	// starting point and never arrives.
	deadline := time.Now().Add(10 * time.Second)
	for !stream.Status().Connected {
		if time.Now().After(deadline) {
			t.Fatalf("stream never connected: %+v", stream.Status())
		}
		time.Sleep(50 * time.Millisecond)
	}

	orderID := uuid.New()
	occurred := time.Date(2026, 6, 15, 10, 30, 0, 123000000, time.UTC)
	var eventID uuid.UUID
	if err := orders.QueryRow(ctx, `
		INSERT INTO order_events_outbox
			(aggregate_id, event_type, schema_version, occurred_at, actor_role, payload)
		VALUES ($1, 'order.placed', 1, $2, 'customer', '{"test": "cdc-stream-test", "note": "ünïcode"}')
		RETURNING id`, orderID, occurred).Scan(&eventID); err != nil {
		t.Fatalf("inserting event: %v", err)
	}
	t.Cleanup(func() {
		_, _ = orders.Exec(context.Background(),
			`DELETE FROM order_events_outbox WHERE id = $1`, eventID)
	})

	for {
		select {
		case <-ctx.Done():
			t.Fatal("event never arrived")
		case events := <-got:
			for _, ev := range events {
				if ev.ID != eventID {
					continue // someone else's event on the shared publication
				}
				if ev.Table != projector.OrderEvents || ev.AggregateID != orderID ||
					ev.EventType != "order.placed" || ev.SchemaVersion != 1 ||
					ev.ActorRole != "customer" {
					t.Errorf("decoded = %+v", ev)
				}
				if !ev.OccurredAt.Equal(occurred) {
					t.Errorf("occurred_at = %v, want %v", ev.OccurredAt, occurred)
				}
				if !strings.Contains(string(ev.Payload), "ünïcode") {
					t.Errorf("payload = %s", ev.Payload)
				}
				if ev.LSN == "" {
					t.Error("no LSN")
				}
				return
			}
		}
	}
}
