package api_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vayal-mikrogreenz/vm-go-common/isttime"
)

// A card carries EVERY grade the grower declared today, not just the one it
// opens on: the customer picks between M and XL on the card itself, which
// means the packs, the price, the stock hint and the cover of each grade have
// to arrive with the listing. The grades never share stock (CLAUDE.md §5.2),
// so selling out one must leave its siblings buyable on the same card.
//
// Integration tests against Postgres, like the sold-out ones beside them: what
// is under test is which rows the SQL returns and how they are grouped.

// cardGrade describes one size code to seed under a product.
type cardGrade struct {
	code       string
	meta       string
	unitGrams  int32
	pricePaise int64
	totalGrams int32
	soldGrams  int32
	status     string
}

// seedCatalogGrades creates one product with several grades, each with its own
// single pack and its own availability sheet for today. Grades are written in
// the order given, which is the order the storefront must show them in.
func seedCatalogGrades(
	t *testing.T, pool *pgxpool.Pool, name string, markupBps int32, grades ...cardGrade,
) (uuid.UUID, uuid.UUID, []uuid.UUID) {
	t.Helper()
	ctx := context.Background()

	supplierID := uuid.New()

	// The markup is pinned rather than left to the column default: these
	// tests assert the CUSTOMER prices on the card, and those are the grower's
	// price plus this product's markup.
	var productID uuid.UUID
	err := pool.QueryRow(ctx, `
		INSERT INTO products (supplier_id, name, type, status, markup_bps)
		VALUES ($1, $2, 'vegetable', 'active', $3) RETURNING id`,
		supplierID, name+" "+uuid.NewString()[:8], markupBps).Scan(&productID)
	if err != nil {
		t.Fatalf("seeding product: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM products WHERE id = $1`, productID)
	})

	sizeCodeIDs := make([]uuid.UUID, 0, len(grades))
	for i, g := range grades {
		var sizeCodeID uuid.UUID
		if err := pool.QueryRow(ctx, `
			INSERT INTO product_size_codes (product_id, code, meta, sort_order)
			VALUES ($1, $2, $3, $4) RETURNING id`,
			productID, g.code, g.meta, i).Scan(&sizeCodeID); err != nil {
			t.Fatalf("seeding size code %s: %v", g.code, err)
		}

		if _, err := pool.Exec(ctx, `
			INSERT INTO product_pack_options
				(size_code_id, product_id, label, weight_grams, price_paise)
			VALUES ($1, $2, $3, $4, $5)`,
			sizeCodeID, productID, g.code+" 1 kg", g.unitGrams, g.pricePaise,
		); err != nil {
			t.Fatalf("seeding pack for %s: %v", g.code, err)
		}

		if _, err := pool.Exec(ctx, `
			INSERT INTO daily_availability
				(supplier_id, product_id, size_code_id, available_on,
				 total_grams, sold_grams, status)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			supplierID, productID, sizeCodeID, isttime.Today(),
			g.totalGrams, g.soldGrams, g.status); err != nil {
			t.Fatalf("seeding availability for %s: %v", g.code, err)
		}

		sizeCodeIDs = append(sizeCodeIDs, sizeCodeID)
	}

	return supplierID, productID, sizeCodeIDs
}

// findCard picks one product out of a catalogue response by id.
func findCard(t *testing.T, page catalogResponse, id uuid.UUID) catalogCard {
	t.Helper()
	for _, card := range page.Products {
		if card.ID == id.String() {
			return card
		}
	}
	t.Fatalf("product %s missing from the catalogue", id)
	return catalogCard{}
}

// markupBps is the platform markup these tests price against: 5%, so ₹80 to
// the grower is ₹84 to the customer.
const markupBps = 500

// The card ships every grade, each priced and stocked on its own.
func TestCatalogCardCarriesEveryGrade(t *testing.T) {
	pool := testPool(t)

	supplierID, productID, sizeCodeIDs := seedCatalogGrades(t, pool, "Tomatoes", markupBps,
		cardGrade{code: "M", meta: "150 g - 200 g", unitGrams: 1000,
			pricePaise: 8000, totalGrams: 40000, status: "open"},
		cardGrade{code: "XL", meta: "300 g +", unitGrams: 1000,
			pricePaise: 12000, totalGrams: 15000, status: "open"},
	)

	card := findCard(t, getCatalog(t, catalogRouter(t, pool, supplierID)), productID)

	if card.SizeCodeCount != 2 {
		t.Fatalf("size_code_count = %d, want 2", card.SizeCodeCount)
	}
	if len(card.SizeCodes) != 2 {
		t.Fatalf("size_codes = %d entries, want 2", len(card.SizeCodes))
	}

	// The grower's own order, so the card's chips read the way the supplier
	// arranged them rather than however the join came back.
	if card.SizeCodes[0].Code != "M" || card.SizeCodes[1].Code != "XL" {
		t.Fatalf("grades out of order: %q then %q",
			card.SizeCodes[0].Code, card.SizeCodes[1].Code)
	}
	if card.SizeCodes[0].ID != sizeCodeIDs[0].String() {
		t.Errorf("first grade id = %s, want %s", card.SizeCodes[0].ID, sizeCodeIDs[0])
	}
	if card.SizeCodes[0].Meta == nil || *card.SizeCodes[0].Meta != "150 g - 200 g" {
		t.Errorf("first grade meta = %v, want %q", card.SizeCodes[0].Meta, "150 g - 200 g")
	}

	// A price belongs to (size, weight), not weight alone: the same 1 kg pack
	// costs different money under each grade, and the card must quote each.
	// ₹80 and ₹120 to the grower, plus the 5% markup.
	for i, want := range []string{"₹84.00", "₹126.00"} {
		packs := card.SizeCodes[i].Units
		if len(packs) != 1 {
			t.Fatalf("grade %s has %d packs, want 1", card.SizeCodes[i].Code, len(packs))
		}
		if packs[0].PriceDisplay != want {
			t.Errorf("grade %s price = %s, want %s",
				card.SizeCodes[i].Code, packs[0].PriceDisplay, want)
		}
		if !packs[0].Purchasable {
			t.Errorf("grade %s pack is not purchasable, want purchasable",
				card.SizeCodes[i].Code)
		}
	}

	// The flat fields still describe the grade the card opens on, so a client
	// that reads none of size_codes renders exactly what it did before.
	if card.SizeCode != "M" || card.SizeCodeID != sizeCodeIDs[0].String() {
		t.Errorf("card opens on %s (%s), want M (%s)",
			card.SizeCode, card.SizeCodeID, sizeCodeIDs[0])
	}
	if len(card.Units) != 1 || card.Units[0].PriceDisplay != "₹84.00" {
		t.Errorf("flat units = %+v, want the M pack at ₹84.00", card.Units)
	}
}

// Selling out one grade leaves its siblings alone. This is the property size
// codes exist for, asserted where the customer meets it: the card.
func TestCatalogCardGradesDoNotShareStock(t *testing.T) {
	pool := testPool(t)

	// M is gone for the day; XL is untouched in its own crate.
	supplierID, productID, _ := seedCatalogGrades(t, pool, "Tomatoes", markupBps,
		cardGrade{code: "M", unitGrams: 1000, pricePaise: 8000,
			totalGrams: 40000, soldGrams: 40000, status: "open"},
		cardGrade{code: "XL", unitGrams: 1000, pricePaise: 12000,
			totalGrams: 15000, status: "open"},
	)

	card := findCard(t, getCatalog(t, catalogRouter(t, pool, supplierID)), productID)

	if len(card.SizeCodes) != 2 {
		t.Fatalf("size_codes = %d entries, want 2", len(card.SizeCodes))
	}
	if card.SizeCodes[0].AnyUnitPurchasable {
		t.Error("M is sold out but reports something purchasable")
	}
	if !card.SizeCodes[1].AnyUnitPurchasable {
		t.Error("XL has full stock but reports nothing purchasable")
	}
	if card.SizeCodes[0].Units[0].Purchasable {
		t.Error("the M pack is buyable against stock that has all been sold")
	}

	// And the card opens on the grade that can actually be bought rather than
	// leading with the sold-out one.
	if card.SizeCode != "XL" {
		t.Errorf("card opens on %s, want XL — the only grade left", card.SizeCode)
	}
	if !card.AnyUnitPurchasable {
		t.Error("card reads sold out while XL is still on sale")
	}
}

// A grade the grower has closed for the day is still listed — the customer
// should see the grade exists — with nothing selectable under it.
func TestCatalogCardKeepsClosedGrade(t *testing.T) {
	pool := testPool(t)

	supplierID, productID, _ := seedCatalogGrades(t, pool, "Tomatoes", markupBps,
		cardGrade{code: "M", unitGrams: 1000, pricePaise: 8000,
			totalGrams: 40000, status: "open"},
		cardGrade{code: "XL", unitGrams: 1000, pricePaise: 12000,
			totalGrams: 15000, status: "closed"},
	)

	card := findCard(t, getCatalog(t, catalogRouter(t, pool, supplierID)), productID)

	if len(card.SizeCodes) != 2 {
		t.Fatalf("size_codes = %d entries, want 2", len(card.SizeCodes))
	}
	if !card.SizeCodes[0].AnyUnitPurchasable {
		t.Error("M is open with stock but reports nothing purchasable")
	}
	if card.SizeCodes[1].AnyUnitPurchasable {
		t.Error("XL is closed for the day but reports something purchasable")
	}
	// A closed sheet still has grams declared against it. Reporting them would
	// nudge a customer towards a grade the grower has shut.
	if card.SizeCodes[1].StockHint != "" {
		t.Errorf("closed grade shows stock hint %q, want none", card.SizeCodes[1].StockHint)
	}
}
