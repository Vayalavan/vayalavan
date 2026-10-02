package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vayal-mikrogreenz/vm-go-common/httpx"
)

type productEvent struct {
	EventType string
	ActorRole string
	Payload   map[string]any
}

func productEvents(t *testing.T, pool *pgxpool.Pool, productID uuid.UUID) []productEvent {
	t.Helper()
	rows, err := pool.Query(context.Background(), `
		SELECT event_type, actor_role, payload FROM product_events_outbox
		WHERE aggregate_id = $1 ORDER BY created_at, id`, productID)
	if err != nil {
		t.Fatalf("reading events: %v", err)
	}
	defer rows.Close()
	var out []productEvent
	for rows.Next() {
		var e productEvent
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

func adminCall(t *testing.T, router http.Handler, method, path, body string) int {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(httpx.InternalTokenHeader, internalToken)
	req.Header.Set(httpx.UserRoleHeader, httpx.RoleAdmin)
	req.Header.Set(httpx.UserIDHeader, uuid.NewString())
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec.Code
}

// TestProductChangesWriteOneEventEach — CLAUDE.md §5.4. A markup change and an
// archive each write one event carrying the product AFTER the change; an
// archive that changes nothing writes none.
func TestProductChangesWriteOneEventEach(t *testing.T) {
	pool := testPool(t)
	router := testRouter(t, pool)
	productID, _ := seedGradedProduct(t, pool,
		gradeStock{code: "M", grams: 1000}, gradeStock{code: "XL", grams: 2000})
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM product_events_outbox WHERE aggregate_id = $1`, productID)
	})

	base := "/admin/products/" + productID.String()
	if code := adminCall(t, router, http.MethodPut, base+"/markup", `{"markup_bps":2000}`); code != http.StatusOK {
		t.Fatalf("markup: got %d", code)
	}
	if code := adminCall(t, router, http.MethodPost, base+"/archive", ""); code != http.StatusOK {
		t.Fatalf("archive: got %d", code)
	}
	// Already archived: nothing changes, so nothing is reported.
	adminCall(t, router, http.MethodPost, base+"/archive", "")

	events := productEvents(t, pool, productID)
	if len(events) != 2 {
		t.Fatalf("got %d events, want 2: %+v", len(events), events)
	}
	markup, archived := events[0], events[1]
	if markup.EventType != "product.markup_changed" || markup.ActorRole != "admin" {
		t.Errorf("first = %s by %s", markup.EventType, markup.ActorRole)
	}
	if markup.Payload["markup_bps"] != float64(2000) {
		t.Errorf("markup_bps = %v", markup.Payload["markup_bps"])
	}
	codes, _ := markup.Payload["size_codes"].([]any)
	if len(codes) != 2 {
		t.Fatalf("size_codes = %d, want 2", len(codes))
	}
	packs, _ := codes[0].(map[string]any)["packs"].([]any)
	if len(packs) != 1 {
		t.Fatalf("packs = %d, want 1", len(packs))
	}
	pack := packs[0].(map[string]any)
	// 10000 paise at 20% markup.
	if pack["price_paise"] != float64(10000) || pack["customer_price_paise"] != float64(12000) {
		t.Errorf("pack prices = %v / %v, want 10000 / 12000",
			pack["price_paise"], pack["customer_price_paise"])
	}
	if archived.EventType != "product.archived" || archived.Payload["status"] != "archived" {
		t.Errorf("second = %s status=%v", archived.EventType, archived.Payload["status"])
	}
	if _, leaked := archived.Payload["description"]; leaked {
		t.Error("the description reached the analytics payload")
	}
}
