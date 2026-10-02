package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vayal-mikrogreenz/vm-go-common/grams"
	"github.com/vayal-mikrogreenz/vm-go-common/httpx"
	"github.com/vayal-mikrogreenz/vm-go-common/money"
	"github.com/vayal-mikrogreenz/vm-go-common/produce"

	"github.com/vayal-mikrogreenz/vm-orders-api/internal/catalogclient"
	"github.com/vayal-mikrogreenz/vm-orders-api/internal/pricing"
	"github.com/vayal-mikrogreenz/vm-orders-api/internal/stockcheck"
	"github.com/vayal-mikrogreenz/vm-orders-api/internal/store"
)

// Kinds of change the customer must be told about.
const (
	changePriceChanged = "price_changed"
	changeSoldOut      = "sold_out"
	changeUnavailable  = "unavailable"
	// changeExceedsStock is the cart asking for more of something than exists
	// today — as opposed to sold_out, where not even one pack fits.
	changeExceedsStock = "exceeds_stock"
)

type cartChange struct {
	CartItemID  string `json:"cart_item_id"`
	ProductName string `json:"product_name"`
	Kind        string `json:"kind"`
	// Message is a WHOLE sentence, product name included. Both storefronts
	// render it on its own — they always have — so a fragment like "is sold
	// out in this pack size" reached customers with no subject.
	Message string `json:"message"`
	// Set for price_changed so the UI can show the movement.
	OldPricePaise *int64 `json:"old_price_paise,omitempty"`
	NewPricePaise *int64 `json:"new_price_paise,omitempty"`
}

type cartLine struct {
	ID            string `json:"id"`
	ProductID     string `json:"product_id"`
	ProductUnitID string `json:"product_unit_id"`
	// The GRADE this pack belongs to. A cart can hold two lines of one produce
	// at different grades, and they are different goods at different prices —
	// so every line has to say which.
	SizeCodeID     string `json:"size_code_id"`
	SizeCode       string `json:"size_code"`
	SizeMeta       string `json:"size_meta"`
	ProductName    string `json:"product_name"`
	UnitLabel      string `json:"unit_label"`
	WeightGrams    int32  `json:"weight_grams"`
	Qty            int32  `json:"qty"`
	UnitPricePaise int64  `json:"unit_price_paise"`
	UnitPrice      string `json:"unit_price_display"`
	LineTotalPaise int64  `json:"line_total_paise"`
	LineTotal      string `json:"line_total_display"`
	// Purchasable is false when the pack exceeds today's remaining stock, or
	// the product is not on sale today at all.
	Purchasable bool `json:"purchasable"`
	// Available is false when the pack is not in TODAY'S catalogue at all: the
	// grade was never declared for today, the product was archived, the pack
	// was deleted, the supplier suspended. Such a line used to be dropped from
	// the response entirely, which left the customer reading "one item is no
	// longer available" about a row they could not see, could not remove, and
	// which blocked checkout for good. It is now returned, priced at nothing,
	// so the clients can render it with a Remove button.
	//
	// Distinct from Purchasable: that one is about today's STOCK of something
	// on sale — the grower declared this grade and it has run out. This one is
	// about the pack not being on today's shelf at all. The service cannot
	// tell those apart any further than that (a pack absent from today's
	// catalogue looks the same whether it was deleted or merely not declared),
	// so the copy says the one thing that is true of every case.
	Available bool `json:"available"`

	// The quantity check. Purchasable asks whether ONE pack fits; these ask
	// whether the quantity in the cart does, which is a different question
	// once someone taps + a few times (see internal/stockcheck).
	//
	// ExceedsStock is true when this line asks for more than is left.
	ExceedsStock bool `json:"exceeds_stock"`
	// MaxQty is how many packs this line could have. Present on every line so
	// a stepper can cap itself before the customer hits the limit, not only
	// after.
	MaxQty int32 `json:"max_qty"`
	// AvailableDisplay is what is left of the PRODUCT, as a customer would say
	// it ("10 kg"). Empty unless the line exceeds: the storefront deliberately
	// shows a coarse hint rather than a live counter, and this is the one
	// place an exact figure is warranted.
	AvailableDisplay string `json:"available_display,omitempty"`
}

// unitDisplayName names a catalogue pack to the customer: the produce, plus
// its grade when the grade is a real one rather than the implicit default.
func unitDisplayName(u catalogclient.Unit) string {
	return produce.GradedName(u.ProductName, u.SizeCode)
}

// DisplayName is how a line is named to the customer: the produce, plus its
// grade when the grade is a real one.
//
// A product with a single implicit grade reads as just "Tomato"; one the
// grower actually graded reads as "Tomato (XL)", because that is the line the
// customer has to go and change.
func (l cartLine) DisplayName() string {
	return produce.GradedName(l.ProductName, l.SizeCode)
}

// GetCart returns the customer's cart, re-priced from the catalogue.
func (a *API) GetCart(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	customerID, err := customerFromRequest(r)
	if err != nil {
		a.fail(ctx, w, err)
		return
	}
	view, err := a.buildCart(ctx, customerID)
	if err != nil {
		a.fail(ctx, w, err)
		return
	}
	a.respond(ctx, w, http.StatusOK, view)
}

type cartView struct {
	CartID           string       `json:"cart_id"`
	Items            []cartLine   `json:"items"`
	Changes          []cartChange `json:"changes"`
	SubtotalPaise    int64        `json:"subtotal_paise"`
	Subtotal         string       `json:"subtotal_display"`
	PlatformFeePaise int64        `json:"platform_fee_paise"`
	PlatformFee      string       `json:"platform_fee_display"`
	DeliveryFeePaise int64        `json:"delivery_fee_paise"`
	DeliveryFee      string       `json:"delivery_fee_display"`
	TotalPaise       int64        `json:"total_paise"`
	Total            string       `json:"total_display"`
	// Checkoutable is false when any line is unpurchasable, so the UI can
	// disable the button rather than let checkout fail.
	Checkoutable bool `json:"checkoutable"`
}

// buildCart re-prices the cart from the live catalogue.
//
// CLAUDE.md §5.3: prices are NOT stored on cart_items — they are re-read on
// every cart read, and a diff is surfaced to the customer. A cart that quietly
// kept yesterday's price would let someone check out at a price the supplier
// has since changed.
func (a *API) buildCart(ctx context.Context, customerID uuid.UUID) (cartView, error) {
	cart, err := a.queries.GetOrCreateCart(ctx, customerID)
	if err != nil {
		return cartView{}, httpx.Internal(err)
	}
	items, err := a.queries.ListCartItems(ctx, cart.ID)
	if err != nil {
		return cartView{}, httpx.Internal(err)
	}

	view := cartView{
		CartID:  cart.ID.String(),
		Items:   make([]cartLine, 0, len(items)),
		Changes: make([]cartChange, 0),
	}
	if len(items) == 0 {
		view.Checkoutable = false
		return view, nil
	}

	// One catalogue read for the whole cart, not one per line — and one for
	// BOTH questions it has to answer: what each pack costs, and how many
	// grams are left behind it.
	today, err := a.catalog.TodaysCatalogue(ctx)
	if err != nil {
		return cartView{}, httpx.Unavailable(
			"We could not check today's prices. Please try again shortly.")
	}
	units := today.Units

	var lines []pricing.Line
	checkoutable := true
	// The lines that make a claim on today's stock, for the quantity check
	// below, plus where each one sits in view.Items. Not the positional match
	// it once was: a line whose pack has vanished is kept in view.Items so the
	// customer can remove it, but claims no grams and has no price.
	quantities := make([]stockcheck.Line, 0, len(items))
	priced := make([]int, 0, len(items))
	// Products that already said "sold out" for a pack size, so the quantity
	// check does not tell the same customer about the same produce twice.
	soldOut := map[uuid.UUID]bool{}

	for _, item := range items {
		unit, found := units[item.ProductUnitID]

		if !found {
			// The pack is not in today's catalogue at all: the product is
			// archived or closed, its supplier suspended, or the grower
			// reshaped their pack sizes and this one no longer exists.
			//
			// The line is KEPT, with no price and no stock claim. The customer
			// has to be able to see and remove what is blocking their
			// checkout; a warning about an invisible row is a dead end.
			checkoutable = false
			view.Items = append(view.Items, cartLine{
				ID:            item.ID.String(),
				ProductID:     item.ProductID.String(),
				ProductUnitID: item.ProductUnitID.String(),
				Qty:           item.Qty,
				// Nothing to name it with: the cart stores no snapshot (prices
				// and names are re-read every time — CLAUDE.md §5.3) and
				// today's catalogue does not carry the row. The clients say
				// "not available today" rather than inventing a name.
				Available:   false,
				Purchasable: false,
			})
			view.Changes = append(view.Changes, cartChange{
				CartItemID:  item.ID.String(),
				ProductName: "This item",
				Kind:        changeUnavailable,
				Message: "One item in your cart is not on sale today. Remove " +
					"it to check out.",
			})
			continue
		}

		line := cartLine{
			ID:             item.ID.String(),
			ProductID:      item.ProductID.String(),
			ProductUnitID:  item.ProductUnitID.String(),
			SizeCodeID:     unit.SizeCodeID.String(),
			SizeCode:       unit.SizeCode,
			SizeMeta:       unit.SizeMeta,
			ProductName:    unit.ProductName,
			UnitLabel:      unit.Label,
			WeightGrams:    unit.WeightGrams,
			Qty:            item.Qty,
			UnitPricePaise: unit.PricePaise.Int64(),
			UnitPrice:      money.FormatRupees(unit.PricePaise),
			Purchasable:    unit.Purchasable,
			Available:      true,
		}
		total := unit.PricePaise.Mul(int64(item.Qty))
		line.LineTotalPaise = total.Int64()
		line.LineTotal = money.FormatRupees(total)

		if !unit.Purchasable {
			checkoutable = false
			// Keyed by GRADE: "Tomato is sold out" must not be said because
			// the XL went while the M is still on the shelf.
			soldOut[unit.SizeCodeID] = true
			view.Changes = append(view.Changes, cartChange{
				CartItemID:  item.ID.String(),
				ProductName: line.DisplayName(),
				Kind:        changeSoldOut,
				Message:     line.DisplayName() + " is sold out in this pack size today.",
			})
		}

		view.Items = append(view.Items, line)
		priced = append(priced, len(view.Items)-1)
		quantities = append(quantities, stockcheck.Line{
			// The GRADE is the pool. Keying this on the product would let a
			// cart of XL draw down the M crate and vice versa.
			SizeCodeID:  unit.SizeCodeID,
			ProductID:   unit.ProductID,
			WeightGrams: unit.WeightGrams,
			Qty:         item.Qty,
		})
		lines = append(lines, pricing.Line{
			SupplierID:     unit.SupplierID,
			UnitPricePaise: unit.PricePaise,
			Qty:            int64(item.Qty),
		})
	}

	// The QUANTITY check, which purchasability above does not do: that asks
	// whether one pack fits, this asks whether the cart does.
	if !a.markOverStock(&view, quantities, priced, soldOut, today.RemainingGrams) {
		checkoutable = false
	}

	breakdown := pricing.Compute(lines, a.pricing)
	view.SubtotalPaise = breakdown.SubtotalPaise.Int64()
	view.Subtotal = money.FormatRupees(breakdown.SubtotalPaise)
	view.PlatformFeePaise = breakdown.PlatformFeePaise.Int64()
	view.PlatformFee = money.FormatRupees(breakdown.PlatformFeePaise)
	view.DeliveryFeePaise = breakdown.DeliveryFeePaise.Int64()
	view.DeliveryFee = money.FormatRupees(breakdown.DeliveryFeePaise)
	view.TotalPaise = breakdown.TotalPaise.Int64()
	view.Total = money.FormatRupees(breakdown.TotalPaise)
	view.Checkoutable = checkoutable && len(view.Items) > 0

	return view, nil
}

// markOverStock fills in the per-line quantity ceiling and says whether the
// cart as a whole still fits in today's stock.
//
// It reports the exact grams left for a product the cart has over-committed —
// the one place on the storefront that quotes a number rather than the coarse
// "Only a few left" hint. A customer being stopped is owed the figure that
// stopped them; a customer merely browsing is not being sold to by a counter.
//
// The verdict is advisory: stock moves while someone is deciding, and the
// authority remains the locked re-check at reservation time (CLAUDE.md §6.3).
// What this buys is the failure surfacing in the cart, against the item that
// caused it, instead of on the last screen as "one of these items".
//
// It takes no network call of its own: the grams come from the same catalogue
// read that priced the cart, so the price and the stock a customer is judged
// against are from one instant rather than two.
func (a *API) markOverStock(
	view *cartView,
	quantities []stockcheck.Line,
	// Where each quantities entry sits in view.Items. Lines whose pack has
	// vanished are in view.Items but make no stock claim, so the two are no
	// longer index-for-index.
	priced []int,
	soldOut map[uuid.UUID]bool,
	remaining map[uuid.UUID]int32,
) bool {
	if len(quantities) == 0 {
		return true
	}

	result := stockcheck.Check(quantities, remaining)

	// Names come from the cart lines, not the stock lookup: a grade with no
	// availability row today is absent from the lookup entirely, and that is
	// exactly the case where the customer most needs to be told which one.
	//
	// Keyed by SIZE CODE, and the name includes the grade — "Only 2 kg of
	// Tomato is left" is wrong when there are 40 kg of Tomato and 2 kg of the
	// XL the customer actually put in their cart.
	names := make(map[uuid.UUID]string, len(quantities))
	firstItemID := make(map[uuid.UUID]string, len(quantities))
	for i, line := range quantities {
		item := &view.Items[priced[i]]
		if _, found := names[line.SizeCodeID]; !found {
			names[line.SizeCodeID] = item.DisplayName()
			firstItemID[line.SizeCodeID] = item.ID
		}

		verdict := result.Lines[i]
		item.MaxQty = verdict.MaxQty
		if verdict.Fits() {
			continue
		}
		item.ExceedsStock = true
		item.AvailableDisplay = grams.Format(remaining[line.SizeCodeID])
	}

	for _, short := range result.Shortfalls {
		// Nothing at all left: the sold-out notice for this grade already says
		// so, and more usefully — it names the pack size. Two messages about
		// one line read like two problems.
		if short.RemainingGrams <= 0 || soldOut[short.SizeCodeID] {
			continue
		}
		view.Changes = append(view.Changes, cartChange{
			CartItemID:  firstItemID[short.SizeCodeID],
			ProductName: names[short.SizeCodeID],
			Kind:        changeExceedsStock,
			Message: fmt.Sprintf(
				"Only %s of %s is left today, and your cart adds up to %s. "+
					"Lower the quantity to check out.",
				grams.Format(short.RemainingGrams),
				names[short.SizeCodeID],
				grams.Format(short.RequestedGrams),
			),
		})
	}

	return result.Fits()
}

type addItemRequest struct {
	ProductUnitID string `json:"product_unit_id"`
	Qty           int32  `json:"qty"`
}

// AddCartItem adds a pack to the cart, or increases its quantity.
func (a *API) AddCartItem(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	customerID, err := customerFromRequest(r)
	if err != nil {
		a.fail(ctx, w, err)
		return
	}

	var req addItemRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		a.fail(ctx, w, err)
		return
	}
	unitID, err := uuid.Parse(req.ProductUnitID)
	if err != nil {
		a.fail(ctx, w, httpx.BadRequest("product_unit_id must be a UUID."))
		return
	}
	if req.Qty <= 0 || req.Qty > 99 {
		a.fail(ctx, w, httpx.Validation("Quantity must be between 1 and 99.",
			map[string]any{"qty": "must be between 1 and 99"}))
		return
	}

	// Verified against the live catalogue: a client must not be able to put an
	// arbitrary, archived or another day's unit into a cart.
	today, err := a.catalog.TodaysCatalogue(ctx)
	if err != nil {
		a.fail(ctx, w, httpx.Unavailable("We could not reach the catalogue. Please try again."))
		return
	}
	unit, found := today.Units[unitID]
	if !found {
		a.fail(ctx, w, httpx.Conflict("NOT_AVAILABLE_TODAY",
			"That item is not available today."))
		return
	}

	cart, err := a.queries.GetOrCreateCart(ctx, customerID)
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}
	if _, err := a.queries.UpsertCartItem(ctx, store.UpsertCartItemParams{
		CartID:        cart.ID,
		ProductUnitID: unitID,
		ProductID:     unit.ProductID,
		SupplierID:    unit.SupplierID,
		Qty:           req.Qty,
	}); err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	view, err := a.buildCart(ctx, customerID)
	if err != nil {
		a.fail(ctx, w, err)
		return
	}
	a.respond(ctx, w, http.StatusOK, view)
}

type updateItemRequest struct {
	Qty int32 `json:"qty"`
}

// UpdateCartItem sets a line's quantity. Zero removes it.
func (a *API) UpdateCartItem(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	customerID, err := customerFromRequest(r)
	if err != nil {
		a.fail(ctx, w, err)
		return
	}
	itemID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		a.fail(ctx, w, httpx.NotFound("Cart item not found."))
		return
	}

	var req updateItemRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		a.fail(ctx, w, err)
		return
	}
	if req.Qty < 0 || req.Qty > 99 {
		a.fail(ctx, w, httpx.Validation("Quantity must be between 0 and 99.",
			map[string]any{"qty": "must be between 0 and 99"}))
		return
	}

	cart, err := a.queries.GetOrCreateCart(ctx, customerID)
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	// Scoped by cart_id, so one customer cannot touch another's line.
	if req.Qty == 0 {
		if _, err := a.queries.DeleteCartItem(ctx, store.DeleteCartItemParams{
			CartID: cart.ID, ID: itemID,
		}); err != nil {
			a.fail(ctx, w, httpx.Internal(err))
			return
		}
	} else if _, err := a.queries.SetCartItemQty(ctx, store.SetCartItemQtyParams{
		CartID: cart.ID, ID: itemID, Qty: req.Qty,
	}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			a.fail(ctx, w, httpx.NotFound("Cart item not found."))
			return
		}
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	view, err := a.buildCart(ctx, customerID)
	if err != nil {
		a.fail(ctx, w, err)
		return
	}
	a.respond(ctx, w, http.StatusOK, view)
}

// RemoveCartItem deletes a line.
func (a *API) RemoveCartItem(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	customerID, err := customerFromRequest(r)
	if err != nil {
		a.fail(ctx, w, err)
		return
	}
	itemID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		a.fail(ctx, w, httpx.NotFound("Cart item not found."))
		return
	}

	cart, err := a.queries.GetOrCreateCart(ctx, customerID)
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}
	if _, err := a.queries.DeleteCartItem(ctx, store.DeleteCartItemParams{
		CartID: cart.ID, ID: itemID,
	}); err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	view, err := a.buildCart(ctx, customerID)
	if err != nil {
		a.fail(ctx, w, err)
		return
	}
	a.respond(ctx, w, http.StatusOK, view)
}

// cartUnits re-reads the cart alongside the live catalogue, for checkout.
func (a *API) cartUnits(
	ctx context.Context, customerID uuid.UUID,
) (store.Cart, []store.CartItem, map[uuid.UUID]catalogclient.Unit, error) {
	cart, err := a.queries.GetOrCreateCart(ctx, customerID)
	if err != nil {
		return store.Cart{}, nil, nil, httpx.Internal(err)
	}
	items, err := a.queries.ListCartItems(ctx, cart.ID)
	if err != nil {
		return store.Cart{}, nil, nil, httpx.Internal(err)
	}
	today, err := a.catalog.TodaysCatalogue(ctx)
	if err != nil {
		return store.Cart{}, nil, nil, httpx.Unavailable(
			"We could not check today's prices. Please try again shortly.")
	}
	return cart, items, today.Units, nil
}
