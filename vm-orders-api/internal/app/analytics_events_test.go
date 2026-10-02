package app_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vayal-mikrogreenz/vm-go-common/httpx"
)

type orderEvent struct {
	EventType string
	ActorRole string
	Payload   map[string]any
}

func orderEvents(t *testing.T, pool *pgxpool.Pool, orderID uuid.UUID) []orderEvent {
	t.Helper()
	rows, err := pool.Query(context.Background(), `
		SELECT event_type, actor_role, payload FROM order_events_outbox
		WHERE aggregate_id = $1 ORDER BY created_at, id`, orderID)
	if err != nil {
		t.Fatalf("reading events: %v", err)
	}
	defer rows.Close()
	var out []orderEvent
	for rows.Next() {
		var e orderEvent
		var raw []byte
		if err := rows.Scan(&e.EventType, &e.ActorRole, &raw); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, &e.Payload); err != nil {
			t.Fatal(err)
		}
		out = append(out, e)
	}
	return out
}

// TestTransitionsWriteOneEventEach — CLAUDE.md §5.4. Each order transition
// writes exactly one analytics event in its own transaction, carrying the
// state AFTER the change; a refused transition writes none.
func TestTransitionsWriteOneEventEach(t *testing.T) {
	pool := testPool(t)
	router := testRouter(t, pool)
	orderID, _, _, _ := seedPaidOrder(t, pool)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM order_events_outbox WHERE aggregate_id = $1`, orderID)
	})

	call := func(path, body string) int {
		req := request(http.MethodPost, "/admin/orders/"+orderID.String()+path, httpx.RoleAdmin, body)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec.Code
	}

	if code := call("/process", ""); code != http.StatusOK {
		t.Fatalf("process: got %d", code)
	}
	// Refused: already processed. Must not write an event.
	if code := call("/process", ""); code != http.StatusConflict {
		t.Fatalf("second process: got %d, want 409", code)
	}
	if code := call("/cancel", `{"reason":"Customer Meena asked to cancel"}`); code != http.StatusOK {
		t.Fatalf("cancel: got %d", code)
	}

	events := orderEvents(t, pool, orderID)
	if len(events) != 2 {
		t.Fatalf("got %d events, want 2 (processed, cancelled): %+v", len(events), events)
	}
	if events[0].EventType != "order.processed" || events[0].Payload["status"] != "processed" ||
		events[0].ActorRole != "admin" {
		t.Errorf("first event = %s %s status=%v", events[0].EventType, events[0].ActorRole,
			events[0].Payload["status"])
	}
	cancelled := events[1]
	if cancelled.EventType != "order.cancelled" || cancelled.Payload["status"] != "cancelled" {
		t.Errorf("second event = %s status=%v", cancelled.EventType, cancelled.Payload["status"])
	}
	if cancelled.Payload["cancelled_from_status"] != "processed" {
		t.Errorf("cancelled_from_status = %v, want processed", cancelled.Payload["cancelled_from_status"])
	}
	if _, leaked := cancelled.Payload["cancel_reason"]; leaked {
		t.Error("the cancel reason (admin free text) reached the analytics payload")
	}
	if items, _ := cancelled.Payload["items"].([]any); len(items) != 3 {
		t.Errorf("items = %d, want 3", len(items))
	}
	if payouts, _ := cancelled.Payload["payouts"].([]any); len(payouts) != 2 {
		t.Errorf("payouts = %d, want 2", len(payouts))
	}
}
