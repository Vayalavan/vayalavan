package app_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vayal-mikrogreenz/vm-go-common/config"
	"github.com/vayal-mikrogreenz/vm-go-common/httpx"
	"github.com/vayal-mikrogreenz/vm-go-common/isttime"
	"github.com/vayal-mikrogreenz/vm-go-common/logging"
	"github.com/vayal-mikrogreenz/vm-go-common/money"

	"github.com/vayal-mikrogreenz/vm-orders-api/internal/app"
	"github.com/vayal-mikrogreenz/vm-orders-api/internal/pricing"
	"github.com/vayal-mikrogreenz/vm-orders-api/internal/store"
)

const internalToken = "admin-test-token"

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }

// testPool connects to the development database, or skips.
//
// Integration tests on purpose: the properties under test — that Postgres
// serialises two settlement runs, and that payout rows agree with order_items
// — are properties of the database, not of Go code.
func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	if err := config.LoadRootDotEnv(); err != nil {
		t.Skipf("no root .env: %v", err)
	}
	url := os.Getenv("ORDERS_DATABASE_URL")
	if url == "" {
		t.Skip("ORDERS_DATABASE_URL is not set; run `make -C infra up` first")
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

func testRouter(t *testing.T, pool *pgxpool.Pool) http.Handler {
	t.Helper()
	logger := logging.NewTo(discard{}, "vm-orders-api-test", "error")
	return app.NewRouter(app.Config{
		InternalToken: internalToken,
		Pricing: app.Pricing{
			PlatformFeeBPS: 300, DeliveryFeePaise: money.Paise(1500),
			SupplierCommissionBPS: 300, DeliveryMarginPaise: money.Paise(500),
		},
		Fulfilment:    app.Fulfilment{CutoffHourIST: 16, ReservationTTL: 15 * time.Minute},
		CatalogAPIURL: "http://127.0.0.1:1", // never reached by these tests
		ProfileAPIURL: "http://127.0.0.1:1",
		SupportEmail:  "support@example.test",
	}, pool, logger)
}

// request builds a call carrying the gateway's internal token plus an identity.
func request(method, path, role string, body string) *http.Request {
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(httpx.InternalTokenHeader, internalToken)
	if role != "" {
		req.Header.Set(httpx.UserRoleHeader, role)
		req.Header.Set(httpx.UserIDHeader, uuid.NewString())
	}
	return req
}

// seedPaidOrder creates a paid order with items from two suppliers and the
// matching payout rows, exactly as the capture path would.
func seedPaidOrder(t *testing.T, pool *pgxpool.Pool) (orderID uuid.UUID, payouts []uuid.UUID, supplierA, supplierB uuid.UUID) {
	t.Helper()
	ctx := context.Background()

	supplierA, supplierB = uuid.New(), uuid.New()
	customerID := uuid.New()
	now := time.Now()

	// Two lines from supplier A (24100 + 12000) and one from B (8500).
	const subtotal, platformFee, deliveryFee = 44600, 1338, 1500
	total := int64(subtotal + platformFee + deliveryFee)

	err := pool.QueryRow(ctx, `
		INSERT INTO orders (
			order_number, customer_id, status, address_snapshot,
			subtotal_paise, platform_fee_paise, delivery_fee_paise, total_paise,
			placed_at, processing_at, delivery_day, expected_delivery_date
		) VALUES ($1,$2,'paid','{}'::jsonb,$3,$4,$5,$6,$7,$8,$9,$10)
		RETURNING id`,
		"VM-TEST-"+uuid.NewString()[:8], customerID,
		subtotal, platformFee, deliveryFee, total,
		now, now.Add(time.Hour), now.AddDate(0, 0, 1), now.AddDate(0, 0, 2),
	).Scan(&orderID)
	if err != nil {
		t.Fatalf("seeding order: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM orders WHERE id = $1`, orderID)
	})

	lines := []struct {
		supplier  uuid.UUID
		unitPaise int64
		qty       int32
		lineTotal int64
	}{
		{supplierA, 12050, 2, 24100},
		{supplierB, 8500, 1, 8500},
		{supplierA, 4000, 3, 12000},
	}
	for i, line := range lines {
		if _, err := pool.Exec(ctx, `
			INSERT INTO order_items (
				order_id, supplier_id, product_id, product_unit_id,
				product_name_snapshot, unit_label_snapshot, weight_grams,
				unit_price_paise, qty, line_total_paise
			) VALUES ($1,$2,$3,$4,$5,'1 kg',1000,$6,$7,$8)`,
			orderID, line.supplier, uuid.New(), uuid.New(),
			fmt.Sprintf("Test Item %d", i), line.unitPaise, line.qty, line.lineTotal,
		); err != nil {
			t.Fatalf("seeding order item: %v", err)
		}
	}

	// Payout rows, as the capture path creates them: one per supplier.
	for _, entry := range []struct {
		supplier uuid.UUID
		amount   int64
	}{{supplierA, 36100}, {supplierB, 8500}} {
		var payoutID uuid.UUID
		if err := pool.QueryRow(ctx, `
			INSERT INTO supplier_payouts (supplier_id, order_id, amount_paise)
			VALUES ($1,$2,$3) RETURNING id`,
			entry.supplier, orderID, entry.amount).Scan(&payoutID); err != nil {
			t.Fatalf("seeding payout: %v", err)
		}
		payouts = append(payouts, payoutID)
	}
	return orderID, payouts, supplierA, supplierB
}

func markPaidBody(ids []uuid.UUID, reference string) string {
	quoted := make([]string, 0, len(ids))
	for _, id := range ids {
		quoted = append(quoted, `"`+id.String()+`"`)
	}
	return fmt.Sprintf(`{"payout_ids":[%s],"reference_no":%q,"notes":"settlement run"}`,
		strings.Join(quoted, ","), reference)
}

// TestMarkPaidRejectsDoublePayment is the money-safety property: a settlement
// run must never pay a supplier twice for the same order.
func TestMarkPaidRejectsDoublePayment(t *testing.T) {
	pool := testPool(t)
	router := testRouter(t, pool)
	_, payouts, _, _ := seedPaidOrder(t, pool)

	// A real UTR identifies one NEFT transfer and is never reused, so the test
	// uses a fresh one per run. A hardcoded literal made the audit assertion
	// below match every previous run's rows as well — these tests share the
	// developer database and nothing truncates it.
	firstRef := "UTR-FIRST-" + uuid.NewString()[:8]

	// --- first settlement succeeds ------------------------------------------
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, request(http.MethodPost, "/admin/payouts/mark-paid",
		httpx.RoleAdmin, markPaidBody(payouts, firstRef)))

	if rec.Code != http.StatusOK {
		t.Fatalf("first mark-paid = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var first struct {
		Settled    int   `json:"settled"`
		TotalPaise int64 `json:"total_paise"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &first); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if first.Settled != len(payouts) {
		t.Errorf("settled = %d, want %d", first.Settled, len(payouts))
	}
	if first.TotalPaise != 44600 {
		t.Errorf("total = %d, want 44600 (the order subtotal)", first.TotalPaise)
	}

	// --- replaying the SAME batch is rejected --------------------------------
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, request(http.MethodPost, "/admin/payouts/mark-paid",
		httpx.RoleAdmin, markPaidBody(payouts, "UTR-SECOND-"+uuid.NewString()[:8])))

	if rec.Code != http.StatusConflict {
		t.Fatalf("replayed mark-paid = %d, want 409: %s", rec.Code, rec.Body.String())
	}
	var conflict struct {
		Error struct {
			Code    string         `json:"code"`
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &conflict); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if conflict.Error.Code != "PAYOUT_ALREADY_PAID" {
		t.Errorf("code = %q, want PAYOUT_ALREADY_PAID", conflict.Error.Code)
	}
	// The admin needs to see WHICH ones and with what reference.
	if _, ok := conflict.Error.Details["already_paid"]; !ok {
		t.Error("409 did not name the already-paid payouts")
	}

	// --- nothing was changed by the rejected replay --------------------------
	for _, id := range payouts {
		var reference string
		var markedBy uuid.UUID
		if err := pool.QueryRow(context.Background(),
			`SELECT reference_no, marked_paid_by FROM supplier_payouts WHERE id = $1`,
			id).Scan(&reference, &markedBy); err != nil {
			t.Fatalf("reading payout: %v", err)
		}
		if reference != firstRef {
			t.Errorf("reference_no = %q — the replay overwrote the original settlement",
				reference)
		}
	}

	// One audit row per settlement batch — not per payout — and none at all
	// for the rejected replay.
	var audits int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM admin_audit_log WHERE action = 'payout.marked_paid'
		 AND after->>'reference_no' = $1`, firstRef).Scan(&audits); err != nil {
		t.Fatalf("counting audit rows: %v", err)
	}
	if audits != 1 {
		t.Errorf("got %d audit rows for the settlement, want 1", audits)
	}
}

// TestMarkPaidIsAllOrNothing — a batch containing one already-paid row must
// leave the rest untouched, not settle them and report a partial success.
func TestMarkPaidIsAllOrNothing(t *testing.T) {
	pool := testPool(t)
	router := testRouter(t, pool)
	_, payoutsA, _, _ := seedPaidOrder(t, pool)
	_, payoutsB, _, _ := seedPaidOrder(t, pool)

	// Settle the first order.
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, request(http.MethodPost, "/admin/payouts/mark-paid",
		httpx.RoleAdmin, markPaidBody(payoutsA, "UTR-A-001")))
	if rec.Code != http.StatusOK {
		t.Fatalf("setup settlement failed: %s", rec.Body.String())
	}

	// Now submit a batch mixing settled and unsettled rows.
	mixed := append(append([]uuid.UUID{}, payoutsA...), payoutsB...)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, request(http.MethodPost, "/admin/payouts/mark-paid",
		httpx.RoleAdmin, markPaidBody(mixed, "UTR-MIXED-003")))

	if rec.Code != http.StatusConflict {
		t.Fatalf("mixed batch = %d, want 409", rec.Code)
	}

	// The untouched order's payouts must still be pending.
	for _, id := range payoutsB {
		var status string
		if err := pool.QueryRow(context.Background(),
			`SELECT status FROM supplier_payouts WHERE id = $1`, id).Scan(&status); err != nil {
			t.Fatalf("reading payout: %v", err)
		}
		if status != "pending" {
			t.Errorf("payout status = %q, want pending — the rejected batch partially applied",
				status)
		}
	}
}

// TestPayoutsReconcileWithOrderItems — the amount we promise a supplier must
// equal the sum of their lines on that order. If these ever diverge, someone
// is paid the wrong amount and no downstream check would notice.
func TestPayoutsReconcileWithOrderItems(t *testing.T) {
	pool := testPool(t)
	orderID, _, supplierA, supplierB := seedPaidOrder(t, pool)
	ctx := context.Background()

	rows, err := pool.Query(ctx, `
		SELECT p.supplier_id, p.amount_paise, i.items_total
		FROM supplier_payouts p
		JOIN (
			SELECT supplier_id, sum(line_total_paise)::bigint AS items_total
			FROM order_items WHERE order_id = $1 GROUP BY supplier_id
		) i ON i.supplier_id = p.supplier_id
		WHERE p.order_id = $1`, orderID)
	if err != nil {
		t.Fatalf("reconciling: %v", err)
	}
	defer rows.Close()

	seen := 0
	for rows.Next() {
		var supplierID uuid.UUID
		var payout, itemsTotal int64
		if err := rows.Scan(&supplierID, &payout, &itemsTotal); err != nil {
			t.Fatalf("scanning: %v", err)
		}
		seen++
		if payout != itemsTotal {
			t.Errorf("supplier %s: payout %d != order_items sum %d",
				supplierID, payout, itemsTotal)
		}
	}
	if seen != 2 {
		t.Fatalf("reconciled %d suppliers, want 2", seen)
	}

	// And the two shares must add up to the order subtotal exactly — our fees
	// sit on top and are never deducted from what a supplier is owed.
	var payoutSum, subtotal int64
	if err := pool.QueryRow(ctx,
		`SELECT coalesce(sum(amount_paise),0) FROM supplier_payouts WHERE order_id = $1`,
		orderID).Scan(&payoutSum); err != nil {
		t.Fatalf("summing payouts: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT subtotal_paise FROM orders WHERE id = $1`, orderID).Scan(&subtotal); err != nil {
		t.Fatalf("reading order: %v", err)
	}
	if payoutSum != subtotal {
		t.Errorf("payouts sum to %d, want the subtotal %d", payoutSum, subtotal)
	}

	// Cross-check against the pricing calculator, so the stored figures and the
	// code that produced them cannot drift apart.
	lines := []pricing.Line{
		{SupplierID: supplierA, UnitPricePaise: 12050, Qty: 2},
		{SupplierID: supplierB, UnitPricePaise: 8500, Qty: 1},
		{SupplierID: supplierA, UnitPricePaise: 4000, Qty: 3},
	}
	if got := pricing.SupplierPayable(lines, supplierA); got != 36100 {
		t.Errorf("calculator says supplier A is owed %d, stored payout says 36100", got)
	}
}

// TestAdminRoutesRejectNonAdmins is the blanket authorisation check: EVERY
// admin route, for every non-admin role, plus anonymous.
func TestAdminRoutesRejectNonAdmins(t *testing.T) {
	pool := testPool(t)
	router := testRouter(t, pool)
	someID := uuid.NewString()

	routes := []struct{ method, path, body string }{
		{http.MethodGet, "/admin/dashboard", ""},
		{http.MethodGet, "/admin/orders/", ""},
		{http.MethodGet, "/admin/orders/" + someID, ""},
		{http.MethodPost, "/admin/orders/" + someID + "/dispatch", ""},
		{http.MethodPost, "/admin/orders/" + someID + "/cancel", `{"reason":"test"}`},
		{http.MethodGet, "/admin/payouts/summary", ""},
		{http.MethodGet, "/admin/payouts/", ""},
		{http.MethodGet, "/admin/payouts/export.csv", ""},
		{http.MethodPost, "/admin/payouts/mark-paid",
			`{"payout_ids":["` + someID + `"],"reference_no":"UTR-X"}`},
	}

	for _, route := range routes {
		for _, role := range []string{httpx.RoleCustomer, httpx.RoleSupplier} {
			t.Run(route.method+" "+route.path+" as "+role, func(t *testing.T) {
				rec := httptest.NewRecorder()
				router.ServeHTTP(rec, request(route.method, route.path, role, route.body))

				if rec.Code != http.StatusForbidden {
					t.Errorf("%s %s as %s = %d, want 403",
						route.method, route.path, role, rec.Code)
				}
				// And nothing of substance leaked in the body.
				if strings.Contains(rec.Body.String(), "amount_paise") {
					t.Errorf("SECURITY: a %s received payout data", role)
				}
			})
		}

		t.Run(route.method+" "+route.path+" anonymous", func(t *testing.T) {
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, request(route.method, route.path, "", route.body))

			if rec.Code != http.StatusUnauthorized {
				t.Errorf("%s %s anonymous = %d, want 401",
					route.method, route.path, rec.Code)
			}
		})
	}
}

// TestAdminRoutesRequireTheInternalToken — these services are not publicly
// callable (CLAUDE.md rule 5), so an admin role header alone is not enough.
func TestAdminRoutesRequireTheInternalToken(t *testing.T) {
	pool := testPool(t)
	router := testRouter(t, pool)

	req := httptest.NewRequest(http.MethodGet, "/admin/dashboard", nil)
	// Admin identity, but no internal token — as if reaching the service
	// directly rather than through the gateway.
	req.Header.Set(httpx.UserRoleHeader, httpx.RoleAdmin)
	req.Header.Set(httpx.UserIDHeader, uuid.NewString())

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("admin without the internal token = %d, want 401", rec.Code)
	}
}

// seedOrderForDispatch inserts one order in a given status with a given
// delivery day, INSIDE the caller's transaction — see dispatchTx for why.
func seedOrderForDispatch(
	t *testing.T, tx pgx.Tx, status string, deliveryDay time.Time,
) uuid.UUID {
	t.Helper()
	ctx := context.Background()

	var orderID uuid.UUID
	err := tx.QueryRow(ctx, `
		INSERT INTO orders (
			order_number, customer_id, status, address_snapshot,
			subtotal_paise, platform_fee_paise, delivery_fee_paise, total_paise,
			placed_at, processing_at, delivery_day, expected_delivery_date
		) VALUES ($1,$2,$3,'{}'::jsonb,10000,300,1500,11800,$4,$5,$6,$7)
		RETURNING id`,
		"VM-TEST-"+uuid.NewString()[:8], uuid.New(), status,
		deliveryDay.AddDate(0, 0, -2), deliveryDay.AddDate(0, 0, -1),
		deliveryDay, deliveryDay.AddDate(0, 0, 1),
	).Scan(&orderID)
	if err != nil {
		t.Fatalf("seeding %s order: %v", status, err)
	}
	return orderID
}

// dispatchTx runs the dispatch tests inside a transaction that is ALWAYS
// rolled back.
//
// Not tidiness — correctness of the test itself. ClaimOrdersDueForDispatch is
// a set-based UPDATE over every processed order whose delivery day has passed;
// it has no way to know which rows a test seeded. Run against the dev database
// on a committed connection, it dispatches the developer's own orders as a
// side effect, which is exactly what happened the first time this test was
// written. A rolled-back transaction gives the query real rows to work on and
// leaves nothing behind.
func dispatchTx(t *testing.T, pool *pgxpool.Pool) pgx.Tx {
	t.Helper()
	tx, err := pool.Begin(context.Background())
	if err != nil {
		t.Fatalf("beginning: %v", err)
	}
	t.Cleanup(func() {
		_ = tx.Rollback(context.Background())
	})
	return tx
}

func statusOf(t *testing.T, tx pgx.Tx, orderID uuid.UUID) string {
	t.Helper()
	var status string
	if err := tx.QueryRow(context.Background(),
		`SELECT status FROM orders WHERE id = $1`, orderID).Scan(&status); err != nil {
		t.Fatalf("reading status: %v", err)
	}
	return status
}

// TestAutoDispatchOnDeliveryDay — a processed order dispatches itself once its
// delivery day arrives, and nothing else does.
//
// This is what stopped a customer's screen saying "Being prepared" days after
// the parcel went out: dispatch used to happen only when an admin pressed the
// button, and nothing noticed when nobody did.
func TestAutoDispatchOnDeliveryDay(t *testing.T) {
	pool := testPool(t)
	tx := dispatchTx(t, pool)
	ctx := context.Background()
	today := isttime.Today()

	due := seedOrderForDispatch(t, tx, "processed", today)
	overdue := seedOrderForDispatch(t, tx, "processed", isttime.AddDays(today, -3))
	tomorrow := seedOrderForDispatch(t, tx, "processed", isttime.AddDays(today, 1))
	// Statuses a clock must never move. An unpaid order is not a parcel, and a
	// cancelled one must not come back to life on its delivery day.
	unpaid := seedOrderForDispatch(t, tx, "pending_payment", today)
	cancelled := seedOrderForDispatch(t, tx, "cancelled", today)

	claimed, err := store.New(tx).ClaimOrdersDueForDispatch(ctx,
		store.ClaimOrdersDueForDispatchParams{Limit: 500, Column2: today})
	if err != nil {
		t.Fatalf("claiming: %v", err)
	}

	// Asserted on THESE orders rather than on the size of the batch: the
	// database may hold other due orders, and they are none of this test's
	// business.
	claimedIDs := map[uuid.UUID]bool{}
	for _, order := range claimed {
		claimedIDs[order.ID] = true
	}
	if !claimedIDs[due] || !claimedIDs[overdue] {
		t.Errorf("due=%v overdue=%v: both should have been claimed",
			claimedIDs[due], claimedIDs[overdue])
	}

	if got := statusOf(t, tx, due); got != "dispatched" {
		t.Errorf("order due today: got %q, want dispatched", got)
	}
	// The delivery day passed while nobody was looking — the job self-heals
	// rather than skipping it forever.
	if got := statusOf(t, tx, overdue); got != "dispatched" {
		t.Errorf("overdue order: got %q, want dispatched", got)
	}
	if got := statusOf(t, tx, tomorrow); got != "processed" {
		t.Errorf("order due tomorrow: got %q, want it left alone", got)
	}
	if got := statusOf(t, tx, unpaid); got != "pending_payment" {
		t.Errorf("unpaid order: got %q, want it left alone", got)
	}
	if got := statusOf(t, tx, cancelled); got != "cancelled" {
		t.Errorf("cancelled order: got %q, want it left alone", got)
	}
}

// TestAutoDispatchIsIdempotent — a second tick claims nothing, so a restart
// loop cannot re-dispatch what it already dispatched.
func TestAutoDispatchIsIdempotent(t *testing.T) {
	pool := testPool(t)
	tx := dispatchTx(t, pool)
	ctx := context.Background()
	today := isttime.Today()

	order := seedOrderForDispatch(t, tx, "processed", today)
	queries := store.New(tx)

	if _, err := queries.ClaimOrdersDueForDispatch(ctx,
		store.ClaimOrdersDueForDispatchParams{Limit: 500, Column2: today}); err != nil {
		t.Fatalf("first claim: %v", err)
	}
	second, err := queries.ClaimOrdersDueForDispatch(ctx,
		store.ClaimOrdersDueForDispatchParams{Limit: 500, Column2: today})
	if err != nil {
		t.Fatalf("second claim: %v", err)
	}
	for _, claimed := range second {
		if claimed.ID == order {
			t.Error("the same order was claimed twice")
		}
	}
	if got := statusOf(t, tx, order); got != "dispatched" {
		t.Errorf("got %q, want dispatched", got)
	}
}

// TestDailyReportClaimIsOncePerDay — the send-once guard.
//
// The job polls every minute, so without a durable claim the courier sheet
// would land in the team's inbox sixty times an hour. The primary key on the
// date is what makes "once" true across ticks, restarts and replicas.
func TestDailyReportClaimIsOncePerDay(t *testing.T) {
	pool := testPool(t)
	tx := dispatchTx(t, pool)
	ctx := context.Background()
	queries := store.New(tx)

	// A date far enough out that a real send for today cannot collide with it.
	day := isttime.AddDays(isttime.Today(), 3650)

	first, err := queries.ClaimDailyReport(ctx, store.ClaimDailyReportParams{
		ReportDate: day, OrderCount: 12, Recipients: "ops@example.com",
	})
	if err != nil {
		t.Fatalf("first claim: %v", err)
	}
	if first.OrderCount != 12 {
		t.Errorf("claim recorded %d orders, want 12", first.OrderCount)
	}

	// The second caller gets no row — which is how the next tick, or another
	// replica, learns the report has already gone out.
	_, err = queries.ClaimDailyReport(ctx, store.ClaimDailyReportParams{
		ReportDate: day, OrderCount: 12, Recipients: "ops@example.com",
	})
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("second claim: got %v, want pgx.ErrNoRows", err)
	}

	// Releasing it — what a failed send does — lets a later tick try again,
	// so a relay outage delays the report rather than losing the day.
	released, err := queries.ReleaseDailyReport(ctx, day)
	if err != nil {
		t.Fatalf("releasing: %v", err)
	}
	if released != 1 {
		t.Errorf("released %d rows, want 1", released)
	}
	if _, err := queries.ClaimDailyReport(ctx, store.ClaimDailyReportParams{
		ReportDate: day, OrderCount: 12, Recipients: "ops@example.com",
	}); err != nil {
		t.Fatalf("re-claiming after a release: %v", err)
	}
}

// TestSweeperCannotExpireAPaidOrder — the guard on the expiry.
//
// The sweeper claims lapsed reservations and expires their orders. Before the
// guard, it did that with an unfiltered UPDATE, so a payment committing during
// a sweep could be overwritten: money captured, stock released, order dead.
// The window is narrow in normal running and wide open on a restart after an
// outage, when a backlog of lapsed holds meets Razorpay's retried webhooks.
func TestSweeperCannotExpireAPaidOrder(t *testing.T) {
	pool := testPool(t)
	tx := dispatchTx(t, pool)
	ctx := context.Background()
	queries := store.New(tx)

	paid := seedOrderForDispatch(t, tx, "paid", isttime.Today())
	unpaid := seedOrderForDispatch(t, tx, "pending_payment", isttime.Today())

	// Exactly what the sweeper does to every order whose hold has lapsed.
	if _, err := queries.ExpireOrderIfUnpaid(ctx, paid); err != nil {
		t.Fatalf("expiring the paid order: %v", err)
	}
	if _, err := queries.ExpireOrderIfUnpaid(ctx, unpaid); err != nil {
		t.Fatalf("expiring the unpaid order: %v", err)
	}

	if got := statusOf(t, tx, paid); got != "paid" {
		t.Errorf("a PAID order was expired by the sweeper: got %q, want paid", got)
	}
	if got := statusOf(t, tx, unpaid); got != "expired" {
		t.Errorf("unpaid order: got %q, want expired", got)
	}

	// Reported as zero rows so the caller can tell "already moved on" from
	// "expired it", rather than guessing.
	rows, err := queries.ExpireOrderIfUnpaid(ctx, paid)
	if err != nil {
		t.Fatalf("second attempt: %v", err)
	}
	if rows != 0 {
		t.Errorf("expiring a paid order reported %d rows changed, want 0", rows)
	}
}

// TestSweeperHoldsStockForACapturedPayment — a paying customer keeps their
// produce whether the capture webhook failed or has not arrived at all.
//
// The webhook event is recorded before it is processed, so a capture that
// failed part-way (a database blip, an amount mismatch) leaves the order in
// pending_payment with its hold lapsing; and a webhook delayed by a Razorpay
// outage leaves no event at all. Releasing either hold put a paid-for pack
// back on sale and expired the order. The claim must take only holds that are
// safe to release, and still take an abandoned order's beside them.
func TestSweeperHoldsStockForACapturedPayment(t *testing.T) {
	pool := testPool(t)
	tx := dispatchTx(t, pool)
	ctx := context.Background()
	queries := store.New(tx)

	lapsedHold := func(rzpOrder *string) (orderID, holdID uuid.UUID) {
		t.Helper()
		orderID = seedOrderForDispatch(t, tx, "pending_payment", isttime.Today())
		if _, err := tx.Exec(ctx,
			`UPDATE orders SET razorpay_order_id = $2 WHERE id = $1`, orderID, rzpOrder); err != nil {
			t.Fatalf("setting razorpay order: %v", err)
		}
		if err := tx.QueryRow(ctx, `
			INSERT INTO stock_reservations (
				order_id, product_id, catalog_reservation_id, available_on, grams, expires_at
			) VALUES ($1, $2, $3, $4, 1000, now() - interval '1 hour')
			RETURNING id`,
			orderID, uuid.New(), uuid.New(), isttime.Today(),
		).Scan(&holdID); err != nil {
			t.Fatalf("seeding hold: %v", err)
		}
		return orderID, holdID
	}
	rzp := func() *string { id := "order_" + uuid.NewString()[:12]; return &id }

	// Captured, but processing the webhook failed: what step 3 leaves behind.
	failedCapture := rzp()
	failedOrder, failedHold := lapsedHold(failedCapture)
	payload := fmt.Sprintf(
		`{"event":"payment.captured","payload":{"payment":{"entity":{"id":"pay_x","order_id":%q,"amount":11800}}}}`,
		*failedCapture)
	if _, err := tx.Exec(ctx, `
		INSERT INTO webhook_events (provider_event_id, event_type, payload, processed_at, error)
		VALUES ($1, 'payment.captured', $2::jsonb, now(), 'database unavailable')`,
		"evt_"+uuid.NewString(), payload); err != nil {
		t.Fatalf("seeding webhook event: %v", err)
	}
	// Went to Razorpay, no event yet, and the sweeper has not cleared it —
	// Razorpay said money was taken, or could not be reached.
	_, uncheckedHold := lapsedHold(rzp())
	// Went to Razorpay, and Razorpay said nothing was paid.
	clearedOrder, clearedHold := lapsedHold(rzp())
	// Never reached Razorpay: nothing to ask about.
	_, offlineHold := lapsedHold(nil)

	// Even an order wrongly passed in as cleared keeps its hold when a capture
	// is on record.
	claimed, err := queries.ClaimExpiredReservations(ctx, store.ClaimExpiredReservationsParams{
		Cleared: []uuid.UUID{clearedOrder, failedOrder}, RowLimit: 100000,
	})
	if err != nil {
		t.Fatalf("claiming: %v", err)
	}
	got := map[uuid.UUID]bool{}
	for _, reservation := range claimed {
		got[reservation.ID] = true
	}

	for _, tc := range []struct {
		name string
		hold uuid.UUID
		want bool
	}{
		{"capture recorded but processing failed", failedHold, false},
		{"went to Razorpay, not cleared", uncheckedHold, false},
		{"Razorpay cleared it", clearedHold, true},
		{"never went to Razorpay", offlineHold, true},
	} {
		if got[tc.hold] != tc.want {
			t.Errorf("%s: claimed = %v, want %v", tc.name, got[tc.hold], tc.want)
		}
	}

	// And the sweeper's list of orders to ask about skips the recorded capture.
	lapsed, err := queries.ListLapsedRazorpayOrders(ctx, 100000)
	if err != nil {
		t.Fatalf("listing lapsed orders: %v", err)
	}
	for _, order := range lapsed {
		if order.ID == failedOrder {
			t.Error("an order with a recorded capture was listed for a Razorpay check")
		}
	}
}

// TestOutboxClaimAndRelease — the dispatcher's at-least-once guarantee.
//
// Rows are claimed by stamping published_at, so two replicas cannot take the
// same one; a failed send releases the row so the next tick retries. Without
// the release, a relay outage would silently swallow the confirmation.
func TestOutboxClaimAndRelease(t *testing.T) {
	pool := testPool(t)
	tx := dispatchTx(t, pool)
	ctx := context.Background()
	queries := store.New(tx)

	orderID := seedOrderForDispatch(t, tx, "paid", isttime.Today())
	row, err := queries.EnqueueOutbox(ctx, store.EnqueueOutboxParams{
		AggregateType: "order", AggregateID: orderID,
		EventType: "order.confirmed", Payload: []byte(`{}`),
	})
	if err != nil {
		t.Fatalf("enqueueing: %v", err)
	}

	// A wide batch on purpose: this database may already hold a backlog of
	// undispatched rows, and the batch is ordered oldest-first, so a small
	// limit would never reach the row seeded above.
	claimed, err := queries.ClaimOutboxBatch(ctx, 1000)
	if err != nil {
		t.Fatalf("claiming: %v", err)
	}
	found := false
	for _, candidate := range claimed {
		if candidate.ID == row.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("a fresh outbox row was not claimed")
	}

	// Stamped published: a second claim must not see it again.
	if _, err := queries.MarkOutboxPublished(ctx, row.ID); err != nil {
		t.Fatalf("marking published: %v", err)
	}
	second, err := queries.ClaimOutboxBatch(ctx, 1000)
	if err != nil {
		t.Fatalf("second claim: %v", err)
	}
	for _, candidate := range second {
		if candidate.ID == row.ID {
			t.Error("a published row was claimed twice")
		}
	}

	// The send failed, so the row goes back with the reason recorded.
	failure := "dial tcp: connection refused"
	released, err := queries.ReleaseOutbox(ctx, store.ReleaseOutboxParams{
		ID: row.ID, LastError: &failure,
	})
	if err != nil {
		t.Fatalf("releasing: %v", err)
	}
	if released != 1 {
		t.Fatalf("released %d rows, want 1", released)
	}

	third, err := queries.ClaimOutboxBatch(ctx, 1000)
	if err != nil {
		t.Fatalf("third claim: %v", err)
	}
	retried := false
	for _, candidate := range third {
		if candidate.ID == row.ID {
			retried = true
			if candidate.Attempts != 1 {
				t.Errorf("attempts = %d, want 1", candidate.Attempts)
			}
			if candidate.LastError == nil || *candidate.LastError != failure {
				t.Errorf("last_error = %v, want %q", candidate.LastError, failure)
			}
		}
	}
	if !retried {
		t.Error("a released row was not picked up again")
	}
}

// TestBulkSelectionIgnoresThePeriod — a selection carries its own ids.
//
// The orders screen defaults to All time, and the bulk endpoint used to
// resolve the period whatever the request was, so "Process selected" failed
// with "range must be one of: today, 2d, 3d…" — an error about a parameter
// that action does not use. The period is only meaningful for {"all": true}.
func TestBulkSelectionIgnoresThePeriod(t *testing.T) {
	pool := testPool(t)
	router := testRouter(t, pool)

	orderID := uuid.New()
	body := `{"order_ids":["` + orderID.String() + `"]}`

	for _, query := range []string{"?range=all", "?range=today", ""} {
		req := httptest.NewRequest(http.MethodPost, "/admin/orders/bulk-process"+query,
			strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(httpx.InternalTokenHeader, internalToken)
		req.Header.Set(httpx.UserIDHeader, uuid.NewString())
		req.Header.Set(httpx.UserRoleHeader, httpx.RoleAdmin)

		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		// The id does not exist, so nothing moves — but the request must be
		// ACCEPTED. A 400 here is the bug.
		if rec.Code != http.StatusOK {
			t.Errorf("query %q: got %d, want 200. body: %s",
				query, rec.Code, rec.Body.String())
		}
	}
}

// NOTE: there is deliberately NO router-level test of {"all": true} here.
//
// One was written and removed: it ran against the development database
// through the real handler and dispatched every processed order it found —
// including a live one placed minutes earlier. A bulk transition has no
// read-only form, so the pieces are tested where they can be tested safely
// instead: resolveQueueRange in internal/api (all-time resolves unbounded),
// and scopeLabel for what the audit row records.

// TestCaptureAfterAFailedAttemptStillPays — the retry path, end to end at the
// state-machine level.
//
// The bug this locks down: `payment.failed` used to move the order to
// `payment_failed` and release its stock. A Razorpay order accepts SEVERAL
// attempts, so a customer whose card was declined could retry from the same
// screen and pay successfully — and the resulting `payment.captured` then found
// the order out of `pending_payment`, did nothing, and returned 200. Money
// captured, order dead, no payout, no confirmation email. It happened in test
// and it was invisible.
//
// The two halves of the invariant are asserted together on purpose: MarkOrderPaid
// REFUSES a terminal order (correctly — that is what stops a cancelled order
// being resurrected), which is precisely why a failed attempt must not make the
// order terminal in the first place.
func TestCaptureAfterAFailedAttemptStillPays(t *testing.T) {
	pool := testPool(t)
	tx := dispatchTx(t, pool)
	ctx := context.Background()
	queries := store.New(tx)

	// --- a failed attempt must leave the order payable -----------------------
	retried := seedOrderForDispatch(t, tx, "pending_payment", isttime.Today())

	// What handlePaymentFailed now does: records the attempt, touches nothing
	// else. If it ever terminalizes the order again, the capture below fails.
	if got := statusOf(t, tx, retried); got != "pending_payment" {
		t.Fatalf("after a failed attempt: got %q, want pending_payment", got)
	}

	ref := "pay_" + uuid.NewString()[:12]
	if _, err := queries.MarkOrderPaid(ctx, store.MarkOrderPaidParams{
		ID: retried, RazorpayPaymentID: &ref,
	}); err != nil {
		t.Fatalf("the successful retry could not be applied: %v", err)
	}
	if got := statusOf(t, tx, retried); got != "paid" {
		t.Errorf("after a successful retry: got %q, want paid", got)
	}

	// --- and why that matters: a terminal order can never be paid ------------
	dead := seedOrderForDispatch(t, tx, "payment_failed", isttime.Today())
	deadRef := "pay_" + uuid.NewString()[:12]
	_, err := queries.MarkOrderPaid(ctx, store.MarkOrderPaidParams{
		ID: dead, RazorpayPaymentID: &deadRef,
	})
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("MarkOrderPaid on a terminal order = %v, want pgx.ErrNoRows", err)
	}
	if got := statusOf(t, tx, dead); got != "payment_failed" {
		t.Errorf("terminal order: got %q, want payment_failed", got)
	}
}
