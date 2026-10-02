package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vayal-mikrogreenz/vm-go-common/httpx"
	"github.com/vayal-mikrogreenz/vm-go-common/isttime"
	"github.com/vayal-mikrogreenz/vm-go-common/logging"

	"github.com/vayal-mikrogreenz/vm-catalog-api/internal/app"
)

// The storefront shows produce that has sold out rather than hiding it
// (CLAUDE.md §5.2: "the product is not purchasable and the client UI shows it
// as sold out"). Hiding it made a grower's produce vanish mid-session, which
// reads as "they never sell this" rather than "you were too late today".
//
// These are integration tests against Postgres for the same reason the
// reservation test is: the rule being checked is which rows the SQL returns.

// catalogResponse is the shape GET /catalog returns, as a customer's browser
// parses it.
type catalogResponse struct {
	Date          string        `json:"date"`
	Products      []catalogCard `json:"products"`
	Total         int64         `json:"total"`
	SellableTotal int64         `json:"sellable_total"`
}

// catalogCard is one card on that page. The flat fields describe the grade
// the card OPENS on; SizeCodes is every grade it can be switched to.
type catalogCard struct {
	ID                 string         `json:"id"`
	Name               string         `json:"name"`
	StockHint          string         `json:"stock_hint"`
	AnyUnitPurchasable bool           `json:"any_unit_purchasable"`
	SizeCodeID         string         `json:"size_code_id"`
	SizeCode           string         `json:"size_code"`
	SizeCodeCount      int            `json:"size_code_count"`
	Units              []catalogPack  `json:"units"`
	SizeCodes          []catalogGrade `json:"size_codes"`
}

type catalogGrade struct {
	ID                 string        `json:"id"`
	Code               string        `json:"code"`
	Meta               *string       `json:"meta"`
	StockHint          string        `json:"stock_hint"`
	AnyUnitPurchasable bool          `json:"any_unit_purchasable"`
	Units              []catalogPack `json:"units"`
}

type catalogPack struct {
	Label        string `json:"label"`
	PriceDisplay string `json:"price_display"`
	Purchasable  bool   `json:"purchasable"`
}

// sheet describes one product to seed, as the supplier's day left it.
type sheet struct {
	name       string
	unitGrams  int32
	totalGrams int32
	soldGrams  int32
	status     string
}

// seedCatalog creates one supplier owning a product per sheet, and returns the
// supplier id plus the product ids in the same order.
//
// Every product hangs off ONE supplier so the approved-supplier allow-list can
// scope the whole assertion: the counts in the response are then about these
// rows and nothing else the development database happens to hold.
func seedCatalog(t *testing.T, pool *pgxpool.Pool, sheets ...sheet) (uuid.UUID, []uuid.UUID) {
	t.Helper()
	ctx := context.Background()

	supplierID := uuid.New()
	ids := make([]uuid.UUID, 0, len(sheets))

	for _, s := range sheets {
		var productID uuid.UUID
		err := pool.QueryRow(ctx, `
			INSERT INTO products (supplier_id, name, type, status)
			VALUES ($1, $2, 'vegetable', 'active') RETURNING id`,
			supplierID, s.name+" "+uuid.NewString()[:8]).Scan(&productID)
		if err != nil {
			t.Fatalf("seeding product %s: %v", s.name, err)
		}

		// One grade per product here: these tests are about which CARDS the
		// storefront returns, and a single grade is the shape that isolates
		// that from the grade-selection logic.
		var sizeCodeID uuid.UUID
		if err := pool.QueryRow(ctx, `
			INSERT INTO product_size_codes (product_id, code, sort_order)
			VALUES ($1, 'STD', 0) RETURNING id`, productID).Scan(&sizeCodeID); err != nil {
			t.Fatalf("seeding size code for %s: %v", s.name, err)
		}

		if _, err := pool.Exec(ctx, `
			INSERT INTO product_pack_options
				(size_code_id, product_id, label, weight_grams, price_paise)
			VALUES ($1, $2, '1 kg', $3, 10000)`,
			sizeCodeID, productID, s.unitGrams); err != nil {
			t.Fatalf("seeding pack option for %s: %v", s.name, err)
		}

		if _, err := pool.Exec(ctx, `
			INSERT INTO daily_availability
				(supplier_id, product_id, size_code_id, available_on,
				 total_grams, sold_grams, status)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			supplierID, productID, sizeCodeID, isttime.Today(),
			s.totalGrams, s.soldGrams, s.status); err != nil {
			t.Fatalf("seeding availability for %s: %v", s.name, err)
		}

		t.Cleanup(func() {
			_, _ = pool.Exec(context.Background(),
				`DELETE FROM products WHERE id = $1`, productID)
		})
		ids = append(ids, productID)
	}

	return supplierID, ids
}

// catalogRouter builds the service with a stub vm-profile-api that approves
// exactly the given suppliers.
func catalogRouter(t *testing.T, pool *pgxpool.Pool, approved ...uuid.UUID) http.Handler {
	t.Helper()

	ids := make([]string, 0, len(approved))
	for _, id := range approved {
		ids = append(ids, id.String())
	}

	profile := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/internal/suppliers/approved-ids" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"supplier_ids": ids})
		}))
	t.Cleanup(profile.Close)

	logger := logging.NewTo(newDiscard(), "vm-catalog-api-test", "error")
	return app.NewRouter(app.Config{
		InternalToken: internalToken,
		ProfileAPIURL: profile.URL,
	}, pool, nil, logger)
}

// getCatalog calls GET /catalog the way the gateway does.
func getCatalog(t *testing.T, router http.Handler) catalogResponse {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, "/catalog", nil)
	req.Header.Set(httpx.InternalTokenHeader, internalToken)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /catalog = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	var out catalogResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decoding catalogue: %v", err)
	}
	return out
}

// TestCatalogShowsSoldOutProducts is the rule itself: produce declared for
// today stays on the storefront once it runs out, as a card with nothing
// purchasable on it.
func TestCatalogShowsSoldOutProducts(t *testing.T) {
	pool := testPool(t)

	const unit = 1000

	supplierID, ids := seedCatalog(t, pool,
		// In stock: two units left.
		sheet{name: "InStock", unitGrams: unit, totalGrams: 2 * unit, status: "open"},
		// Sold out: every declared gram is committed to paid orders.
		sheet{name: "SoldOut", unitGrams: unit, totalGrams: unit, soldGrams: unit, status: "open"},
		// Closed early by the grower, with grams still declared. Not sold out
		// arithmetically, but equally unbuyable (CLAUDE.md §5.2).
		sheet{name: "Closed", unitGrams: unit, totalGrams: 5 * unit, status: "closed"},
		// Stock remains, but less than the one pack size on offer.
		sheet{name: "PackTooBig", unitGrams: 5 * unit, totalGrams: 6 * unit, soldGrams: 5 * unit, status: "open"},
	)

	body := getCatalog(t, catalogRouter(t, pool, supplierID))

	if len(body.Products) != len(ids) {
		t.Fatalf("got %d products, want %d — sold-out produce was filtered out",
			len(body.Products), len(ids))
	}

	byID := make(map[string]int, len(body.Products))
	for i, p := range body.Products {
		byID[p.ID] = i
	}

	cases := []struct {
		label           string
		productID       uuid.UUID
		wantPurchasable bool
	}{
		{"in stock", ids[0], true},
		{"sold out", ids[1], false},
		{"closed by supplier", ids[2], false},
		{"only pack too big", ids[3], false},
	}

	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			index, ok := byID[tc.productID.String()]
			if !ok {
				t.Fatalf("%s product is missing from the catalogue", tc.label)
			}
			product := body.Products[index]

			if product.AnyUnitPurchasable != tc.wantPurchasable {
				t.Errorf("any_unit_purchasable = %v, want %v",
					product.AnyUnitPurchasable, tc.wantPurchasable)
			}
			if len(product.Units) != 1 {
				t.Fatalf("got %d units, want 1", len(product.Units))
			}
			if product.Units[0].Purchasable != tc.wantPurchasable {
				t.Errorf("unit purchasable = %v, want %v",
					product.Units[0].Purchasable, tc.wantPurchasable)
			}
			// A card nobody can buy from must not carry a stock nudge; on the
			// closed product that would advertise 5 kg that is not for sale.
			if !tc.wantPurchasable && product.StockHint != "" {
				t.Errorf("stock_hint = %q on an unbuyable product, want empty",
					product.StockHint)
			}
		})
	}
}

// TestCatalogCountsOnlySellableAsAvailable guards the copy under the grid:
// total is how many cards there are, sellable_total is how many can be bought.
func TestCatalogCountsOnlySellableAsAvailable(t *testing.T) {
	pool := testPool(t)

	const unit = 1000

	supplierID, _ := seedCatalog(t, pool,
		sheet{name: "CountA", unitGrams: unit, totalGrams: 3 * unit, status: "open"},
		sheet{name: "CountB", unitGrams: unit, totalGrams: unit, soldGrams: unit, status: "open"},
		sheet{name: "CountC", unitGrams: unit, totalGrams: 2 * unit, status: "closed"},
	)

	body := getCatalog(t, catalogRouter(t, pool, supplierID))

	if body.Total != 3 {
		t.Errorf("total = %d, want 3 (every card listed today)", body.Total)
	}
	if body.SellableTotal != 1 {
		t.Errorf("sellable_total = %d, want 1 (only what can be bought)", body.SellableTotal)
	}
}

// TestCatalogOrdersSoldOutLast keeps the shop leading with what is for sale.
//
// The names are seeded so alphabetical order alone would put the sold-out one
// first: if the sold-out tail is not sorted last, this fails.
func TestCatalogOrdersSoldOutLast(t *testing.T) {
	pool := testPool(t)

	const unit = 1000

	supplierID, ids := seedCatalog(t, pool,
		sheet{name: "AAA Gone", unitGrams: unit, totalGrams: unit, soldGrams: unit, status: "open"},
		sheet{name: "ZZZ Here", unitGrams: unit, totalGrams: 2 * unit, status: "open"},
	)

	body := getCatalog(t, catalogRouter(t, pool, supplierID))

	if len(body.Products) != 2 {
		t.Fatalf("got %d products, want 2", len(body.Products))
	}
	if body.Products[0].ID != ids[1].String() {
		t.Errorf("first card is %q, want the in-stock %q — sold-out produce sorted ahead of it",
			body.Products[0].Name, "ZZZ Here")
	}
	if body.Products[1].ID != ids[0].String() {
		t.Errorf("last card is %q, want the sold-out %q",
			body.Products[1].Name, "AAA Gone")
	}
}
