package api

import (
	"net/http"

	"github.com/google/uuid"

	"github.com/vayal-mikrogreenz/vm-go-common/httpx"
	"github.com/vayal-mikrogreenz/vm-go-common/isttime"

	"github.com/vayal-mikrogreenz/vm-catalog-api/internal/availability"
	"github.com/vayal-mikrogreenz/vm-catalog-api/internal/pricing"
	"github.com/vayal-mikrogreenz/vm-catalog-api/internal/store"
)

// internalUnit is one purchasable pack, priced from BOTH sides.
//
// The two prices are the whole point of this endpoint. The customer-facing
// /catalog returns one price — the marked-up one — and must never publish the
// markup beside it, because that would publish the grower's price too.
// vm-orders-api needs both: the customer price to charge, and the markup to
// know which part of that money is not the supplier's.
type internalUnit struct {
	ID          string `json:"id"`
	ProductID   string `json:"product_id"`
	SizeCodeID  string `json:"size_code_id"`
	SupplierID  string `json:"supplier_id"`
	ProductName string `json:"product_name"`
	Label       string `json:"label"`
	WeightGrams int32  `json:"weight_grams"`
	// SupplierPricePaise is what the grower set and is paid on.
	SupplierPricePaise int64 `json:"supplier_price_paise"`
	// MarkupBps is the product's markup rate; MarkupPaise is what that rate
	// came to on THIS pack. Both travel: the amount is what gets snapshotted
	// onto an order line, the rate is what explains it later.
	MarkupBps   int32 `json:"markup_bps"`
	MarkupPaise int64 `json:"markup_paise"`
	// PricePaise is what the customer pays: supplier price + markup.
	PricePaise int64 `json:"price_paise"`
	// Purchasable is false when this pack alone is larger than the stock left.
	// It says nothing about QUANTITY — that is the cart's question, answered
	// from RemainingGrams on the product below.
	Purchasable bool `json:"purchasable"`
}

// internalGrade is one SIZE CODE of a product, with the stock behind it.
//
// One entry per (product, grade), not per product: the gram pool belongs to
// the size code, so "how much is left" has no product-level answer any more.
type internalGrade struct {
	ProductID string `json:"product_id"`
	Name      string `json:"product_name"`
	// The grade these units belong to and whose stock RemainingGrams reports.
	SizeCodeID string  `json:"size_code_id"`
	SizeCode   string  `json:"size_code"`
	SizeMeta   *string `json:"size_meta"`
	// Type and Grade exist here so vm-orders-api can snapshot them onto the
	// order line. They are not pricing inputs; they are what lets an order —
	// and every report built on orders — still say what kind of produce was
	// sold after the product itself has been edited or deleted.
	Type  string  `json:"product_type"`
	Grade *string `json:"grade"`
	// RemainingGrams is what is still sellable of THIS GRADE today, exactly.
	// Internal only: the storefront gives a coarse hint and never a number,
	// because a live counter on a card is competitor intelligence and reads as
	// pressure selling.
	RemainingGrams int32          `json:"remaining_grams"`
	MarkupBps      int32          `json:"markup_bps"`
	Units          []internalUnit `json:"units"`
}

// InternalUnits returns today's purchasable packs with both prices and the
// exact stock behind them.
//
// Service-to-service only, and the single source vm-orders-api prices a cart
// from. It replaced that service reading the customer-facing /catalog, which
// worked only while there was one price: now there are two, and reading them
// from one row in one call is what stops the customer price and the markup
// drifting apart between requests and mis-splitting an order.
//
// Advisory on stock, like everything outside a lock: overselling is prevented
// by the locked re-check in Reserve (CLAUDE.md §6.3).
func (a *API) InternalUnits(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// The business day is decided HERE, server-side in IST. A caller-supplied
	// date must never decide what is purchasable (CLAUDE.md rule 2).
	today := availability.Today()

	// Suspended or pending growers must not be sellable, exactly as on the
	// storefront. The answer lives in vm-profile-api (CLAUDE.md §3).
	approved, err := a.suppliers.ApprovedIDs(ctx)
	if err != nil {
		a.fail(ctx, w, httpx.Unavailable(
			"The catalogue is temporarily unavailable. Please try again shortly."))
		return
	}
	if len(approved) == 0 {
		a.respond(ctx, w, http.StatusOK, map[string]any{
			"date": isttime.FormatISODate(today), "grades": []internalGrade{},
		})
		return
	}

	rows, err := a.queries.ListInternalUnitsForDate(ctx, store.ListInternalUnitsForDateParams{
		AvailableOn: today, SupplierIds: approved,
	})
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	// Grouped by SIZE CODE, in the order the query returned them, so the shape
	// mirrors how availability actually works: grams belong to the grade and
	// every pack of that grade competes for the same pool (CLAUDE.md §5.2).
	byGrade := map[uuid.UUID]*internalGrade{}
	order := make([]uuid.UUID, 0, len(rows))

	for _, row := range rows {
		grade, seen := byGrade[row.SizeCodeID]
		if !seen {
			sheet := availability.Sheet{
				AvailableOn:   today,
				TotalGrams:    row.TotalGrams,
				ReservedGrams: row.ReservedGrams,
				SoldGrams:     row.SoldGrams,
				Status:        row.AvailabilityStatus,
			}
			grade = &internalGrade{
				ProductID:  row.ProductID.String(),
				Name:       row.ProductName,
				SizeCodeID: row.SizeCodeID.String(),
				SizeCode:   row.SizeCode,
				SizeMeta:   row.SizeMeta,
				Type:       row.ProductType,
				Grade:      row.Grade,
				// SellableGrams, not RemainingGrams: a closed sheet still has
				// grams declared against it, and reporting the raw remainder
				// would put produce a grower shut for the day back on sale.
				RemainingGrams: sheet.SellableGrams(),
				MarkupBps:      row.MarkupBps,
				Units:          make([]internalUnit, 0, 4),
			}
			byGrade[row.SizeCodeID] = grade
			order = append(order, row.SizeCodeID)
		}

		markupPaise := pricing.MarkupPaise(row.PricePaise, row.MarkupBps)
		grade.Units = append(grade.Units, internalUnit{
			ID:                 row.UnitID.String(),
			ProductID:          row.ProductID.String(),
			SizeCodeID:         row.SizeCodeID.String(),
			SupplierID:         row.SupplierID.String(),
			ProductName:        row.ProductName,
			Label:              row.Label,
			WeightGrams:        row.WeightGrams,
			SupplierPricePaise: row.PricePaise,
			MarkupBps:          row.MarkupBps,
			MarkupPaise:        markupPaise,
			PricePaise:         row.PricePaise + markupPaise,
			Purchasable: availability.UnitPurchasable(
				grade.RemainingGrams, row.WeightGrams),
		})
	}

	grades := make([]internalGrade, 0, len(order))
	for _, id := range order {
		grades = append(grades, *byGrade[id])
	}

	a.respond(ctx, w, http.StatusOK, map[string]any{
		"date":   isttime.FormatISODate(today),
		"grades": grades,
	})
}
