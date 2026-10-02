package api

import (
	"context"
	"fmt"
	"net/http"
	"strconv"

	"github.com/google/uuid"

	"github.com/vayal-mikrogreenz/vm-go-common/httpx"
	"github.com/vayal-mikrogreenz/vm-go-common/money"

	"github.com/vayal-mikrogreenz/vm-catalog-api/internal/analyticsevents"
	"github.com/vayal-mikrogreenz/vm-catalog-api/internal/pricing"
	"github.com/vayal-mikrogreenz/vm-catalog-api/internal/store"
)

// maxMarkupBPS caps a markup at 100% of the grower's price.
//
// Doubling a price is already an extreme setting; anything past it is a typo —
// most likely a percentage typed as basis points. The database CHECKs the same
// bound, so this is the friendly half of one rule, not a second one.
const maxMarkupBPS int32 = 10_000

// adminUnit is a pack as an ADMIN sees it: both prices, side by side.
//
// This shape exists so the supplier's own response cannot grow a markup field
// by accident. `unitResponse` is what a grower receives and carries one price
// — theirs.
type adminUnit struct {
	ID string `json:"id"`
	// The grade this pack belongs to. Units are flattened across grades on
	// this screen, so each has to name its own or the prices are unreadable.
	SizeCodeID string  `json:"size_code_id"`
	SizeCode   string  `json:"size_code"`
	SizeMeta   *string `json:"size_meta"`
	Label      string  `json:"label"`
	// THIS pack's own detail — "6-8 fruit" — as distinct from the grade's.
	// An admin reviewing a listing sees what the customer will.
	Meta        *string `json:"meta"`
	WeightGrams int32   `json:"weight_grams"`
	IsActive    bool    `json:"is_active"`
	SortOrder   int32   `json:"sort_order"`
	// What the grower set, and is paid on.
	SupplierPricePaise int64  `json:"supplier_price_paise"`
	SupplierPrice      string `json:"supplier_price_display"`
	// What the product's rate came to on THIS pack. Per unit, because that is
	// the point of a percentage: 30% is ₹300 on a ₹1,000 pack and ₹600 on a
	// ₹2,000 one, and a screen that showed one figure for both would be
	// describing the flat markup this replaced.
	MarkupPaise int64  `json:"markup_paise"`
	Markup      string `json:"markup_display"`
	// What the customer pays: supplier price + markup.
	CustomerPricePaise int64  `json:"customer_price_paise"`
	CustomerPrice      string `json:"customer_price_display"`
}

// adminProduct is one row of the admin products screen.
type adminProduct struct {
	ID           string  `json:"id"`
	SupplierID   string  `json:"supplier_id"`
	SupplierName string  `json:"supplier_name"`
	Name         string  `json:"name"`
	Type         string  `json:"type"`
	Grade        *string `json:"grade"`
	Description  *string `json:"description"`
	ImageURL     *string `json:"image_url"`
	// MediaCount is every image and video across every grade, of which
	// ImageURL is the cover. The admin table shows one thumbnail and a count,
	// never the set.
	MediaCount int `json:"media_count"`
	// How many grades the listing has. The Units below are flattened across
	// them and each names its own.
	SizeCodeCount int    `json:"size_code_count"`
	Status        string `json:"status"`
	// The markup RATE, which only an admin ever sees. Basis points on the
	// wire, like every other rate on the platform (3000 = 30%).
	MarkupBPS int32 `json:"markup_bps"`
	// The same rate as a label ("30%") and as a plain number for the edit box
	// ("30"), so the UI never parses a formatted string back into a value.
	Markup        string      `json:"markup_display"`
	MarkupPercent string      `json:"markup_percent"`
	Units         []adminUnit `json:"units"`
	CreatedAt     string      `json:"created_at"`
	UpdatedAt     string      `json:"updated_at"`
}

// toAdminProduct renders a product with both prices.
func (a *API) toAdminProduct(
	ctx context.Context, p store.Product, tree productTree, supplierName string,
) adminProduct {
	out := adminProduct{
		ID:            p.ID.String(),
		SupplierID:    p.SupplierID.String(),
		SupplierName:  supplierName,
		Name:          p.Name,
		Type:          p.Type,
		Grade:         p.Grade,
		Description:   p.Description,
		Status:        p.Status,
		MarkupBPS:     p.MarkupBps,
		Markup:        pricing.FormatBPS(p.MarkupBps),
		MarkupPercent: percentString(p.MarkupBps),
		Units:         make([]adminUnit, 0),
		CreatedAt:     p.CreatedAt.Format(timeFormat),
		UpdatedAt:     p.UpdatedAt.Format(timeFormat),
	}

	// Flattened across grades rather than nested. The admin screen exists to
	// price things: one row per sellable pack, each carrying the grade it
	// belongs to, is what a person checking margins actually reads — a tree
	// would make them expand every grade to see the number they came for.
	//
	// The markup RATE is per product, so it is applied identically to every
	// grade; only the base price differs.
	mediaCount := 0
	for _, sc := range tree.SizeCodes {
		mediaCount += len(tree.Media[sc.ID])

		for _, u := range tree.Packs[sc.ID] {
			markup := pricing.MarkupPaise(u.PricePaise, p.MarkupBps)
			out.Units = append(out.Units, adminUnit{
				ID:                 u.ID.String(),
				SizeCodeID:         sc.ID.String(),
				SizeCode:           sc.Code,
				SizeMeta:           sc.Meta,
				Label:              u.Label,
				Meta:               u.Meta,
				WeightGrams:        u.WeightGrams,
				IsActive:           u.IsActive && sc.IsActive,
				SortOrder:          u.SortOrder,
				SupplierPricePaise: u.PricePaise,
				SupplierPrice:      money.FormatRupees(money.Paise(u.PricePaise)),
				MarkupPaise:        markup,
				Markup:             money.FormatRupees(money.Paise(markup)),
				CustomerPricePaise: u.PricePaise + markup,
				CustomerPrice:      money.FormatRupees(money.Paise(u.PricePaise + markup)),
			})
		}
	}

	// The admin table is a table: one thumbnail per row, so the cover alone —
	// the first grade that has one, and failing that the product's own
	// gallery. MediaCount is the whole listing's media, across every grade
	// AND the product-level bucket, so an admin can see there is more without
	// it being shipped.
	for _, sc := range tree.SizeCodes {
		if out.ImageURL == nil {
			out.ImageURL = a.coverImageURL(ctx, p.ID, tree.Media[sc.ID])
		}
	}
	if out.ImageURL == nil {
		out.ImageURL = a.coverImageURL(ctx, p.ID, tree.CommonMedia)
	}
	out.MediaCount = mediaCount + len(tree.CommonMedia)
	out.SizeCodeCount = len(tree.SizeCodes)
	return out
}

// AdminListProducts lists produce across every supplier.
//
// Its own response shape rather than the supplier list's: this one carries the
// markup and the customer price, and neither may ever reach the grower whose
// produce it is. Sharing the mapper would make that one careless edit away.
func (a *API) AdminListProducts(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	filters, err := parseListFilters(r.URL.Query())
	if err != nil {
		a.fail(ctx, w, err)
		return
	}

	// An admin may optionally narrow to one supplier.
	var supplierID *uuid.UUID
	if raw := r.URL.Query().Get("supplier_id"); raw != "" {
		parsed, parseErr := uuid.Parse(raw)
		if parseErr != nil {
			a.fail(ctx, w, httpx.BadRequest("supplier_id must be a UUID."))
			return
		}
		supplierID = &parsed
	}

	products, err := a.queries.ListProducts(ctx, store.ListProductsParams{
		SupplierID: supplierID,
		Type:       filters.Type,
		Status:     filters.Status,
		Search:     filters.Search,
		Limit:      filters.Limit,
		Offset:     filters.Offset,
	})
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	total, err := a.queries.CountProducts(ctx, store.CountProductsParams{
		SupplierID: supplierID,
		Type:       filters.Type,
		Status:     filters.Status,
		Search:     filters.Search,
	})
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	trees, err := a.treesByProduct(ctx, products)
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	// One directory call for the page. "Which grower is this?" is the first
	// question asked of a list that spans all of them.
	names := a.supplierNames(ctx, products)

	out := make([]adminProduct, 0, len(products))
	for _, p := range products {
		out = append(out, a.toAdminProduct(ctx, p, trees[p.ID], names[p.SupplierID]))
	}

	a.respond(ctx, w, http.StatusOK, map[string]any{
		"products": out,
		"total":    total,
		"limit":    filters.Limit,
		"offset":   filters.Offset,
	})
}

// AdminGetProduct reads any product, regardless of owner, with both prices.
func (a *API) AdminGetProduct(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	id, err := productIDFromPath(r)
	if err != nil {
		a.fail(ctx, w, err)
		return
	}

	product, err := a.queries.GetProduct(ctx, id)
	if err != nil {
		a.fail(ctx, w, a.lookupError(err))
		return
	}
	tree, err := a.treeForProduct(ctx, product)
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	a.respond(ctx, w, http.StatusOK,
		a.toAdminProduct(ctx, product, tree, a.suppliers.Name(ctx, product.SupplierID)))
}

type setMarkupRequest struct {
	// Basis points, as an integer. The same unit and the same wire shape as
	// suppliers.commission_bps, so the two rates on this platform are entered,
	// stored and validated identically. The UI converts from the percent an
	// admin types; a fractional basis point is not a thing.
	MarkupBPS *int32 `json:"markup_bps"`
}

// SetProductMarkup sets what we add to a grower's price.
//
// Admin only, and invisible to the supplier: the column is in no supplier
// response, the write does not touch updated_at (which is the grower's own
// "when did my listing change" signal), and the payouts for orders already
// placed are computed from the markup snapshotted on those orders, not from
// this value. Repricing tomorrow's sales cannot restate yesterday's.
func (a *API) SetProductMarkup(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	id, err := productIDFromPath(r)
	if err != nil {
		a.fail(ctx, w, err)
		return
	}

	var req setMarkupRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		a.fail(ctx, w, err)
		return
	}

	if req.MarkupBPS == nil {
		a.fail(ctx, w, httpx.Validation("A markup rate is required.",
			map[string]any{"markup_bps": "is required"}))
		return
	}
	bps := *req.MarkupBPS
	if bps < 0 {
		a.fail(ctx, w, httpx.Validation("A markup cannot be negative.",
			map[string]any{"markup_bps": "must be zero or more"}))
		return
	}
	if bps > maxMarkupBPS {
		a.fail(ctx, w, httpx.Validation("That markup is too large.",
			map[string]any{
				"markup_bps": "must be at most " + pricing.FormatBPS(maxMarkupBPS),
			}))
		return
	}

	product, err := a.changeWithEvent(ctx, analyticsevents.ProductMarkupChanged,
		analyticsevents.ActorAdmin, analyticsevents.SourceAdmin,
		func(q *store.Queries) (store.Product, error) {
			return q.SetProductMarkup(ctx, store.SetProductMarkupParams{ID: id, MarkupBps: bps})
		})
	if err != nil {
		a.fail(ctx, w, a.lookupError(err))
		return
	}

	tree, err := a.treeForProduct(ctx, product)
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	a.respond(ctx, w, http.StatusOK,
		a.toAdminProduct(ctx, product, tree, a.suppliers.Name(ctx, product.SupplierID)))
}

// supplierNames resolves the distinct suppliers on a page of products.
func (a *API) supplierNames(
	ctx context.Context, products []store.Product,
) map[uuid.UUID]string {
	ids := make([]uuid.UUID, 0, len(products))
	seen := map[uuid.UUID]bool{}
	for _, p := range products {
		if seen[p.SupplierID] {
			continue
		}
		seen[p.SupplierID] = true
		ids = append(ids, p.SupplierID)
	}
	return a.suppliers.Names(ctx, ids)
}

// percentString renders a rate as the plain number an edit box holds: "30",
// "2.5". Not "30%" — a form field that has to have its own suffix stripped
// before it can be used is a parsing bug waiting to happen.
func percentString(bps int32) string {
	whole := bps / 100
	frac := bps % 100
	switch {
	case frac == 0:
		return strconv.Itoa(int(whole))
	case frac%10 == 0:
		return strconv.Itoa(int(whole)) + "." + strconv.Itoa(int(frac/10))
	default:
		return fmt.Sprintf("%d.%02d", whole, frac)
	}
}
