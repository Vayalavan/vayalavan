package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vayal-mikrogreenz/vm-go-common/config"
	"github.com/vayal-mikrogreenz/vm-go-common/httpx"
	"github.com/vayal-mikrogreenz/vm-go-common/isttime"
	"github.com/vayal-mikrogreenz/vm-go-common/logging"

	"github.com/vayal-mikrogreenz/vm-catalog-api/internal/app"
)

const internalToken = "concurrency-test-token"

// testPool connects to the development database, or skips.
//
// An integration test on purpose: the property under test is that POSTGRES
// serialises two concurrent reservations. A mocked store would test the mock.
func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	if err := config.LoadRootDotEnv(); err != nil {
		t.Skipf("no root .env: %v", err)
	}
	url := os.Getenv("CATALOG_DATABASE_URL")
	if url == "" {
		t.Skip("CATALOG_DATABASE_URL is not set; run `make -C infra up` first")
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

// seedProduct creates a product with one grade, one pack, and today's
// availability for that grade set to exactly `grams`. It returns the SIZE CODE
// id, because that is what a reservation is taken against.
func seedProduct(t *testing.T, pool *pgxpool.Pool, grams int32) uuid.UUID {
	t.Helper()
	_, sizeCodeIDs := seedGradedProduct(t, pool, gradeStock{code: "STD", grams: grams})
	return sizeCodeIDs[0]
}

// gradeStock is one grade to seed and the grams to declare for it today.
type gradeStock struct {
	code  string
	grams int32
}

// seedGradedProduct creates one product with a grade per gradeStock, each with
// a single pack whose weight equals that grade's declared grams — so "the last
// unit" is exactly one purchase, per grade.
//
// Returns the product id and the size code ids in the order given.
func seedGradedProduct(
	t *testing.T, pool *pgxpool.Pool, grades ...gradeStock,
) (uuid.UUID, []uuid.UUID) {
	t.Helper()
	ctx := context.Background()

	supplierID := uuid.New()
	var productID uuid.UUID

	err := pool.QueryRow(ctx, `
		INSERT INTO products (supplier_id, name, type, status)
		VALUES ($1, $2, 'vegetable', 'active') RETURNING id`,
		supplierID, "Concurrency Probe "+uuid.NewString()[:8]).Scan(&productID)
	if err != nil {
		t.Fatalf("seeding product: %v", err)
	}

	sizeCodeIDs := make([]uuid.UUID, 0, len(grades))
	for i, grade := range grades {
		var sizeCodeID uuid.UUID
		if err := pool.QueryRow(ctx, `
			INSERT INTO product_size_codes (product_id, code, sort_order)
			VALUES ($1, $2, $3) RETURNING id`,
			productID, grade.code, i).Scan(&sizeCodeID); err != nil {
			t.Fatalf("seeding size code %s: %v", grade.code, err)
		}

		if _, err := pool.Exec(ctx, `
			INSERT INTO product_pack_options
				(size_code_id, product_id, label, weight_grams, price_paise)
			VALUES ($1, $2, '1 kg', $3, 10000)`,
			sizeCodeID, productID, grade.grams); err != nil {
			t.Fatalf("seeding pack option for %s: %v", grade.code, err)
		}

		if _, err := pool.Exec(ctx, `
			INSERT INTO daily_availability
				(supplier_id, product_id, size_code_id, available_on, total_grams)
			VALUES ($1, $2, $3, $4, $5)`,
			supplierID, productID, sizeCodeID, isttime.Today(), grade.grams); err != nil {
			t.Fatalf("seeding availability for %s: %v", grade.code, err)
		}
		sizeCodeIDs = append(sizeCodeIDs, sizeCodeID)
	}

	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM products WHERE id = $1`, productID)
	})
	return productID, sizeCodeIDs
}

func testRouter(t *testing.T, pool *pgxpool.Pool) http.Handler {
	t.Helper()
	logger := logging.NewTo(newDiscard(), "vm-catalog-api-test", "error")
	return app.NewRouter(app.Config{InternalToken: internalToken}, pool, nil, logger)
}

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }
func newDiscard() discardWriter                   { return discardWriter{} }

// TestReserveIsSerialisedUnderConcurrency is the CLAUDE.md §6.3 requirement:
// N goroutines check out the last available unit, and exactly one must succeed.
//
// This is the single most important correctness property in the system.
// Overselling means telling a customer their produce is coming when it is not,
// and no amount of downstream reconciliation fixes that.
func TestReserveIsSerialisedUnderConcurrency(t *testing.T) {
	pool := testPool(t)

	const (
		goroutines = 20
		unitGrams  = 5000 // exactly one unit's worth is declared
	)
	sizeCodeID := seedProduct(t, pool, unitGrams)
	router := testRouter(t, pool)

	// All goroutines are released at once, so they genuinely contend rather
	// than arriving in a queue.
	var start sync.WaitGroup
	start.Add(1)

	var done sync.WaitGroup
	results := make([]int, goroutines)

	for i := 0; i < goroutines; i++ {
		done.Add(1)
		go func(index int) {
			defer done.Done()
			start.Wait()

			body := fmt.Sprintf(
				`{"order_ref":%q,"ttl_seconds":900,"lines":[{"size_code_id":%q,"grams":%d}]}`,
				uuid.NewString(), sizeCodeID, unitGrams)

			req := httptest.NewRequest(http.MethodPost, "/internal/reservations",
				strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set(httpx.InternalTokenHeader, internalToken)

			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			results[index] = rec.Code
		}(i)
	}

	start.Done()
	done.Wait()

	var succeeded, conflicted, other int
	for _, code := range results {
		switch code {
		case http.StatusCreated:
			succeeded++
		case http.StatusConflict:
			conflicted++
		default:
			other++
		}
	}

	t.Logf("%d goroutines: %d succeeded, %d conflicted, %d other",
		goroutines, succeeded, conflicted, other)

	if succeeded != 1 {
		t.Errorf("SECURITY/CORRECTNESS: %d goroutines reserved the last unit, want exactly 1",
			succeeded)
	}
	if other != 0 {
		t.Errorf("%d requests failed with an unexpected status", other)
	}
	if conflicted != goroutines-1 {
		t.Errorf("%d conflicted, want %d", conflicted, goroutines-1)
	}

	// The ledger must agree: exactly one unit's worth reserved, no more.
	var reserved, sold, total int32
	if err := pool.QueryRow(context.Background(), `
		SELECT total_grams, reserved_grams, sold_grams
		FROM daily_availability WHERE size_code_id = $1`,
		sizeCodeID).Scan(&total, &reserved, &sold); err != nil {
		t.Fatalf("reading availability: %v", err)
	}
	if reserved != unitGrams {
		t.Errorf("reserved_grams = %d, want %d — grams were over-committed", reserved, unitGrams)
	}
	if reserved+sold > total {
		t.Errorf("OVERSOLD: reserved(%d) + sold(%d) exceeds total(%d)", reserved, sold, total)
	}

	// And exactly one hold row exists.
	var holds int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM stock_holds WHERE size_code_id = $1 AND status = 'held'`,
		sizeCodeID).Scan(&holds); err != nil {
		t.Fatalf("counting holds: %v", err)
	}
	if holds != 1 {
		t.Errorf("got %d holds, want exactly 1", holds)
	}
}

// TestReserveAllowsExactlyTheAvailableCount — with room for three units, three
// concurrent buyers should all succeed and the fourth must not.
func TestReserveAllowsExactlyTheAvailableCount(t *testing.T) {
	pool := testPool(t)

	const (
		unitGrams  = 1000
		capacity   = 3
		goroutines = 10
	)
	sizeCodeID := seedProduct(t, pool, unitGrams*capacity)
	router := testRouter(t, pool)

	var start sync.WaitGroup
	start.Add(1)
	var done sync.WaitGroup
	codes := make([]int, goroutines)

	for i := 0; i < goroutines; i++ {
		done.Add(1)
		go func(index int) {
			defer done.Done()
			start.Wait()

			body := fmt.Sprintf(
				`{"order_ref":%q,"ttl_seconds":900,"lines":[{"size_code_id":%q,"grams":%d}]}`,
				uuid.NewString(), sizeCodeID, unitGrams)
			req := httptest.NewRequest(http.MethodPost, "/internal/reservations",
				strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set(httpx.InternalTokenHeader, internalToken)

			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			codes[index] = rec.Code
		}(i)
	}
	start.Done()
	done.Wait()

	succeeded := 0
	for _, code := range codes {
		if code == http.StatusCreated {
			succeeded++
		}
	}
	if succeeded != capacity {
		t.Errorf("%d reservations succeeded, want exactly %d", succeeded, capacity)
	}

	var reserved int32
	if err := pool.QueryRow(context.Background(),
		`SELECT reserved_grams FROM daily_availability WHERE size_code_id = $1`,
		sizeCodeID).Scan(&reserved); err != nil {
		t.Fatalf("reading availability: %v", err)
	}
	if reserved != unitGrams*capacity {
		t.Errorf("reserved_grams = %d, want %d", reserved, unitGrams*capacity)
	}
}

// TestSettleIsIdempotent — a retried release must not return the same grams
// twice, which would inflate available stock out of thin air.
func TestSettleIsIdempotent(t *testing.T) {
	pool := testPool(t)

	const grams = 4000
	sizeCodeID := seedProduct(t, pool, grams)
	router := testRouter(t, pool)

	// Reserve.
	body := fmt.Sprintf(
		`{"order_ref":%q,"ttl_seconds":900,"lines":[{"size_code_id":%q,"grams":%d}]}`,
		uuid.NewString(), sizeCodeID, grams)
	req := httptest.NewRequest(http.MethodPost, "/internal/reservations", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(httpx.InternalTokenHeader, internalToken)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("reserve = %d, want 201: %s", rec.Code, rec.Body.String())
	}
	var reserveResp struct {
		Holds []struct {
			HoldID string `json:"hold_id"`
		} `json:"holds"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &reserveResp); err != nil {
		t.Fatalf("decoding reserve response: %v", err)
	}
	holdID := reserveResp.Holds[0].HoldID

	release := func() {
		payload := fmt.Sprintf(`{"outcome":"released","hold_ids":[%q]}`, holdID)
		r := httptest.NewRequest(http.MethodPost, "/internal/reservations/settle",
			strings.NewReader(payload))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set(httpx.InternalTokenHeader, internalToken)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("settle = %d, want 200: %s", w.Code, w.Body.String())
		}
	}

	release()
	release() // the retry
	release() // and again, for good measure

	var reserved int32
	if err := pool.QueryRow(context.Background(),
		`SELECT reserved_grams FROM daily_availability WHERE size_code_id = $1`,
		sizeCodeID).Scan(&reserved); err != nil {
		t.Fatalf("reading availability: %v", err)
	}
	if reserved != 0 {
		t.Errorf("reserved_grams = %d after repeated releases, want 0", reserved)
	}
}

var _ = time.Second

// TestGradesHoldSeparateStock is the property size codes exist for: the gram
// pools are per grade, so selling out one must leave the others untouched.
//
// Under the old flat model this test could not even be written — one product
// had one pool, and buying the last M would have made the XL unbuyable too.
func TestGradesHoldSeparateStock(t *testing.T) {
	pool := testPool(t)

	const unitGrams = 5000 // exactly one pack of each grade is declared

	_, sizeCodeIDs := seedGradedProduct(t, pool,
		gradeStock{code: "M", grams: unitGrams},
		gradeStock{code: "XL", grams: unitGrams},
	)
	mID, xlID := sizeCodeIDs[0], sizeCodeIDs[1]
	router := testRouter(t, pool)

	reserve := func(sizeCodeID uuid.UUID) int {
		body := fmt.Sprintf(
			`{"order_ref":%q,"ttl_seconds":900,"lines":[{"size_code_id":%q,"grams":%d}]}`,
			uuid.NewString(), sizeCodeID, unitGrams)
		req := httptest.NewRequest(http.MethodPost, "/internal/reservations",
			strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(httpx.InternalTokenHeader, internalToken)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec.Code
	}

	// Take the last M.
	if got := reserve(mID); got != http.StatusCreated {
		t.Fatalf("reserving the last M = %d, want %d", got, http.StatusCreated)
	}
	// M is now gone.
	if got := reserve(mID); got != http.StatusConflict {
		t.Errorf("second M reservation = %d, want %d — M was oversold",
			got, http.StatusConflict)
	}
	// XL must be completely unaffected. This is the assertion that fails if
	// the pools are ever collapsed back onto the product.
	if got := reserve(xlID); got != http.StatusCreated {
		t.Errorf("reserving XL after M sold out = %d, want %d — "+
			"the grades are sharing a gram pool", got, http.StatusCreated)
	}

	// And the rows agree: one pack held against each grade, nothing borrowed.
	for _, tc := range []struct {
		name       string
		sizeCodeID uuid.UUID
	}{{"M", mID}, {"XL", xlID}} {
		var total, reserved, sold int32
		if err := pool.QueryRow(context.Background(), `
			SELECT total_grams, reserved_grams, sold_grams
			FROM daily_availability WHERE size_code_id = $1`,
			tc.sizeCodeID).Scan(&total, &reserved, &sold); err != nil {
			t.Fatalf("reading %s availability: %v", tc.name, err)
		}
		if reserved != unitGrams {
			t.Errorf("%s reserved_grams = %d, want %d", tc.name, reserved, unitGrams)
		}
		if reserved+sold > total {
			t.Errorf("%s OVERSOLD: reserved(%d) + sold(%d) exceeds total(%d)",
				tc.name, reserved, sold, total)
		}
	}
}
