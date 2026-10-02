package projector

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vayal-mikrogreenz/vm-go-common/config"
)

// These run against the real analytics schema: upsert guards, COALESCE on
// lifecycle columns and the pack-version index are properties of the SQL, and
// a mocked store would only test the mock.
func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if err := config.LoadRootDotEnv(); err != nil {
		t.Skipf("no root .env: %v", err)
	}
	url := os.Getenv("EVENTS_CONSUMER_DATABASE_URL")
	if url == "" {
		t.Skip("EVENTS_CONSUMER_DATABASE_URL is not set; run `make -C infra migrate-up` first")
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Skipf("cannot connect: %v", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		pool.Close()
		t.Skipf("database unreachable: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func testProjector(t *testing.T) (*Projector, *pgxpool.Pool) {
	pool := testPool(t)
	return New(pool, slog.New(slog.NewTextHandler(io.Discard, nil))), pool
}

func orderEvent(t *testing.T, orderID uuid.UUID, eventType, status string, at time.Time, extra map[string]any) Event {
	t.Helper()
	payload := map[string]any{
		"order_id": orderID, "order_number": "VM-PROJ-" + orderID.String()[:6],
		"customer_id": uuid.New(), "source": "checkout", "status": status,
		"subtotal_paise": 10000, "platform_fee_paise": 300, "delivery_fee_paise": 1500,
		"total_paise": 11800, "markup_paise": 1000,
		"placed_at": "2026-06-15T03:30:00Z", "processing_at": "2026-06-15T10:30:00Z",
		"delivery_day": "2026-06-16", "expected_delivery_date": "2026-06-17",
		"placed_date_ist": "2026-06-15", "placed_hour_ist": 9, "placed_before_cutoff": true,
		"ship_city": "Chennai", "ship_state": "Tamil Nadu", "ship_pincode": "600004",
		"items": []map[string]any{{
			"order_item_id": uuid.NewSHA1(orderID, []byte("line-1")), "supplier_id": uuid.Nil,
			"product_id": uuid.New(), "pack_option_id": uuid.New(), "product_name": "Tomato",
			"pack_label": "1 kg box", "weight_grams": 1000, "qty": 2,
			"unit_price_paise": 5000, "line_total_paise": 10000, "line_markup_paise": 1000,
		}},
		"payouts": []map[string]any{},
	}
	for k, v := range extra {
		payload[k] = v
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return Event{
		ID: uuid.New(), Table: OrderEvents, AggregateID: orderID, EventType: eventType,
		SchemaVersion: 1, OccurredAt: at, ActorRole: "system", Payload: raw, LSN: "0/TEST",
	}
}

func cleanupOrder(t *testing.T, pool *pgxpool.Pool, orderID uuid.UUID, events ...*Event) {
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = pool.Exec(ctx, `DELETE FROM orders WHERE order_id = $1`, orderID)
		for _, ev := range events {
			_, _ = pool.Exec(ctx, `DELETE FROM processed_events WHERE event_id = $1`, ev.ID)
			_, _ = pool.Exec(ctx, `DELETE FROM dead_letter_events WHERE event_id = $1`, ev.ID)
		}
	})
}

type orderRow struct {
	Status              string
	PaidAt, ProcessedAt *time.Time
	PaymentMethod       *string
	Payable             *int64
	LastEventType       string
	TotalGrams          int64
	Items               int
}

func readOrder(t *testing.T, pool *pgxpool.Pool, orderID uuid.UUID) orderRow {
	t.Helper()
	var r orderRow
	if err := pool.QueryRow(context.Background(), `
		SELECT status, paid_at, processed_at, payment_method, supplier_payable_paise,
		       last_event_type, total_grams,
		       (SELECT count(*) FROM order_items i WHERE i.order_id = o.order_id)
		FROM orders o WHERE order_id = $1`, orderID,
	).Scan(&r.Status, &r.PaidAt, &r.ProcessedAt, &r.PaymentMethod, &r.Payable,
		&r.LastEventType, &r.TotalGrams, &r.Items); err != nil {
		t.Fatalf("reading order: %v", err)
	}
	return r
}

// TestOrderLifecycle — placed, paid, processed in turn; then a replay of the
// paid event and a stale event, neither of which may move anything.
func TestOrderLifecycle(t *testing.T) {
	proj, pool := testProjector(t)
	ctx := context.Background()
	orderID := uuid.New()
	t0 := time.Date(2026, 6, 15, 3, 30, 0, 0, time.UTC)

	placed := orderEvent(t, orderID, "order.placed", "pending_payment", t0, nil)
	paid := orderEvent(t, orderID, "order.paid", "paid", t0.Add(2*time.Minute), map[string]any{
		"payment_method": "upi", "paid_via": "razorpay_webhook",
		"payouts": []map[string]any{{"supplier_id": uuid.New(), "amount_paise": 9700}},
	})
	// A processed event does not repeat the method; the row must keep it.
	processed := orderEvent(t, orderID, "order.processed", "processed", t0.Add(7*time.Hour),
		map[string]any{"payouts": []map[string]any{{"supplier_id": uuid.New(), "amount_paise": 9700}}})
	cleanupOrder(t, pool, orderID, &placed, &paid, &processed)

	for _, ev := range []Event{placed, paid, processed} {
		if _, err := proj.ApplyTransaction(ctx, []Event{ev}); err != nil {
			t.Fatalf("%s: %v", ev.EventType, err)
		}
	}
	got := readOrder(t, pool, orderID)
	if got.Status != "processed" || got.LastEventType != "order.processed" {
		t.Errorf("status = %s via %s", got.Status, got.LastEventType)
	}
	if got.PaidAt == nil || !got.PaidAt.Equal(t0.Add(2*time.Minute)) {
		t.Errorf("paid_at = %v", got.PaidAt)
	}
	if got.ProcessedAt == nil || !got.ProcessedAt.Equal(t0.Add(7*time.Hour)) {
		t.Errorf("processed_at = %v", got.ProcessedAt)
	}
	if got.PaymentMethod == nil || *got.PaymentMethod != "upi" {
		t.Errorf("payment_method = %v, want upi kept from the paid event", got.PaymentMethod)
	}
	if got.Payable == nil || *got.Payable != 9700 {
		t.Errorf("supplier_payable = %v", got.Payable)
	}
	if got.TotalGrams != 2000 || got.Items != 1 {
		t.Errorf("grams %d, items %d", got.TotalGrams, got.Items)
	}

	// Replay: Postgres resends after a reconnect. Same id → no-op.
	res, err := proj.ApplyTransaction(ctx, []Event{paid})
	if err != nil {
		t.Fatal(err)
	}
	if res.Duplicates != 1 || res.Applied != 0 {
		t.Errorf("replay = %+v, want one duplicate", res)
	}

	// Stale: a NEW event id carrying an older state must not roll the row back.
	stale := orderEvent(t, orderID, "order.snapshot", "paid", t0.Add(time.Hour), nil)
	cleanupOrder(t, pool, orderID, &stale)
	if _, err := proj.ApplyTransaction(ctx, []Event{stale}); err != nil {
		t.Fatal(err)
	}
	if after := readOrder(t, pool, orderID); after.Status != "processed" {
		t.Errorf("stale event moved status to %s", after.Status)
	}
}

// TestPoisonEventIsParked — an event no retry can fix is dead-lettered, and
// the good event in the same source transaction is still applied.
func TestPoisonEventIsParked(t *testing.T) {
	proj, pool := testProjector(t)
	ctx := context.Background()
	orderID := uuid.New()
	good := orderEvent(t, orderID, "order.placed", "pending_payment", time.Now(), nil)
	future := orderEvent(t, uuid.New(), "order.placed", "pending_payment", time.Now(), nil)
	future.SchemaVersion = 99
	broken := orderEvent(t, uuid.New(), "order.placed", "pending_payment", time.Now(), nil)
	broken.Payload = []byte(`{"order_id": "not-a-uuid"`)
	cleanupOrder(t, pool, orderID, &good, &future, &broken)

	res, err := proj.ApplyTransaction(ctx, []Event{future, broken, good})
	if err != nil {
		t.Fatalf("a poison event failed the transaction: %v", err)
	}
	if res.Applied != 1 || res.DeadLettered != 2 {
		t.Errorf("result = %+v, want 1 applied, 2 dead-lettered", res)
	}
	var parked int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM dead_letter_events WHERE event_id = ANY($1)`,
		[]uuid.UUID{future.ID, broken.ID}).Scan(&parked); err != nil {
		t.Fatal(err)
	}
	if parked != 2 {
		t.Errorf("parked = %d, want 2", parked)
	}
}

func productEvent(t *testing.T, productID uuid.UUID, eventType, status string, at time.Time, markupBPS int, packs ...map[string]any) Event {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"product_id": productID, "supplier_id": uuid.New(), "name": "Pomegranate",
		"type": "fruit", "status": status, "markup_bps": markupBPS, "source": "form",
		"created_at": "2026-06-01T00:00:00Z", "image_count": 2, "video_count": 0,
		"size_codes": []map[string]any{{
			"size_code_id": uuid.NewSHA1(productID, []byte("M")), "code": "M",
			"is_active": true, "packs": packs,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return Event{
		ID: uuid.New(), Table: ProductEvents, AggregateID: productID, EventType: eventType,
		SchemaVersion: 1, OccurredAt: at, ActorRole: "supplier", Payload: raw, LSN: "0/TEST",
	}
}

func pack(id uuid.UUID, price, customer int64) map[string]any {
	return map[string]any{"pack_option_id": id, "label": "1 kg", "weight_grams": 1000,
		"price_paise": price, "customer_price_paise": customer, "is_active": true}
}

// TestPackVersions — a price change closes the old pack version and opens a
// new one; an unchanged re-send opens nothing; a pack the grower removed is
// closed, and its id still resolves to the price it sold at.
func TestPackVersions(t *testing.T) {
	proj, pool := testProjector(t)
	ctx := context.Background()
	productID := uuid.New()
	packA, packB := uuid.New(), uuid.New()
	t0 := time.Date(2026, 6, 1, 4, 0, 0, 0, time.UTC)

	created := productEvent(t, productID, "product.created", "active", t0, 1000, pack(packA, 10000, 11000))
	repriced := productEvent(t, productID, "product.markup_changed", "active", t0.Add(time.Hour), 2000, pack(packA, 10000, 12000))
	same := productEvent(t, productID, "product.snapshot", "active", t0.Add(2*time.Hour), 2000, pack(packA, 10000, 12000))
	// A save replaces pack ids: A is gone, B is new.
	saved := productEvent(t, productID, "product.updated", "active", t0.Add(3*time.Hour), 2000, pack(packB, 15000, 18000))
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM products WHERE product_id = $1`, productID)
		_, _ = pool.Exec(ctx, `DELETE FROM product_packs WHERE product_id = $1`, productID)
		for _, ev := range []Event{created, repriced, same, saved} {
			_, _ = pool.Exec(ctx, `DELETE FROM processed_events WHERE event_id = $1`, ev.ID)
		}
	})

	for _, ev := range []Event{created, repriced, same, saved} {
		if _, err := proj.ApplyTransaction(ctx, []Event{ev}); err != nil {
			t.Fatalf("%s: %v", ev.EventType, err)
		}
	}

	type version struct {
		Pack     uuid.UUID
		From     time.Time
		To       *time.Time
		Customer int64
	}
	rows, err := pool.Query(ctx, `
		SELECT pack_option_id, valid_from, valid_to, customer_price_paise
		FROM product_packs WHERE product_id = $1 ORDER BY valid_from`, productID)
	if err != nil {
		t.Fatal(err)
	}
	var got []version
	for rows.Next() {
		var v version
		if err := rows.Scan(&v.Pack, &v.From, &v.To, &v.Customer); err != nil {
			t.Fatal(err)
		}
		got = append(got, v)
	}
	rows.Close()

	if len(got) != 3 {
		t.Fatalf("got %d versions, want 3 (A@11000, A@12000, B@18000): %+v", len(got), got)
	}
	wantTo := func(i int, to *time.Time) {
		t.Helper()
		switch {
		case to == nil && got[i].To != nil, to != nil && (got[i].To == nil || !got[i].To.Equal(*to)):
			t.Errorf("version %d valid_to = %v, want %v", i, got[i].To, to)
		}
	}
	t1, t3 := t0.Add(time.Hour), t0.Add(3*time.Hour)
	if got[0].Pack != packA || got[0].Customer != 11000 {
		t.Errorf("v0 = %+v", got[0])
	}
	wantTo(0, &t1)
	if got[1].Pack != packA || got[1].Customer != 12000 {
		t.Errorf("v1 = %+v", got[1])
	}
	wantTo(1, &t3)
	if got[2].Pack != packB || got[2].Customer != 18000 {
		t.Errorf("v2 = %+v", got[2])
	}
	wantTo(2, nil)

	var minPrice, maxPrice int64
	var markup int32
	if err := pool.QueryRow(ctx, `SELECT min_price_paise, max_price_paise, markup_bps
		FROM products WHERE product_id = $1`, productID).Scan(&minPrice, &maxPrice, &markup); err != nil {
		t.Fatal(err)
	}
	if minPrice != 18000 || maxPrice != 18000 || markup != 2000 {
		t.Errorf("product prices %d-%d markup %d", minPrice, maxPrice, markup)
	}
}

// TestSupplierProjection — a supplier is named, approved, then suspended;
// approved_at survives the suspension and a stale replay changes nothing.
func TestSupplierProjection(t *testing.T) {
	proj, pool := testProjector(t)
	ctx := context.Background()
	supplierID := uuid.New()
	t0 := time.Date(2026, 6, 1, 4, 0, 0, 0, time.UTC)
	approvedAt := t0.Add(time.Hour)

	event := func(eventType, status string, at time.Time, approved *time.Time) Event {
		raw, err := json.Marshal(map[string]any{
			"supplier_id": supplierID, "business_name": "Kaveri Greens", "status": status,
			"city": "Kumbakonam", "gst_registered": true, "commission_bps": 250,
			"approved_at": approved, "created_at": t0,
		})
		if err != nil {
			t.Fatal(err)
		}
		return Event{ID: uuid.New(), Table: SupplierEvents, AggregateID: supplierID,
			EventType: eventType, SchemaVersion: 1, OccurredAt: at, ActorRole: "admin",
			Payload: raw, LSN: "0/TEST"}
	}
	applied := event("supplier.applied", "pending", t0, nil)
	approved := event("supplier.approved", "approved", approvedAt, &approvedAt)
	suspended := event("supplier.suspended", "suspended", t0.Add(48*time.Hour), &approvedAt)
	stale := event("supplier.snapshot", "approved", t0.Add(2*time.Hour), &approvedAt)
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM suppliers WHERE supplier_id = $1`, supplierID)
		for _, ev := range []Event{applied, approved, suspended, stale} {
			_, _ = pool.Exec(ctx, `DELETE FROM processed_events WHERE event_id = $1`, ev.ID)
		}
	})
	for _, ev := range []Event{applied, approved, suspended, stale} {
		if _, err := proj.ApplyTransaction(ctx, []Event{ev}); err != nil {
			t.Fatalf("%s: %v", ev.EventType, err)
		}
	}

	var status, name string
	var approvedGot, suspendedGot *time.Time
	var commission *int32
	if err := pool.QueryRow(ctx, `SELECT status, business_name, approved_at, suspended_at,
		commission_bps FROM suppliers WHERE supplier_id = $1`, supplierID).
		Scan(&status, &name, &approvedGot, &suspendedGot, &commission); err != nil {
		t.Fatal(err)
	}
	if status != "suspended" || name != "Kaveri Greens" {
		t.Errorf("status %s, name %s — the stale snapshot must not undo the suspension", status, name)
	}
	if approvedGot == nil || !approvedGot.Equal(approvedAt) {
		t.Errorf("approved_at = %v", approvedGot)
	}
	if suspendedGot == nil || !suspendedGot.Equal(t0.Add(48*time.Hour)) {
		t.Errorf("suspended_at = %v", suspendedGot)
	}
	if commission == nil || *commission != 250 {
		t.Errorf("commission = %v", commission)
	}
}
