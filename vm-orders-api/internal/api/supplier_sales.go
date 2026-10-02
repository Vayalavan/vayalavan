package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/vayal-mikrogreenz/vm-go-common/httpx"
	"github.com/vayal-mikrogreenz/vm-go-common/isttime"
	"github.com/vayal-mikrogreenz/vm-go-common/money"

	"github.com/vayal-mikrogreenz/vm-orders-api/internal/store"
)

// defaultSupplierOrderLimit bounds the order list on the supplier's screen.
const (
	defaultSupplierOrderLimit = 50
	maxSupplierOrderLimit     = 200
)

type supplierSaleLine struct {
	ProductName string  `json:"product_name"`
	UnitLabel   string  `json:"unit_label"`
	Grade       *string `json:"grade"`
	// The GRADE this pack was sold at, snapshotted at placement. Null on an
	// ungraded listing and on orders placed before size codes existed — the
	// clients render a missing grade as no grade.
	SizeCode    *string `json:"size_code"`
	SizeMeta    *string `json:"size_meta"`
	WeightGrams int32   `json:"weight_grams"`
	Qty         int32   `json:"qty"`
	UnitPrice   string  `json:"unit_price_display"`
	LineTotal   string  `json:"line_total_display"`
	LineTotalP  int64   `json:"line_total_paise"`
}

type supplierOrderRow struct {
	OrderID     string `json:"order_id"`
	OrderNumber string `json:"order_number"`
	PlacedAt    string `json:"placed_at"`
	DeliveryDay string `json:"delivery_day"`
	Status      string `json:"status"`
	Units       int64  `json:"units"`
	AmountPaise int64  `json:"amount_paise"`
	Amount      string `json:"amount_display"`
	// PayoutStatus is "pending" until an admin settles, then "paid".
	PayoutStatus    string             `json:"payout_status"`
	PayoutReference string             `json:"payout_reference"`
	PayoutPaidAt    *string            `json:"payout_paid_at"`
	Items           []supplierSaleLine `json:"items"`
}

type supplierProductRow struct {
	ProductName string  `json:"product_name"`
	Grade       *string `json:"grade"`
	Units       int64   `json:"units"`
	Grams       int64   `json:"grams"`
	AmountPaise int64   `json:"amount_paise"`
	Amount      string  `json:"amount_display"`
}

// SupplierSales is the grower's own record of what they have sold.
//
// Its purpose is reconciliation. A supplier handing over produce needs to be
// able to check, independently, that the amount an admin is about to transfer
// matches what they actually sold — CLAUDE.md settles by manual NEFT, so there
// is no statement from a payment provider to fall back on. Both this screen
// and the admin settlement screen read supplier_payouts, so the two cannot
// quote different totals.
//
// Everything is scoped to the supplier on the request. There is no supplier_id
// parameter to tamper with (CLAUDE.md §7).
func (a *API) SupplierSales(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	supplierID, err := supplierFromRequest(r)
	if err != nil {
		a.fail(ctx, w, err)
		return
	}

	// Offset is validated the same way as limit: a negative one would make the
	// SQL error rather than simply return nothing useful.
	var offset int32
	if raw := r.URL.Query().Get("offset"); raw != "" {
		parsed, convErr := strconv.Atoi(raw)
		if convErr != nil || parsed < 0 {
			a.fail(ctx, w, httpx.BadRequest("offset must be zero or a positive number."))
			return
		}
		offset = int32(parsed)
	}

	limit := int32(defaultSupplierOrderLimit)
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, convErr := strconv.Atoi(raw)
		if convErr != nil || parsed < 1 || parsed > maxSupplierOrderLimit {
			a.fail(ctx, w, httpx.BadRequest(
				"limit must be a number between 1 and "+
					strconv.Itoa(maxSupplierOrderLimit)+"."))
			return
		}
		limit = int32(parsed)
	}

	// The window every figure below is computed over. Absent means all time,
	// which is what this screen showed before the filter existed.
	window, err := resolveSalesRange(r)
	if err != nil {
		a.fail(ctx, w, err)
		return
	}
	placedFrom, placedTo := window.Instants()

	summary, err := a.queries.SupplierSalesSummary(ctx, store.SupplierSalesSummaryParams{
		SupplierID: supplierID, PlacedFrom: placedFrom, PlacedTo: placedTo,
	})
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	// The pager's total. Counted over every order in the window, not the page,
	// so paging does not change the figure the supplier is reconciling against.
	orderTotal, err := a.queries.SupplierOrdersCount(ctx, store.SupplierOrdersCountParams{
		SupplierID: supplierID, PlacedFrom: placedFrom, PlacedTo: placedTo,
	})
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	byProduct, err := a.queries.SupplierSalesByProduct(ctx, store.SupplierSalesByProductParams{
		SupplierID: supplierID, PlacedFrom: placedFrom, PlacedTo: placedTo,
	})
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}
	products := make([]supplierProductRow, 0, len(byProduct))
	for _, row := range byProduct {
		products = append(products, supplierProductRow{
			ProductName: row.ProductName,
			Grade:       row.Grade,
			Units:       row.Units,
			Grams:       row.Grams,
			AmountPaise: row.AmountPaise,
			Amount:      money.FormatRupees(money.Paise(row.AmountPaise)),
		})
	}

	orderRows, err := a.queries.SupplierOrders(ctx, store.SupplierOrdersParams{
		SupplierID: supplierID, Limit: limit, Offset: offset,
		PlacedFrom: placedFrom, PlacedTo: placedTo,
	})
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	orderIDs := make([]uuid.UUID, 0, len(orderRows))
	for _, row := range orderRows {
		orderIDs = append(orderIDs, row.ID)
	}

	// One query for every line rather than one per order: a supplier with a
	// busy morning would otherwise generate fifty round trips to paint a list.
	itemsByOrder := map[uuid.UUID][]supplierSaleLine{}
	if len(orderIDs) > 0 {
		items, itemErr := a.queries.SupplierOrderItems(ctx, store.SupplierOrderItemsParams{
			SupplierID: supplierID, Column2: orderIDs,
		})
		if itemErr != nil {
			a.fail(ctx, w, httpx.Internal(itemErr))
			return
		}
		for _, item := range items {
			itemsByOrder[item.OrderID] = append(itemsByOrder[item.OrderID], supplierSaleLine{
				ProductName: item.ProductName,
				UnitLabel:   item.UnitLabel,
				Grade:       item.Grade,
				SizeCode:    item.SizeCode,
				SizeMeta:    item.SizeMeta,
				WeightGrams: item.WeightGrams,
				Qty:         item.Qty,
				UnitPrice:   money.FormatRupees(money.Paise(item.UnitPricePaise)),
				LineTotal:   money.FormatRupees(money.Paise(item.LineTotalPaise)),
				LineTotalP:  item.LineTotalPaise,
			})
		}
	}

	orders := make([]supplierOrderRow, 0, len(orderRows))
	for _, row := range orderRows {
		out := supplierOrderRow{
			OrderID:     row.ID.String(),
			OrderNumber: row.OrderNumber,
			PlacedAt:    row.PlacedAt.In(isttime.Location()).Format(time.RFC3339),
			DeliveryDay: isttime.FormatDate(row.DeliveryDay),
			Status:      row.Status,
			Units:       row.Units,
			AmountPaise: row.AmountPaise,
			Amount:      money.FormatRupees(money.Paise(row.AmountPaise)),
			// No payout row yet means the order is paid but settlement has not
			// been created — surfaced as "pending" rather than blank, which
			// would read as "nothing owed".
			PayoutStatus: orDefault(row.PayoutStatus, "pending"),
			Items:        itemsByOrder[row.ID],
		}
		out.PayoutReference = row.PayoutReference
		// Empty means the payout has not been settled yet — see the coalesce
		// in SupplierOrders.
		if row.PayoutPaidAt != "" {
			if parsed, parseErr := time.Parse(time.RFC3339, row.PayoutPaidAt); parseErr == nil {
				paidAt := parsed.In(isttime.Location()).Format(time.RFC3339)
				out.PayoutPaidAt = &paidAt
			}
		}
		orders = append(orders, out)
	}

	// The gross figure the commission is charged on, derived from the lines
	// rather than from payouts: payouts already have it deducted, and showing
	// a grower "you sold X, we took Y, you get Z" from two different sources
	// would eventually disagree by a paise.
	var grossPaise int64
	for _, row := range byProduct {
		grossPaise += row.AmountPaise
	}
	// THIS supplier's rate, not the platform default. A grower on a negotiated
	// rate seeing "3%" here while their bank receives a different number is
	// exactly the kind of discrepancy that destroys trust in the figures.
	var override *int64
	if bps, found := a.supplierCommissions(ctx, []uuid.UUID{supplierID})[supplierID]; found {
		override = &bps
	}
	effectiveBPS := a.Rates.SupplierCommissionBPS
	if override != nil {
		effectiveBPS = *override
	}
	commission := a.Rates.SupplierCommissionAt(money.Paise(grossPaise), override)

	a.respond(ctx, w, http.StatusOK, map[string]any{
		// Stated plainly so a supplier can reconcile their own bank statement:
		// what they sold, what we deducted, and what should arrive.
		"charges": map[string]any{
			"commission_rate":    bpsLabel(effectiveBPS),
			"commission_bps":     effectiveBPS,
			"gross_paise":        grossPaise,
			"gross_display":      money.FormatRupees(money.Paise(grossPaise)),
			"commission_paise":   int64(commission),
			"commission_display": money.FormatRupees(commission),
			"net_paise":          grossPaise - int64(commission),
			"net_display":        money.FormatRupees(money.Paise(grossPaise) - commission),
		},
		"summary": map[string]any{
			"order_count":     summary.OrderCount,
			"gross_paise":     summary.GrossPaise,
			"gross_display":   money.FormatRupees(money.Paise(summary.GrossPaise)),
			"pending_paise":   summary.PendingPaise,
			"pending_display": money.FormatRupees(money.Paise(summary.PendingPaise)),
			"settled_paise":   summary.SettledPaise,
			"settled_display": money.FormatRupees(money.Paise(summary.SettledPaise)),
		},
		"products": products,
		"orders":   orders,
		// Echoed so the screen can label what it is showing, and so a stale
		// filter is visible rather than silently applied.
		"range": window.Describe(),
		// Paging metadata for the orders list only. The summary and the
		// per-produce breakdown above are totals across everything sold, and
		// deliberately do not move as the supplier turns pages.
		"orders_total": orderTotal,
		"limit":        limit,
		"offset":       offset,
	})
}

func orDefault(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

// supplierFromRequest resolves the supplier making the request.
//
// The id comes from the gateway-verified actor, never from the URL or body, so
// there is nothing for a supplier to substitute in order to read another
// grower's sales (CLAUDE.md §7).
func supplierFromRequest(r *http.Request) (uuid.UUID, error) {
	actor, err := httpx.RequireActor(r.Context())
	if err != nil {
		return uuid.Nil, err
	}
	return actor.RequireSupplierID()
}
