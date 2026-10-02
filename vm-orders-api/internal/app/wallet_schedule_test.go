package app_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vayal-mikrogreenz/vm-go-common/isttime"
	"github.com/vayal-mikrogreenz/vm-go-common/logging"
	"github.com/vayal-mikrogreenz/vm-go-common/money"

	"github.com/vayal-mikrogreenz/vm-orders-api/internal/api"
	"github.com/vayal-mikrogreenz/vm-orders-api/internal/app"
	"github.com/vayal-mikrogreenz/vm-orders-api/internal/razorpay"
)

// Integration tests for the prepaid wallet and the scheduled-delivery charging
// run — CLAUDE.md §6.7. Like the rest of this package they run against the
// development database and skip without one. Every row they write belongs to
// a customer id minted here, the run is narrowed to that customer, and all of
// it is deleted afterwards.

const webhookSecret = "whsec_wallet_test"

func walletTestConfig(catalogURL string, pauseAfter int) app.Config {
	return app.Config{
		InternalToken: internalToken,
		Pricing: app.Pricing{
			PlatformFeeBPS: 300, DeliveryFeePaise: money.Paise(1500),
			SupplierCommissionBPS: 300, DeliveryMarginPaise: money.Paise(500),
		},
		Fulfilment:    app.Fulfilment{CutoffHourIST: 16, ReservationTTL: 15 * time.Minute},
		Razorpay:      app.Razorpay{KeyID: "rzp_test", KeySecret: "secret", WebhookSecret: webhookSecret},
		Wallet:        app.Wallet{MinTopupPaise: 10_000, MaxTopupPaise: 1_000_000, MaxBalancePaise: 2_000_000},
		Schedules:     app.Schedules{ChargeLead: 30 * time.Minute, LowBalancePauseAfter: pauseAfter},
		CatalogAPIURL: catalogURL,
		ProfileAPIURL: "http://127.0.0.1:1", // commissions fall back to the default
		SupportEmail:  "support@example.test",
	}
}

func cleanupCustomer(t *testing.T, pool *pgxpool.Pool, customerID uuid.UUID) {
	t.Cleanup(func() {
		ctx := context.Background()
		for _, stmt := range []string{
			`DELETE FROM wallet_transactions WHERE customer_id = $1`,
			`DELETE FROM wallet_refunds WHERE customer_id = $1`,
			`DELETE FROM wallet_topups WHERE customer_id = $1`,
			`DELETE FROM outbox WHERE aggregate_id IN (SELECT id FROM schedules WHERE customer_id = $1)
			    OR aggregate_id IN (SELECT id FROM orders WHERE customer_id = $1)`,
			`DELETE FROM schedules WHERE customer_id = $1`,
			`DELETE FROM orders WHERE customer_id = $1`,
			`DELETE FROM wallets WHERE customer_id = $1`,
		} {
			if _, err := pool.Exec(ctx, stmt, customerID); err != nil {
				t.Logf("cleanup %q: %v", stmt, err)
			}
		}
	})
}

func balanceOf(t *testing.T, pool *pgxpool.Pool, customerID uuid.UUID) int64 {
	t.Helper()
	var balance int64
	if err := pool.QueryRow(context.Background(),
		`SELECT balance_paise FROM wallets WHERE customer_id = $1`, customerID).Scan(&balance); err != nil {
		t.Fatalf("reading balance: %v", err)
	}
	return balance
}

func postWebhook(t *testing.T, router http.Handler, eventID string, body []byte) int {
	t.Helper()
	req := request(http.MethodPost, "/webhooks/razorpay", "", string(body))
	req.Header.Set("X-Razorpay-Signature", razorpay.SignWebhookBody(body, webhookSecret))
	req.Header.Set("X-Razorpay-Event-Id", eventID)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	t.Cleanup(func() {
		// Webhook events are keyed by provider id, not by customer.
		_, _ = testPoolNoSkip.Exec(context.Background(),
			`DELETE FROM webhook_events WHERE provider_event_id = $1`, eventID)
	})
	return rec.Code
}

// testPoolNoSkip is the pool the webhook cleanup above uses; set per test.
var testPoolNoSkip *pgxpool.Pool

func capturedBody(rzpOrderID, paymentID string, amount int64) []byte {
	body, _ := json.Marshal(map[string]any{
		"event": "payment.captured",
		"payload": map[string]any{"payment": map[string]any{"entity": map[string]any{
			"id": paymentID, "order_id": rzpOrderID, "amount": amount,
			"currency": "INR", "status": "captured", "method": "upi",
		}}},
	})
	return body
}

// A captured top-up credits the wallet exactly once, whatever replays, and an
// amount we did not ask for credits nothing (CLAUDE.md §6.4 step 7).
func TestTopupWebhookCreditsOnceAndChecksTheAmount(t *testing.T) {
	pool := testPool(t)
	testPoolNoSkip = pool
	ctx := context.Background()
	customerID := uuid.New()
	cleanupCustomer(t, pool, customerID)

	logger := logging.NewTo(discard{}, "vm-orders-api-test", "error")
	router := app.NewRouter(walletTestConfig("http://127.0.0.1:1", 3), pool, logger)

	if _, err := pool.Exec(ctx, `INSERT INTO wallets (customer_id) VALUES ($1)`, customerID); err != nil {
		t.Fatal(err)
	}
	seedTopup := func(amount int64) string {
		rzpOrderID := "order_" + uuid.NewString()[:14]
		if _, err := pool.Exec(ctx, `
			INSERT INTO wallet_topups (customer_id, amount_paise, idempotency_key, razorpay_order_id)
			VALUES ($1, $2, $3, $4)`, customerID, amount, uuid.NewString(), rzpOrderID); err != nil {
			t.Fatal(err)
		}
		return rzpOrderID
	}

	// --- captured: credited ---------------------------------------------------
	first := seedTopup(50_000)
	paymentID := "pay_" + uuid.NewString()[:14]
	if code := postWebhook(t, router, "evt_"+uuid.NewString(), capturedBody(first, paymentID, 50_000)); code != http.StatusOK {
		t.Fatalf("capture: got %d, want 200", code)
	}
	if got := balanceOf(t, pool, customerID); got != 50_000 {
		t.Fatalf("balance after capture = %d, want 50000", got)
	}

	// --- a replay under a NEW event id gets past the event dedupe, and must
	//     still not credit twice -------------------------------------------------
	if code := postWebhook(t, router, "evt_"+uuid.NewString(), capturedBody(first, paymentID, 50_000)); code != http.StatusOK {
		t.Fatalf("replay: got %d, want 200", code)
	}
	if got := balanceOf(t, pool, customerID); got != 50_000 {
		t.Fatalf("balance after replay = %d, want 50000 (credited twice)", got)
	}

	// --- an amount we did not ask for credits nothing --------------------------
	//
	// Answered 200 and closed with the error, not 5xx: no retry can fix a
	// wrong amount, and Razorpay disables the whole webhook after 24 hours of
	// failures. The alert and the closed event are what reach a human.
	second := seedTopup(20_000)
	mismatchEvent := "evt_" + uuid.NewString()
	if code := postWebhook(t, router, mismatchEvent,
		capturedBody(second, "pay_"+uuid.NewString()[:14], 1)); code != http.StatusOK {
		t.Fatalf("mismatched amount: got %d, want 200 (a permanent failure)", code)
	}
	if got := balanceOf(t, pool, customerID); got != 50_000 {
		t.Fatalf("balance after a mismatched capture = %d, want 50000", got)
	}
	var closed bool
	var eventErr *string
	if err := pool.QueryRow(ctx, `
		SELECT processed_at IS NOT NULL, error FROM webhook_events WHERE provider_event_id = $1`,
		mismatchEvent).Scan(&closed, &eventErr); err != nil {
		t.Fatal(err)
	}
	if !closed || eventErr == nil {
		t.Fatalf("mismatch event: closed=%v error=%v, want closed with the error kept", closed, eventErr)
	}

	var entries int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM wallet_transactions WHERE customer_id = $1`, customerID).Scan(&entries); err != nil {
		t.Fatal(err)
	}
	if entries != 1 {
		t.Fatalf("ledger rows = %d, want 1", entries)
	}
}

// TestWebhookRetriesATemporaryFailure — an event whose last attempt failed
// for a temporary reason is processed again on Razorpay's redelivery, and an
// event that succeeded is not.
//
// Before, "already processed" meant "already received": a capture that hit a
// database blip left a paid customer unpaid, every redelivery was answered
// "already processed", and nothing fixed it but a human.
func TestWebhookRetriesATemporaryFailure(t *testing.T) {
	pool := testPool(t)
	testPoolNoSkip = pool
	ctx := context.Background()
	customerID := uuid.New()
	cleanupCustomer(t, pool, customerID)

	logger := logging.NewTo(discard{}, "vm-orders-api-test", "error")
	router := app.NewRouter(walletTestConfig("http://127.0.0.1:1", 3), pool, logger)

	if _, err := pool.Exec(ctx, `INSERT INTO wallets (customer_id) VALUES ($1)`, customerID); err != nil {
		t.Fatal(err)
	}
	rzpOrderID := "order_" + uuid.NewString()[:14]
	if _, err := pool.Exec(ctx, `
		INSERT INTO wallet_topups (customer_id, amount_paise, idempotency_key, razorpay_order_id)
		VALUES ($1, 30000, $2, $3)`, customerID, uuid.NewString(), rzpOrderID); err != nil {
		t.Fatal(err)
	}

	// What a first delivery that hit a database blip leaves behind: recorded,
	// open (processed_at NULL), the error kept, the wallet not yet credited.
	eventID := "evt_" + uuid.NewString()
	body := capturedBody(rzpOrderID, "pay_"+uuid.NewString()[:14], 30_000)
	if _, err := pool.Exec(ctx, `
		INSERT INTO webhook_events (provider_event_id, event_type, payload, error)
		VALUES ($1, 'payment.captured', $2, 'conn reset by peer')`, eventID, body); err != nil {
		t.Fatal(err)
	}

	// Razorpay redelivers the same event: it is processed this time.
	if code := postWebhook(t, router, eventID, body); code != http.StatusOK {
		t.Fatalf("redelivery: got %d, want 200", code)
	}
	if got := balanceOf(t, pool, customerID); got != 30_000 {
		t.Fatalf("balance after the redelivery = %d, want 30000 (the retry was not processed)", got)
	}
	var attempts int
	var closed bool
	var eventErr *string
	if err := pool.QueryRow(ctx, `
		SELECT attempts, processed_at IS NOT NULL, error FROM webhook_events WHERE provider_event_id = $1`,
		eventID).Scan(&attempts, &closed, &eventErr); err != nil {
		t.Fatal(err)
	}
	if attempts != 2 || !closed || eventErr != nil {
		t.Fatalf("after the retry: attempts=%d closed=%v error=%v, want 2, closed, error cleared",
			attempts, closed, eventErr)
	}

	// Once it has succeeded it is closed: a further redelivery changes nothing.
	if code := postWebhook(t, router, eventID, body); code != http.StatusOK {
		t.Fatalf("replay of a processed event: got %d, want 200", code)
	}
	if got := balanceOf(t, pool, customerID); got != 30_000 {
		t.Fatalf("balance after a replay = %d, want 30000 (credited twice)", got)
	}
	if err := pool.QueryRow(ctx,
		`SELECT attempts FROM webhook_events WHERE provider_event_id = $1`, eventID).Scan(&attempts); err != nil {
		t.Fatal(err)
	}
	if attempts != 2 {
		t.Fatalf("a closed event counted another attempt: %d, want 2", attempts)
	}
}

// fakeCatalog serves today's catalogue and grants every reservation.
type fakeCatalog struct {
	grades   []map[string]any
	reserved int
	settled  []string
}

func (f *fakeCatalog) server(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/internal/units/today":
			_ = json.NewEncoder(w).Encode(map[string]any{"grades": f.grades})
		case r.Method == http.MethodPost && r.URL.Path == "/internal/reservations":
			var body struct {
				Lines []struct {
					SizeCodeID string `json:"size_code_id"`
					Grams      int32  `json:"grams"`
				} `json:"lines"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			f.reserved++
			holds := []map[string]any{}
			for _, line := range body.Lines {
				holds = append(holds, map[string]any{
					"hold_id": uuid.NewString(), "product_id": productID.String(),
					"grams": line.Grams, "expires_at": time.Now().Add(15 * time.Minute).Format(time.RFC3339),
				})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"holds": holds})
		case r.Method == http.MethodPost && r.URL.Path == "/internal/reservations/settle":
			var body struct {
				Outcome string `json:"outcome"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			f.settled = append(f.settled, body.Outcome)
			_, _ = w.Write([]byte(`{}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

var (
	productID  = uuid.MustParse("7e000000-0000-0000-0000-0000000000aa")
	supplierID = uuid.MustParse("7e000000-0000-0000-0000-0000000000bb")
	gradeL     = uuid.MustParse("7e000000-0000-0000-0000-0000000000c1")
	gradeM     = uuid.MustParse("7e000000-0000-0000-0000-0000000000c2")
	packL      = uuid.MustParse("7e000000-0000-0000-0000-0000000000d1")
	packM      = uuid.MustParse("7e000000-0000-0000-0000-0000000000d2")
)

// Today: 1.5 kg of L left (so one 1 kg pack fits, not two), and M sold out.
func pomegranateToday() []map[string]any {
	unit := func(id uuid.UUID, sizeCode uuid.UUID, purchasable bool) map[string]any {
		return map[string]any{
			"id": id.String(), "size_code_id": sizeCode.String(), "supplier_id": supplierID.String(),
			"label": "1 Kg Box", "weight_grams": 1000, "price_paise": 20_000,
			"markup_paise": 0, "markup_bps": 0, "purchasable": purchasable,
		}
	}
	return []map[string]any{
		{"product_id": productID.String(), "product_name": "Pomegranate", "size_code_id": gradeL.String(),
			"size_code": "L", "product_type": "fruit", "remaining_grams": 1500,
			"units": []map[string]any{unit(packL, gradeL, true)}},
		{"product_id": productID.String(), "product_name": "Pomegranate", "size_code_id": gradeM.String(),
			"size_code": "M", "product_type": "fruit", "remaining_grams": 0,
			"units": []map[string]any{unit(packM, gradeM, false)}},
	}
}

// seedSchedule makes a daily schedule whose next delivery is processed today:
// two packs of L and one of M.
func seedSchedule(t *testing.T, pool *pgxpool.Pool, customerID uuid.UUID, delivery time.Time) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	var scheduleID uuid.UUID
	address := `{"id":"a","recipient_name":"Test","phone":"9000000000","line1":"1 Street","city":"Ooty","state":"TN","pincode":"643001"}`
	if err := pool.QueryRow(ctx, `
		INSERT INTO schedules (customer_id, address_id, address_snapshot, frequency,
		                       start_date, next_delivery_date)
		VALUES ($1, $2, $3::jsonb, 'daily', $4, $4) RETURNING id`,
		customerID, uuid.New(), address, delivery).Scan(&scheduleID); err != nil {
		t.Fatalf("seeding schedule: %v", err)
	}
	for _, line := range []struct {
		unit uuid.UUID
		qty  int
		code string
	}{{packL, 2, "L"}, {packM, 1, "M"}} {
		if _, err := pool.Exec(ctx, `
			INSERT INTO schedule_items (schedule_id, product_id, product_unit_id, qty,
			                            product_name_snapshot, unit_label_snapshot, size_code_snapshot)
			VALUES ($1, $2, $3, $4, 'Pomegranate', '1 Kg Box', $5)`,
			scheduleID, productID, line.unit, line.qty, line.code); err != nil {
			t.Fatalf("seeding schedule item: %v", err)
		}
	}
	return scheduleID
}

func chargeWindowClock() func() time.Time {
	today := isttime.Today()
	at := time.Date(today.Year(), today.Month(), today.Day(), 15, 40, 0, 0, isttime.Location())
	return func() time.Time { return at }
}

// The run charges the wallet for WHAT IS THERE: one of two L packs, no M, at
// today's price — one order, paid from the wallet, dated for the delivery the
// customer chose, and the schedule moved to its next date. A second run the
// same afternoon charges nothing more.
func TestScheduleRunDeliversWhatIsThereFromTheWallet(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	customerID := uuid.New()
	cleanupCustomer(t, pool, customerID)

	catalog := &fakeCatalog{grades: pomegranateToday()}
	srv := catalog.server(t)
	logger := logging.NewTo(discard{}, "vm-orders-api-test", "error")
	cfg := walletTestConfig(srv.URL, 3)

	if _, err := pool.Exec(ctx,
		`INSERT INTO wallets (customer_id, balance_paise) VALUES ($1, 100000)`, customerID); err != nil {
		t.Fatal(err)
	}
	delivery := isttime.AddDays(isttime.Today(), 2)
	scheduleID := seedSchedule(t, pool, customerID, delivery)

	runner := api.NewScheduleRunner(app.NewAPI(cfg, pool, logger), time.Minute, logger).
		WithClock(chargeWindowClock()).ForCustomer(customerID)
	if err := runner.RunOnce(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}

	// --- the occurrence ------------------------------------------------------
	var status string
	var orderID *uuid.UUID
	var note *string
	if err := pool.QueryRow(ctx, `
		SELECT status, order_id, note FROM schedule_occurrences
		WHERE schedule_id = $1 AND delivery_date = $2`, scheduleID, delivery).
		Scan(&status, &orderID, &note); err != nil {
		t.Fatalf("no occurrence recorded: %v", err)
	}
	if status != "placed" || orderID == nil {
		t.Fatalf("occurrence = %s (order %v), want placed", status, orderID)
	}
	if note == nil || !strings.Contains(*note, "1 of 2") || !strings.Contains(*note, "(M)") {
		t.Fatalf("note should name the reduced L line and the missing M line, got %v", note)
	}

	// --- the order: one L pack at ₹200 + 3% + ₹15 = ₹221 ------------------------
	const wantTotal = 20_000 + 600 + 1_500
	var orderStatus string
	var total int64
	var expected time.Time
	var linkedSchedule *uuid.UUID
	if err := pool.QueryRow(ctx, `
		SELECT status, total_paise, expected_delivery_date, schedule_id FROM orders WHERE id = $1`,
		*orderID).Scan(&orderStatus, &total, &expected, &linkedSchedule); err != nil {
		t.Fatal(err)
	}
	if orderStatus != "paid" {
		t.Errorf("order status = %s, want paid", orderStatus)
	}
	if total != wantTotal {
		t.Errorf("order total = %d, want %d", total, wantTotal)
	}
	if isttime.FormatISODate(expected) != isttime.FormatISODate(delivery) {
		t.Errorf("expected delivery = %s, want %s",
			isttime.FormatISODate(expected), isttime.FormatISODate(delivery))
	}
	if linkedSchedule == nil || *linkedSchedule != scheduleID {
		t.Errorf("order not linked to its schedule")
	}

	var payouts, walletPayments int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM supplier_payouts WHERE order_id = $1`, *orderID).Scan(&payouts)
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM payments WHERE order_id = $1 AND provider = 'wallet' AND status = 'captured'`, *orderID).Scan(&walletPayments)
	if payouts != 1 || walletPayments != 1 {
		t.Errorf("payouts = %d, wallet payments = %d; want 1 and 1", payouts, walletPayments)
	}

	// --- the wallet ----------------------------------------------------------
	if got := balanceOf(t, pool, customerID); got != 100_000-wantTotal {
		t.Errorf("balance = %d, want %d", got, 100_000-wantTotal)
	}
	var debit int64
	if err := pool.QueryRow(ctx, `
		SELECT amount_paise FROM wallet_transactions
		WHERE customer_id = $1 AND kind = 'order_debit' AND order_id = $2`,
		customerID, *orderID).Scan(&debit); err != nil {
		t.Fatalf("no debit in the ledger: %v", err)
	}
	if debit != -wantTotal {
		t.Errorf("ledger debit = %d, want %d", debit, -wantTotal)
	}

	// --- stock committed in the catalogue ------------------------------------
	if catalog.reserved != 1 || len(catalog.settled) != 1 || catalog.settled[0] != "committed" {
		t.Errorf("catalogue: reserved %d, settled %v; want 1 and [committed]",
			catalog.reserved, catalog.settled)
	}

	// --- the schedule moved on ------------------------------------------------
	var next time.Time
	if err := pool.QueryRow(ctx, `SELECT next_delivery_date FROM schedules WHERE id = $1`,
		scheduleID).Scan(&next); err != nil {
		t.Fatal(err)
	}
	if isttime.FormatISODate(next) != isttime.FormatISODate(isttime.AddDays(delivery, 1)) {
		t.Errorf("next delivery = %s, want the day after", isttime.FormatISODate(next))
	}

	// --- and a second run charges nothing more ------------------------------------
	if err := runner.RunOnce(ctx); err != nil {
		t.Fatalf("second run: %v", err)
	}
	var orders int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM orders WHERE customer_id = $1`, customerID).Scan(&orders)
	if orders != 1 {
		t.Fatalf("orders after a second run = %d, want 1", orders)
	}
	if got := balanceOf(t, pool, customerID); got != 100_000-wantTotal {
		t.Fatalf("balance after a second run = %d (charged twice)", got)
	}
}

// A wallet that cannot cover the delivery is not charged, holds no stock, and
// — at the configured count — pauses the schedule.
func TestScheduleRunSkipsAndPausesOnLowBalance(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	customerID := uuid.New()
	cleanupCustomer(t, pool, customerID)

	catalog := &fakeCatalog{grades: pomegranateToday()}
	srv := catalog.server(t)
	logger := logging.NewTo(discard{}, "vm-orders-api-test", "error")

	if _, err := pool.Exec(ctx,
		`INSERT INTO wallets (customer_id, balance_paise) VALUES ($1, 5000)`, customerID); err != nil {
		t.Fatal(err)
	}
	delivery := isttime.AddDays(isttime.Today(), 2)
	scheduleID := seedSchedule(t, pool, customerID, delivery)

	runner := api.NewScheduleRunner(app.NewAPI(walletTestConfig(srv.URL, 1), pool, logger),
		time.Minute, logger).WithClock(chargeWindowClock()).ForCustomer(customerID)
	if err := runner.RunOnce(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}

	var occurrence, scheduleStatus string
	var skips int
	if err := pool.QueryRow(ctx, `
		SELECT so.status, s.status, s.low_balance_skips
		FROM schedule_occurrences so JOIN schedules s ON s.id = so.schedule_id
		WHERE so.schedule_id = $1 AND so.delivery_date = $2`, scheduleID, delivery).
		Scan(&occurrence, &scheduleStatus, &skips); err != nil {
		t.Fatalf("no occurrence: %v", err)
	}
	if occurrence != "skipped_low_balance" {
		t.Errorf("occurrence = %s, want skipped_low_balance", occurrence)
	}
	if scheduleStatus != "paused" || skips != 1 {
		t.Errorf("schedule = %s with %d skips, want paused with 1", scheduleStatus, skips)
	}
	if catalog.reserved != 0 {
		t.Errorf("a low wallet reserved stock %d time(s)", catalog.reserved)
	}
	if got := balanceOf(t, pool, customerID); got != 5000 {
		t.Errorf("balance = %d, want untouched 5000", got)
	}
	var orders int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM orders WHERE customer_id = $1`, customerID).Scan(&orders)
	if orders != 0 {
		t.Errorf("orders = %d, want 0", orders)
	}
	_ = fmt.Sprint // keep fmt for ad-hoc debugging output
}
