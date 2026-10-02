package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/vayal-mikrogreenz/vm-go-common/httpx"
	"github.com/vayal-mikrogreenz/vm-go-common/money"

	"github.com/vayal-mikrogreenz/vm-catalog-api/internal/availability"
	"github.com/vayal-mikrogreenz/vm-catalog-api/internal/pricing"
	"github.com/vayal-mikrogreenz/vm-catalog-api/internal/store"
)

// catalogUnit is a purchasable pack size as a customer sees it.
type catalogUnit struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	// The grower's own detail for this pack — "6-8 fruit", "ventilated
	// carton" — or null. The label has to stay short enough to be a chip;
	// this is the sentence that would not fit in it, and the detail page
	// prints it under the label.
	Meta         *string `json:"meta"`
	WeightGrams  int32   `json:"weight_grams"`
	PricePaise   int64   `json:"price_paise"`
	PriceDisplay string  `json:"price_display"`
	// Purchasable is false when this pack is larger than the stock left, even
	// though the product itself is not sold out.
	Purchasable bool `json:"purchasable"`
}

// catalogProduct is one card on the storefront.
//
// Note what is ABSENT: total_grams, reserved_grams, sold_grams, remaining.
// Exact stock is competitor-useful intelligence — a rival watching a
// product's numbers fall could infer daily sales volume — so the customer API
// exposes a coarse hint or nothing at all.
type catalogProduct struct {
	ID          string  `json:"id"`
	SupplierID  string  `json:"supplier_id"`
	Name        string  `json:"name"`
	Type        string  `json:"type"`
	Grade       *string `json:"grade"`
	Description *string `json:"description"`
	ImageURL    *string `json:"image_url"`
	// The grade the card OPENS on, and whose picture ImageURL is — the first
	// with something buyable, so a card never leads with a sold-out M while
	// the XL beside it is on sale. The flat fields mirror it so a client that
	// reads none of SizeCodes still renders a coherent card.
	SizeCodeID string  `json:"size_code_id"`
	SizeCode   string  `json:"size_code"`
	SizeMeta   *string `json:"size_meta"`
	// How many grades the product has today.
	SizeCodeCount int `json:"size_code_count"`
	// SizeCodes is every grade declared today, in the grower's order, each
	// with its own cover, packs and stock. The card carries them all so
	// switching grade on the storefront is a local move with no round trip —
	// the same reason the detail page does.
	//
	// Shadowed by catalogProductDetail's richer field of the same JSON name:
	// encoding/json prefers the shallower field, so the detail endpoint still
	// ships the full galleries and this one is left empty there.
	SizeCodes []catalogCardSizeCode `json:"size_codes"`
	// StockHint is "" or a fixed phrase such as "Only a few left".
	StockHint string `json:"stock_hint"`
	// AnyUnitPurchasable is false when nothing on this card can be bought —
	// the grower has sold out or closed the product, or stock remains but
	// every pack size is too large for it. The card is still rendered, in a
	// sold-out state.
	AnyUnitPurchasable bool          `json:"any_unit_purchasable"`
	Units              []catalogUnit `json:"units"`
}

// catalogCardSizeCode is one grade as a CARD offers it.
//
// Deliberately lighter than catalogSizeCode on the detail page: the COVER
// only, never the gallery. A listing must not presign every grade's whole
// media set for pictures no card draws (CLAUDE.md §5.2) — a card paints one
// thumbnail, and switching grade swaps that one thumbnail.
type catalogCardSizeCode struct {
	ID   string  `json:"id"`
	Code string  `json:"code"`
	Meta *string `json:"meta"`
	// The presigned cover of THIS grade — the photographs are of a grade, so
	// choosing one has to change the picture or the choice means nothing.
	ImageURL *string `json:"image_url"`
	// StockHint is "" or a fixed phrase, scaled to THIS grade's smallest pack.
	StockHint string `json:"stock_hint"`
	// False when nothing in this grade can be bought today. The grades do not
	// share stock, so a sold-out M says nothing about the XL.
	AnyUnitPurchasable bool          `json:"any_unit_purchasable"`
	Units              []catalogUnit `json:"units"`
}

// Catalog returns everything a customer is offered today.
//
// That includes produce that has sold out or been closed since this morning:
// it comes back as a card with nothing purchasable on it, so the storefront
// can grey it out rather than make it vanish mid-session. Only produce no
// grower declared for today is absent.
//
// The business day is computed HERE, server-side, in Asia/Kolkata
// (requirement 4). A ?date parameter is accepted only so a client can state
// what it believes today to be; if it disagrees with the server the request is
// rejected rather than silently answered for the wrong day. A browser with a
// skewed clock, or a crafted request, must never be able to buy against
// yesterday's stock.
func (a *API) Catalog(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	today := availability.Today()

	if raw := strings.TrimSpace(r.URL.Query().Get("date")); raw != "" && raw != "today" {
		requested, err := parseBusinessDate(raw)
		if err != nil {
			a.fail(ctx, w, err)
			return
		}
		if !requested.Equal(today) {
			a.fail(ctx, w, httpx.BadRequest(
				"Only today's catalogue is available. Today is "+
					today.Format(dateFormat)+" (IST)."))
			return
		}
	}

	// Suspended or pending suppliers must not appear on the storefront. The
	// answer lives in vm-profile-api, so it comes over HTTP (CLAUDE.md §3).
	approved, err := a.suppliers.ApprovedIDs(ctx)
	if err != nil {
		a.fail(ctx, w, httpx.Unavailable(
			"The catalogue is temporarily unavailable. Please try again shortly."))
		return
	}
	if len(approved) == 0 {
		// Genuinely no approved suppliers. An empty catalogue, not an error.
		a.respond(ctx, w, http.StatusOK, map[string]any{
			"date": today.Format(dateFormat), "products": []catalogProduct{},
			"total": 0, "sellable_total": 0, "grades": []string{},
		})
		return
	}

	filters, err := parseCatalogFilters(r)
	if err != nil {
		a.fail(ctx, w, err)
		return
	}

	rows, err := a.queries.ListCatalog(ctx, store.ListCatalogParams{
		AvailableOn: today,
		SupplierIds: approved,
		Type:        filters.Type,
		Grade:       filters.Grade,
		Search:      filters.Search,
		Limit:       filters.Limit,
		Offset:      filters.Offset,
	})
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	total, err := a.queries.CountCatalog(ctx, store.CountCatalogParams{
		AvailableOn: today,
		SupplierIds: approved,
		Type:        filters.Type,
		Grade:       filters.Grade,
		Search:      filters.Search,
	})
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	// Counted apart from total, which now includes the sold-out cards: the
	// storefront's "N items available today" must mean N a customer can buy.
	sellableTotal, err := a.queries.CountSellableCatalog(ctx, store.CountSellableCatalogParams{
		AvailableOn: today,
		SupplierIds: approved,
		Type:        filters.Type,
		Grade:       filters.Grade,
		Search:      filters.Search,
	})
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	// Units for the whole page in one query rather than N+1.
	products := make([]store.Product, 0, len(rows))
	for _, row := range rows {
		// Field by field, so a column added to products does not silently
		// arrive here as a zero. MarkupBps is why that matters: a missing one
		// prices the whole storefront at the grower's price and nothing fails.
		products = append(products, store.Product{
			ID: row.ID, SupplierID: row.SupplierID, Name: row.Name, Type: row.Type,
			Grade: row.Grade, Description: row.Description,
			Status: row.Status, MarkupBps: row.MarkupBps,
			CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
		})
	}

	// The grades and their stock for this page. ListCatalog aggregated the
	// grams to page over products; the per-grade numbers are what actually
	// price and gate a pack.
	gradesByProduct, err := a.gradesForProducts(ctx, products, today)
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	trees, err := a.treesByProduct(ctx, products)
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	out := make([]catalogProduct, 0, len(products))
	for _, product := range products {
		grades := gradesByProduct[product.ID]
		tree := trees[product.ID]

		card := catalogProduct{
			ID:            product.ID.String(),
			SupplierID:    product.SupplierID.String(),
			Name:          product.Name,
			Type:          product.Type,
			Grade:         product.Grade,
			Description:   product.Description,
			SizeCodeCount: len(grades),
			Units:         make([]catalogUnit, 0),
			SizeCodes:     make([]catalogCardSizeCode, 0, len(grades)),
		}

		// Which grade the card OPENS on. Every grade is still sent — the
		// customer switches between them on the card itself — but one of them
		// has to be showing before they touch anything.
		display, _ := pickDisplayGrade(grades, tree)

		for i := range grades {
			grade := &grades[i]
			// Per grade, because the grades do not share stock: selling out M
			// must leave XL untouched (CLAUDE.md §5.2).
			remaining := sellableGramsFor(*grade)

			entry := catalogCardSizeCode{
				ID:   grade.SizeCodeID.String(),
				Code: grade.Code,
				Meta: grade.Meta,
				// The COVER of this grade, not its gallery: the card paints
				// one thumbnail per grade and the detail endpoint serves the
				// rest. Taken from the same combined list the detail page
				// draws, so a grade with no picture of its own shows the
				// product's rather than a placeholder — and the card and the
				// page it opens cannot lead with different photographs.
				ImageURL: a.coverImageURL(ctx, product.ID, tree.gallery(grade.SizeCodeID)),
				Units:    make([]catalogUnit, 0),
			}

			var smallestUnit int32
			for _, unit := range tree.Packs[grade.SizeCodeID] {
				if !unit.IsActive {
					continue
				}
				if smallestUnit == 0 || unit.WeightGrams < smallestUnit {
					smallestUnit = unit.WeightGrams
				}

				purchasable := availability.UnitPurchasable(remaining, unit.WeightGrams)
				if purchasable {
					entry.AnyUnitPurchasable = true
				}
				// The CUSTOMER price: the grower's price plus this product's
				// markup. The markup itself is never sent to the storefront —
				// publishing it would publish the grower's price with it.
				customerPaise := pricing.CustomerPaise(unit.PricePaise, product.MarkupBps)
				entry.Units = append(entry.Units, catalogUnit{
					ID:           unit.ID.String(),
					Label:        unit.Label,
					Meta:         unit.Meta,
					WeightGrams:  unit.WeightGrams,
					PricePaise:   customerPaise,
					PriceDisplay: money.FormatRupees(money.Paise(customerPaise)),
					Purchasable:  purchasable,
				})
			}

			// Scaled to this grade's smallest pack, so "a few left" means a
			// few PACKS of the grade the customer is looking at.
			entry.StockHint = availability.StockHint(remaining, smallestUnit)
			card.SizeCodes = append(card.SizeCodes, entry)

			// The flat fields mirror the opening grade, so a client that reads
			// none of SizeCodes still renders exactly what it did before.
			if display != nil && grade.SizeCodeID == display.SizeCodeID {
				card.SizeCodeID = entry.ID
				card.SizeCode = entry.Code
				card.SizeMeta = entry.Meta
				card.ImageURL = entry.ImageURL
				card.StockHint = entry.StockHint
				card.AnyUnitPurchasable = entry.AnyUnitPurchasable
				card.Units = entry.Units
			}
		}

		// A product with nothing purchasable — sold out, closed, or with every
		// pack too big for what is left — stays on the page so the customer
		// knows it was on offer today, with nothing selectable.
		out = append(out, card)
	}

	grades, err := a.queries.ListCatalogGrades(ctx, store.ListCatalogGradesParams{
		AvailableOn: today, SupplierIds: approved,
	})
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}
	gradeList := make([]string, 0, len(grades))
	for _, grade := range grades {
		if grade != nil && *grade != "" {
			gradeList = append(gradeList, *grade)
		}
	}

	a.respond(ctx, w, http.StatusOK, map[string]any{
		// Echoed so a client can show "produce for 14 Aug" without deciding
		// what today is itself.
		"date":     today.Format(dateFormat),
		"products": out,
		// total counts every card, sold-out ones included, and is what paging
		// is against. sellable_total is what can actually be bought.
		"total":          total,
		"sellable_total": sellableTotal,
		"grades":         gradeList,
		"limit":          filters.Limit,
		"offset":         filters.Offset,
	})
}

type catalogFilters struct {
	Type   *string
	Grade  *string
	Search *string
	Limit  int32
	Offset int32
}

func parseCatalogFilters(r *http.Request) (catalogFilters, error) {
	base, err := parseListFilters(r.URL.Query())
	if err != nil {
		return catalogFilters{}, err
	}

	filters := catalogFilters{
		Type:   base.Type,
		Search: base.Search,
		Limit:  base.Limit,
		Offset: base.Offset,
	}
	if grade := strings.TrimSpace(r.URL.Query().Get("grade")); grade != "" {
		filters.Grade = &grade
	}
	return filters, nil
}

// catalogProductDetail is one product on its own page.
//
// A superset of the card: the full description, the grower's trading name, and
// every grade's whole gallery. Still no exact stock — a detail page is a more
// convenient place to scrape numbers from, not a reason to publish them.
// catalogSizeCode is one grade as the storefront offers it: its own gallery,
// its own packs, its own stock.
type catalogSizeCode struct {
	ID   string  `json:"id"`
	Code string  `json:"code"`
	Meta *string `json:"meta"`
	// Media is this grade's whole gallery — images and videos, in the grower's
	// order. Switching grade swaps it, which is the point of the tree.
	Media    []mediaResponse `json:"media"`
	ImageURL *string         `json:"image_url"`
	// StockHint is "" or a fixed phrase, scaled to THIS grade's smallest pack.
	StockHint string `json:"stock_hint"`
	// HarvestSharePct is roughly what percent of this product's harvest comes
	// off as this grade — 55 for the M that is most of the field, 3 for the
	// XL2 that is a handful of fruit a tree. Null when the grower has not
	// estimated their split.
	//
	// A GENERAL property of the grade, not today's stock: it says how the
	// trees size up season after season, and it is what the storefront draws
	// the rarity of a grade's chip from. Nothing prices or reserves against
	// it.
	HarvestSharePct *int16 `json:"harvest_share_pct"`
	// False when nothing in this grade can be bought today.
	AnyUnitPurchasable bool                `json:"any_unit_purchasable"`
	Units              []catalogUnitDetail `json:"units"`
}

type catalogProductDetail struct {
	catalogProduct
	// SizeCodes is every grade declared for today, in the grower's order. The
	// detail page is the one customer surface that shows them all.
	SizeCodes    []catalogSizeCode `json:"size_codes"`
	SupplierName string            `json:"supplier_name"`
	// AvailableToday is false when the grower has not listed this produce for
	// today, or has closed it. The page renders either way; a shared link must
	// not 404 just because the produce sold out this morning.
	AvailableToday bool   `json:"available_today"`
	Date           string `json:"date"`
}

// gradesForProducts loads today's grades and their stock for a page of
// products, grouped by product and already in the grower's order.
func (a *API) gradesForProducts(
	ctx context.Context, products []store.Product, on time.Time,
) (map[uuid.UUID][]store.ListSizeCodeAvailabilityRow, error) {
	grouped := map[uuid.UUID][]store.ListSizeCodeAvailabilityRow{}
	if len(products) == 0 {
		return grouped, nil
	}
	ids := make([]uuid.UUID, 0, len(products))
	for _, p := range products {
		ids = append(ids, p.ID)
	}

	rows, err := a.queries.ListSizeCodeAvailability(ctx, store.ListSizeCodeAvailabilityParams{
		AvailableOn: on, ProductIds: ids,
	})
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		grouped[row.ProductID] = append(grouped[row.ProductID], row)
	}
	return grouped, nil
}

// sellableGramsFor reduces one grade's declaration to what can still be bought.
//
// No AvailableOn: the query that produced these rows already filtered to the
// business day, and SellableGrams does not consult the date anyway — the
// day-comparison lives in SellableOn, used on the detail page.
func sellableGramsFor(row store.ListSizeCodeAvailabilityRow) int32 {
	sheet := availability.Sheet{
		TotalGrams:    row.TotalGrams,
		ReservedGrams: row.ReservedGrams,
		SoldGrams:     row.SoldGrams,
		Status:        row.AvailabilityStatus,
	}
	// SellableGrams, not RemainingGrams: a closed sheet still has grams
	// declared against it, and the query no longer filters those rows out.
	// Reporting the raw remainder would put pack sizes back on sale on a grade
	// the grower has shut for the day.
	return sheet.SellableGrams()
}

// pickDisplayGrade chooses which grade a CARD shows, and returns its sellable
// grams alongside.
//
// The first grade with something actually buyable wins, so a card never leads
// with a sold-out M while the XL beside it is on sale. Failing that the first
// grade with any stock left, and failing that simply the first — a card for a
// product that has sold out entirely still has to render something, and the
// grower's own first grade is the least surprising choice.
func pickDisplayGrade(
	grades []store.ListSizeCodeAvailabilityRow, tree productTree,
) (*store.ListSizeCodeAvailabilityRow, int32) {
	var withStock *store.ListSizeCodeAvailabilityRow
	var withStockGrams int32

	for i := range grades {
		grade := &grades[i]
		remaining := sellableGramsFor(*grade)

		for _, pack := range tree.Packs[grade.SizeCodeID] {
			if pack.IsActive && availability.UnitPurchasable(remaining, pack.WeightGrams) {
				return grade, remaining
			}
		}
		if withStock == nil && remaining > 0 {
			withStock, withStockGrams = grade, remaining
		}
	}

	if withStock != nil {
		return withStock, withStockGrams
	}
	if len(grades) > 0 {
		return &grades[0], 0
	}
	return nil, 0
}

// catalogUnitDetail is a pack size on the detail page.
//
// PerKgDisplay is a comparison figure — what a 3 kg pack costs per kilo
// against a 5 kg one. No client renders it any more: beside a pack that
// already names its own weight it read as clutter, and it competed with the
// detail line the grower wrote. Still computed and still sent, because it is
// derived from the price already in hand and is the natural place for the
// comparison to come back to.
type catalogUnitDetail struct {
	catalogUnit
	PerKgDisplay string `json:"per_kg_display"`
}

// CatalogProduct returns one product for the customer-facing detail page.
//
// GET /catalog/{id}
func (a *API) CatalogProduct(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		a.fail(ctx, w, httpx.NotFound("That product could not be found."))
		return
	}

	today := availability.Today()

	approved, err := a.suppliers.ApprovedIDs(ctx)
	if err != nil {
		a.fail(ctx, w, httpx.Unavailable(
			"The catalogue is temporarily unavailable. Please try again shortly."))
		return
	}
	if len(approved) == 0 {
		a.fail(ctx, w, httpx.NotFound("That product could not be found."))
		return
	}

	row, err := a.queries.GetCatalogProduct(ctx, store.GetCatalogProductParams{
		ID: id, SupplierIds: approved,
	})
	if err != nil {
		// Archived, draft, or belonging to a supplier who is not approved.
		// All indistinguishable to the customer, deliberately: a 403 would
		// confirm the product exists.
		a.fail(ctx, w, httpx.NotFound("That product could not be found."))
		return
	}

	gradesByProduct, err := a.gradesForProducts(ctx, []store.Product{{ID: row.ID}}, today)
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}
	grades := gradesByProduct[row.ID]

	tree, err := a.treeForProduct(ctx, row)
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	detail := catalogProductDetail{
		catalogProduct: catalogProduct{
			ID:            row.ID.String(),
			SupplierID:    row.SupplierID.String(),
			Name:          row.Name,
			Type:          row.Type,
			Grade:         row.Grade,
			Description:   row.Description,
			SizeCodeCount: len(grades),
			Units:         make([]catalogUnit, 0),
		},
		SizeCodes:      make([]catalogSizeCode, 0, len(grades)),
		SupplierName:   a.suppliers.Name(ctx, row.SupplierID),
		Date:           today.Format(dateFormat),
		AvailableToday: false,
	}

	// Every grade declared for today, each with its own gallery, packs and
	// stock. Switching grade on the storefront is a client-side move between
	// these — no second request, so the pictures swap instantly.
	display, displayRemaining := pickDisplayGrade(grades, tree)

	for i := range grades {
		grade := &grades[i]
		remaining := sellableGramsFor(*grade)

		sheet := availability.Sheet{
			AvailableOn:   today,
			TotalGrams:    grade.TotalGrams,
			ReservedGrams: grade.ReservedGrams,
			SoldGrams:     grade.SoldGrams,
			Status:        grade.AvailabilityStatus,
		}
		sellableToday := sheet.SellableOn(today)
		if sellableToday {
			// The product is on sale today if ANY grade is.
			detail.AvailableToday = true
		}

		// This grade's own pictures, then the product's common ones — the
		// field, the packing shed, the grower. Concatenated server-side so a
		// phone and a browser cannot disagree about the order.
		media := tree.gallery(grade.SizeCodeID)
		out := catalogSizeCode{
			ID:              grade.SizeCodeID.String(),
			Code:            grade.Code,
			Meta:            grade.Meta,
			HarvestSharePct: grade.HarvestSharePct,
			Media:           a.toMediaResponses(ctx, row.ID, media),
			ImageURL:        a.coverImageURL(ctx, row.ID, media),
			Units:           make([]catalogUnitDetail, 0),
		}

		var smallestUnit int32
		for _, unit := range tree.Packs[grade.SizeCodeID] {
			if !unit.IsActive {
				continue
			}
			if smallestUnit == 0 || unit.WeightGrams < smallestUnit {
				smallestUnit = unit.WeightGrams
			}
			purchasable := sellableToday &&
				availability.UnitPurchasable(remaining, unit.WeightGrams)
			if purchasable {
				out.AnyUnitPurchasable = true
			}
			// The CUSTOMER price, exactly as on the card — the detail page
			// must not quote a different number from the one that opened it.
			customerPaise := pricing.CustomerPaise(unit.PricePaise, row.MarkupBps)
			base := catalogUnit{
				ID:           unit.ID.String(),
				Label:        unit.Label,
				Meta:         unit.Meta,
				WeightGrams:  unit.WeightGrams,
				PricePaise:   customerPaise,
				PriceDisplay: money.FormatRupees(money.Paise(customerPaise)),
				Purchasable:  purchasable,
			}
			out.Units = append(out.Units, catalogUnitDetail{
				catalogUnit: base,
				// Integer arithmetic in paise (CLAUDE.md rule 1): the per-kilo
				// figure is derived for comparison only and never priced
				// against. Scaled from the customer price, so it compares like
				// with like.
				PerKgDisplay: perKilo(customerPaise, unit.WeightGrams),
			})
		}

		// Scaled to THIS grade's smallest pack, so "a few left" means a few
		// packs of the grade the customer is looking at.
		out.StockHint = availability.StockHint(remaining, smallestUnit)
		detail.SizeCodes = append(detail.SizeCodes, out)

		// The embedded catalogProduct mirrors the grade the page OPENS on, so
		// a client that only reads the flat fields still gets a coherent view.
		if display != nil && grade.SizeCodeID == display.SizeCodeID {
			detail.SizeCodeID = out.ID
			detail.SizeCode = out.Code
			detail.SizeMeta = out.Meta
			detail.ImageURL = out.ImageURL
			detail.StockHint = out.StockHint
			detail.AnyUnitPurchasable = out.AnyUnitPurchasable
			for _, u := range out.Units {
				detail.Units = append(detail.Units, u.catalogUnit)
			}
		}
	}
	_ = displayRemaining

	a.respond(ctx, w, http.StatusOK, map[string]any{
		"product": detail,
		// The default grade's packs, flattened, so a client that has not yet
		// learned about grades still renders a working page.
		"units": defaultGradeUnits(detail),
	})
}

// defaultGradeUnits returns the packs of the grade the detail page opens on.
func defaultGradeUnits(detail catalogProductDetail) []catalogUnitDetail {
	for _, sc := range detail.SizeCodes {
		if sc.ID == detail.SizeCodeID {
			return sc.Units
		}
	}
	return []catalogUnitDetail{}
}

// perKilo renders a pack's price scaled to one kilogram.
//
// Rounded half-up in paise, never floating point. Returns "" for a zero
// weight rather than dividing by it.
func perKilo(pricePaise int64, weightGrams int32) string {
	if weightGrams <= 0 {
		return ""
	}
	perKg := (pricePaise*1000 + int64(weightGrams)/2) / int64(weightGrams)
	return money.FormatRupees(money.Paise(perKg)) + "/kg"
}
