package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vayal-mikrogreenz/vm-go-common/httpx"
	"github.com/vayal-mikrogreenz/vm-go-common/isttime"
	"github.com/vayal-mikrogreenz/vm-go-common/money"

	"github.com/vayal-mikrogreenz/vm-orders-api/internal/analyticsevents"
	"github.com/vayal-mikrogreenz/vm-orders-api/internal/pricing"
	"github.com/vayal-mikrogreenz/vm-orders-api/internal/store"
)

// supplierSubtotal is one supplier's share of an order.
type supplierSubtotal struct {
	SupplierID   string `json:"supplier_id"`
	BusinessName string `json:"business_name"`
	AmountPaise  int64  `json:"amount_paise"`
	Amount       string `json:"amount_display"`
	ItemCount    int    `json:"item_count"`
}

// ---------------------------------------------------------------------------
// GET /admin/orders
// ---------------------------------------------------------------------------

// AdminListOrders lists orders across all customers, with filters.
func (a *API) AdminListOrders(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if _, err := adminFromRequest(r); err != nil {
		a.fail(ctx, w, err)
		return
	}

	filters, err := parseAdminOrderFilters(r)
	if err != nil {
		a.fail(ctx, w, err)
		return
	}

	orders, err := a.queries.AdminListOrders(ctx, store.AdminListOrdersParams{
		Status: filters.Status, CustomerID: filters.CustomerID,
		OrderNumber: filters.OrderNumber, SupplierID: filters.SupplierID,
		PlacedFrom: filters.From, PlacedTo: filters.To,
		Limit: filters.Limit, Offset: filters.Offset,
	})
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	total, err := a.queries.AdminCountOrders(ctx, store.AdminCountOrdersParams{
		Status: filters.Status, CustomerID: filters.CustomerID,
		OrderNumber: filters.OrderNumber, SupplierID: filters.SupplierID,
		PlacedFrom: filters.From, PlacedTo: filters.To,
	})
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	// One query for the whole page's items rather than N+1.
	ids := make([]uuid.UUID, 0, len(orders))
	for _, order := range orders {
		ids = append(ids, order.ID)
	}
	allItems, err := a.queries.ListOrderItemsForOrders(ctx, ids)
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}
	byOrder := map[uuid.UUID][]store.OrderItem{}
	for _, item := range allItems {
		byOrder[item.OrderID] = append(byOrder[item.OrderID], item)
	}

	now := time.Now()
	out := make([]orderResponse, 0, len(orders))
	for _, order := range orders {
		out = append(out, a.toOrderResponse(order, byOrder[order.ID], now))
	}

	a.respond(ctx, w, http.StatusOK, map[string]any{
		"orders": out, "total": total,
		"limit": filters.Limit, "offset": filters.Offset,
	})
}

type adminOrderFilters struct {
	Status      *string
	CustomerID  *uuid.UUID
	SupplierID  *uuid.UUID
	OrderNumber *string
	From        *time.Time
	To          *time.Time
	Limit       int32
	Offset      int32
}

var orderStatuses = map[string]struct{}{
	"pending_payment": {}, "paid": {}, "processed": {}, "dispatched": {},
	"payment_failed": {}, "expired": {}, "cancelled": {}, "refunded": {},
}

func parseAdminOrderFilters(r *http.Request) (adminOrderFilters, error) {
	q := r.URL.Query()
	filters := adminOrderFilters{Limit: 25}

	if status := strings.TrimSpace(q.Get("status")); status != "" {
		if _, ok := orderStatuses[status]; !ok {
			return filters, httpx.BadRequest("status is not a recognised order status.")
		}
		filters.Status = &status
	}
	for param, target := range map[string]**uuid.UUID{
		"customer_id": &filters.CustomerID,
		"supplier_id": &filters.SupplierID,
	} {
		if raw := strings.TrimSpace(q.Get(param)); raw != "" {
			id, err := uuid.Parse(raw)
			if err != nil {
				return filters, httpx.BadRequest(param + " must be a UUID.")
			}
			*target = &id
		}
	}
	if search := strings.TrimSpace(q.Get("order_number")); search != "" {
		// Escape LIKE wildcards so searching "VM-2608%" is literal.
		escaped := strings.NewReplacer("%", `\%`, "_", `\_`).Replace(search)
		filters.OrderNumber = &escaped
	}

	// A named period (?range=7d) is the same thing as a pair of dates, and
	// resolving it through the SAME function the dashboard uses is what keeps
	// the two screens agreeing about what "past 7 days" covers.
	//
	// Explicit from/to still win, so a custom range works unchanged.
	if named := strings.TrimSpace(q.Get("range")); named != "" && named != "custom" {
		period, err := resolveQueueRange(r)
		if err != nil {
			return filters, err
		}
		// All time leaves the bounds unset — a nil filter, not a window from
		// the zero time, which is what istDayBounds would produce from an
		// unset date.
		if !period.All {
			start, end := istDayBounds(period.From, period.To)
			filters.From, filters.To = &start, &end
		}
		// Deliberately NOT an early return: limit and offset are parsed
		// below, and returning here silently ignored them — every ranged
		// request came back with the default page size.
		return parseOrderPaging(q, filters), nil
	}

	// Dates arrive as IST calendar days and are converted to instants here, so
	// "orders from the 14th" means the IST day, not a UTC window
	// (CLAUDE.md rule 2).
	if raw := strings.TrimSpace(q.Get("from")); raw != "" {
		day, err := time.ParseInLocation("2006-01-02", raw, isttime.Location())
		if err != nil {
			return filters, httpx.BadRequest("from must be YYYY-MM-DD.")
		}
		filters.From = &day
	}
	if raw := strings.TrimSpace(q.Get("to")); raw != "" {
		day, err := time.ParseInLocation("2006-01-02", raw, isttime.Location())
		if err != nil {
			return filters, httpx.BadRequest("to must be YYYY-MM-DD.")
		}
		// Exclusive upper bound at the START of the next day, so "to=14th"
		// includes everything placed on the 14th.
		end := isttime.AddDays(day, 1)
		filters.To = &end
	}

	return parseOrderPaging(q, filters), nil
}

// parseOrderPaging reads limit and offset.
//
// Shared by both paths through the filter parser, so a ranged request and a
// dated one cannot disagree about page size — which is exactly what happened
// when the ranged branch returned early and skipped this.
func parseOrderPaging(q url.Values, filters adminOrderFilters) adminOrderFilters {
	if v, err := strconv.Atoi(q.Get("limit")); err == nil && v > 0 && v <= 100 {
		filters.Limit = int32(v)
	}
	if v, err := strconv.Atoi(q.Get("offset")); err == nil && v >= 0 {
		filters.Offset = int32(v)
	}
	return filters
}

// ---------------------------------------------------------------------------
// GET /admin/orders/{id}
// ---------------------------------------------------------------------------

// AdminGetOrder returns one order in full: items, address, payment record,
// computed timeline and per-supplier subtotals.
func (a *API) AdminGetOrder(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if _, err := adminFromRequest(r); err != nil {
		a.fail(ctx, w, err)
		return
	}
	orderID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		a.fail(ctx, w, httpx.NotFound("Order not found."))
		return
	}

	// No customer filter: an admin may read any order.
	order, err := a.queries.GetOrderByID(ctx, orderID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			a.fail(ctx, w, httpx.NotFound("Order not found."))
			return
		}
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	items, err := a.queries.ListOrderItems(ctx, order.ID)
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	// Per-supplier subtotals, computed from the LINE ITEMS — the same source
	// the payout rows come from, so the two always agree.
	shares, err := a.queries.SumOrderItemsBySupplier(ctx, order.ID)
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}
	itemCounts := map[uuid.UUID]int{}
	for _, item := range items {
		itemCounts[item.SupplierID]++
	}
	supplierIDs := make([]uuid.UUID, 0, len(shares))
	for _, share := range shares {
		supplierIDs = append(supplierIDs, share.SupplierID)
	}
	names := a.supplierNames(ctx, supplierIDs)

	subtotals := make([]supplierSubtotal, 0, len(shares))
	for _, share := range shares {
		subtotals = append(subtotals, supplierSubtotal{
			SupplierID:   share.SupplierID.String(),
			BusinessName: names[share.SupplierID],
			AmountPaise:  share.AmountPaise,
			Amount:       money.FormatRupees(money.Paise(share.AmountPaise)),
			ItemCount:    itemCounts[share.SupplierID],
		})
	}

	response := a.toOrderResponse(order, items, time.Now())

	// The payment record, if an attempt exists. Absent is normal for an order
	// still in pending_payment.
	var payment map[string]any
	if row, err := a.queries.GetPaymentForOrder(ctx, order.ID); err == nil {
		payment = map[string]any{
			"provider":            row.Provider,
			"provider_order_id":   row.ProviderOrderID,
			"provider_payment_id": row.ProviderPaymentID,
			"amount_paise":        row.AmountPaise,
			"amount_display":      money.FormatRupees(money.Paise(row.AmountPaise)),
			"status":              row.Status,
			"method":              row.Method,
			"created_at":          row.CreatedAt.Format(time.RFC3339),
		}
	}

	payouts, err := a.queries.ListPayoutsForOrder(ctx, order.ID)
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}
	payoutRows := make([]map[string]any, 0, len(payouts))
	for _, payout := range payouts {
		payoutRows = append(payoutRows, map[string]any{
			"id":             payout.ID.String(),
			"supplier_id":    payout.SupplierID.String(),
			"business_name":  names[payout.SupplierID],
			"amount_paise":   payout.AmountPaise,
			"amount_display": money.FormatRupees(money.Paise(payout.AmountPaise)),
			"status":         payout.Status,
			"reference_no":   payout.ReferenceNo,
		})
	}

	a.respond(ctx, w, http.StatusOK, map[string]any{
		"order":              response,
		"supplier_subtotals": subtotals,
		"payment":            payment,
		"payouts":            payoutRows,
		"cancelled_at":       formatNullableTime(order.CancelledAt),
		"cancel_reason":      order.CancelReason,
	})
}

func formatNullableTime(t *time.Time) *string {
	if t == nil {
		return nil
	}
	formatted := t.Format(time.RFC3339)
	return &formatted
}

// ---------------------------------------------------------------------------
// Manual transitions
// ---------------------------------------------------------------------------

// AdminDispatchOrder marks an order handed to the courier.
//
// The SQL only permits it from paid or processed, so an unpaid or cancelled
// order cannot be dispatched even if the UI offers the button.
func (a *API) AdminDispatchOrder(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	adminID, err := adminFromRequest(r)
	if err != nil {
		a.fail(ctx, w, err)
		return
	}
	orderID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		a.fail(ctx, w, httpx.NotFound("Order not found."))
		return
	}

	tx, err := a.pool.Begin(ctx)
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit
	q := a.queries.WithTx(tx)

	before, err := q.GetOrderByID(ctx, orderID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			a.fail(ctx, w, httpx.NotFound("Order not found."))
			return
		}
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	after, err := q.AdminMarkOrderDispatched(ctx, orderID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			a.fail(ctx, w, httpx.Conflict("INVALID_TRANSITION",
				"Only a paid or processed order can be dispatched. This one is "+
					before.Status+"."))
			return
		}
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	if err := a.writeAudit(ctx, q, adminID, actionOrderDispatched, "order", orderID,
		map[string]any{"status": before.Status},
		map[string]any{"status": after.Status, "order_number": after.OrderNumber}); err != nil {
		a.fail(ctx, w, err)
		return
	}
	if err := analyticsevents.Emit(ctx, q, orderID, analyticsevents.OrderDispatched,
		analyticsevents.ActorAdmin, time.Now(), analyticsevents.Options{}); err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}
	if err := tx.Commit(ctx); err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	items, _ := a.queries.ListOrderItems(ctx, after.ID)
	a.respond(ctx, w, http.StatusOK, a.toOrderResponse(after, items, time.Now()))
}

type cancelOrderRequest struct {
	Reason string `json:"reason"`
}

// AdminCancelOrder cancels an order with a recorded reason.
//
// A reason is required: CLAUDE.md §5.3 makes cancellation a mandatory audit
// action, and an audit entry that cannot answer "why" is not much of one.
func (a *API) AdminCancelOrder(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	adminID, err := adminFromRequest(r)
	if err != nil {
		a.fail(ctx, w, err)
		return
	}
	orderID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		a.fail(ctx, w, httpx.NotFound("Order not found."))
		return
	}

	var req cancelOrderRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		a.fail(ctx, w, err)
		return
	}
	reason := strings.TrimSpace(req.Reason)
	v := newValidation()
	switch {
	case reason == "":
		v.add("reason", "is required")
	case len(reason) > 500:
		v.add("reason", "is too long")
	}
	if err := v.err(); err != nil {
		a.fail(ctx, w, err)
		return
	}

	tx, err := a.pool.Begin(ctx)
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit
	q := a.queries.WithTx(tx)

	before, err := q.GetOrderByID(ctx, orderID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			a.fail(ctx, w, httpx.NotFound("Order not found."))
			return
		}
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	after, err := q.AdminCancelOrder(ctx, store.AdminCancelOrderParams{
		ID: orderID, CancelReason: &reason,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			a.fail(ctx, w, httpx.Conflict("INVALID_TRANSITION",
				"An order that is "+before.Status+" cannot be cancelled."))
			return
		}
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	// Release any stock still held, so a cancelled order does not keep
	// produce off the shelf until the sweeper notices.
	reservations, err := q.ListHeldReservationsForOrder(ctx, orderID)
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}
	holdIDs := make([]uuid.UUID, 0, len(reservations))
	for _, reservation := range reservations {
		if _, err := q.SettleReservation(ctx, store.SettleReservationParams{
			ID: reservation.ID, Status: "released",
		}); err != nil {
			a.fail(ctx, w, httpx.Internal(err))
			return
		}
		holdIDs = append(holdIDs, reservation.CatalogReservationID)
	}

	if err := a.writeAudit(ctx, q, adminID, actionOrderCancelled, "order", orderID,
		map[string]any{"status": before.Status},
		map[string]any{
			"status": after.Status, "reason": reason,
			"order_number": after.OrderNumber, "reservations_released": len(holdIDs),
		}); err != nil {
		a.fail(ctx, w, err)
		return
	}
	// The reason stays in the audit log: it is admin free text that often
	// names the customer, and the analytics event carries no free text.
	if err := analyticsevents.Emit(ctx, q, orderID, analyticsevents.OrderCancelled,
		analyticsevents.ActorAdmin, time.Now(),
		analyticsevents.Options{CancelledFromStatus: before.Status}); err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}
	if err := tx.Commit(ctx); err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	if len(holdIDs) > 0 {
		if err := a.catalog.Settle(ctx, outcomeReleased, holdIDs); err != nil {
			a.logger.WarnContext(ctx, "cancelled but stock not released; sweeper will reclaim",
				slog2(err))
		}
	}

	items, _ := a.queries.ListOrderItems(ctx, after.ID)
	a.respond(ctx, w, http.StatusOK, a.toOrderResponse(after, items, time.Now()))
}

// ---------------------------------------------------------------------------
// GET /admin/dashboard
// ---------------------------------------------------------------------------

// parseISTDate reads a YYYY-MM-DD parameter as an IST calendar day.
//
// Parsed IN Asia/Kolkata rather than UTC: "2026-08-15" means that day as the
// business understands it, and parsing it as UTC midnight would shift the
// boundary five and a half hours and quietly move orders between days.
//
// An empty string is an error here, unlike the catalogue's equivalent — a
// custom range with a missing end date should be refused, not silently turned
// into today.
// AdminDashboard returns trading figures for a period.
//
// The period defaults to today and is always resolved server-side in IST — the
// browser's clock is never consulted (CLAUDE.md rule 2).
//
// Two figures deliberately ignore the range: outstanding settlement and
// pending supplier applications are both "right now" queue depths, not
// activity during a window. Scoping them to last month would report a backlog
// that has since been cleared.
func (a *API) AdminDashboard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if _, err := adminFromRequest(r); err != nil {
		a.fail(ctx, w, err)
		return
	}

	period, err := resolveDashboardRange(r)
	if err != nil {
		a.fail(ctx, w, err)
		return
	}

	figures, err := a.queries.DashboardRange(ctx, store.DashboardRangeParams{
		PlacedAt: period.From, PlacedAt_2: period.To,
	})
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}
	outstanding, err := a.queries.DashboardOutstanding(ctx)
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	// Supplier applications live in the profile schema. Best-effort: the rest
	// of the dashboard is still useful if that call fails.
	pendingApplications := a.pendingSupplierApplications(ctx)

	// Every figure below is read from what actually happened, not recomputed
	// from a rate.
	//
	// The platform fee is the sum stored on the orders; the commission is the
	// difference between growers' line totals and what their payouts actually
	// paid them; the delivery margin is the flat amount times the number of
	// paid orders. Recomputing commission from the configured rate was wrong
	// the moment rates became per-supplier — it reported the platform rate
	// applied to everyone, which is plausible enough to go unnoticed.
	collected, err := a.queries.CommissionCollectedInRange(ctx,
		store.CommissionCollectedInRangeParams{
			PlacedAt: period.From, PlacedAt_2: period.To,
		})
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	commissionPaise := money.Paise(collected.SupplierGrossPaise - collected.SupplierNetPaise)
	if commissionPaise < 0 {
		// Payouts exceeding gross would mean a data problem, not a negative
		// commission. Report zero rather than a figure that cannot be true.
		commissionPaise = 0
	}

	// Markup: what we added to growers' prices on the orders in this period,
	// read from the snapshot on each line rather than from the products'
	// current markup, which an admin may have changed since.
	markupPaise, err := a.queries.MarkupCollectedInRange(ctx,
		store.MarkupCollectedInRangeParams{
			PlacedAt: period.From, PlacedAt_2: period.To,
		})
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	earnings := pricing.Earnings{
		PlatformFeePaise:        money.Paise(figures.PlatformFeesTodayPaise),
		SupplierCommissionPaise: commissionPaise,
		DeliveryMarginPaise:     a.Rates.DeliveryMarginPaise.Mul(figures.PaidOrdersToday),
		MarkupPaise:             money.Paise(markupPaise),
	}
	earnings.TotalPaise = earnings.Total()

	a.respond(ctx, w, http.StatusOK, map[string]any{
		// `date` is retained as the END of the period so anything still
		// reading it shows a sensible day rather than breaking.
		"date": period.To.Format("2006-01-02"),
		"range": map[string]any{
			"key":   period.Key,
			"label": period.Label,
			"from":  period.From.Format("2006-01-02"),
			"to":    period.To.Format("2006-01-02"),
			"days":  int(period.To.Sub(period.From).Hours()/24) + 1,
		},
		"orders_today": figures.OrdersToday,
		// GMV counts paid orders only — a pending_payment order is not revenue.
		"gmv_today_paise": figures.GmvTodayPaise,
		"gmv_today":       money.FormatRupees(money.Paise(figures.GmvTodayPaise)),
		// What we actually earn today, and where it comes from.
		//
		// Four sources with different shapes: the platform fee and supplier
		// commission scale with basket size, the delivery margin is flat per
		// paid order, and markup is flat per pack sold. A single blended
		// figure hides which one is paying for the business, so the breakdown
		// is returned alongside the total rather than left for the UI to
		// reconstruct.
		"earnings_today_paise": earnings.TotalPaise,
		"earnings_today":       money.FormatRupees(earnings.TotalPaise),
		"earnings_breakdown": map[string]any{
			"platform_fee_paise":        int64(earnings.PlatformFeePaise),
			"platform_fee":              money.FormatRupees(earnings.PlatformFeePaise),
			"platform_fee_rate":         bpsLabel(a.Rates.PlatformFeeBPS),
			"supplier_commission_paise": int64(earnings.SupplierCommissionPaise),
			"supplier_commission":       money.FormatRupees(earnings.SupplierCommissionPaise),
			// The DEFAULT rate, clearly labelled as such. Suppliers can be on
			// negotiated rates, so a single percentage cannot describe the
			// amount beside it — the UI says "default 3%" rather than
			// implying every grower paid it.
			"supplier_commission_default_rate": bpsLabel(a.Rates.SupplierCommissionBPS),
			"supplier_gross_paise":             collected.SupplierGrossPaise,
			"supplier_net_paise":               collected.SupplierNetPaise,
			"delivery_margin_paise":            int64(earnings.DeliveryMarginPaise),
			"delivery_margin":                  money.FormatRupees(earnings.DeliveryMarginPaise),
			"delivery_margin_per_order":        money.FormatRupees(a.Rates.DeliveryMarginPaise),
			"paid_orders":                      figures.PaidOrdersToday,
			// The fourth source: a flat amount per pack, set per product in
			// the admin products screen and included in what the customer
			// paid. No rate to quote beside it — every product has its own.
			"markup_paise": int64(earnings.MarkupPaise),
			"markup":       money.FormatRupees(earnings.MarkupPaise),
		},
		// Kept for anything still reading the old key.
		"platform_fees_today_paise":      figures.PlatformFeesTodayPaise,
		"platform_fees_today":            money.FormatRupees(money.Paise(figures.PlatformFeesTodayPaise)),
		"outstanding_to_suppliers_paise": outstanding,
		"outstanding_to_suppliers":       money.FormatRupees(money.Paise(outstanding)),
		"awaiting_processing":            figures.AwaitingProcessing,
		"pending_supplier_applications":  pendingApplications,
	})
}

type recordPaymentRequest struct {
	// Reference is how this payment can be traced outside our system — a NEFT
	// UTR, a UPI reference, a cash receipt number.
	Reference string `json:"reference"`
	Method    string `json:"method"`
	Notes     string `json:"notes"`
}

// AdminRecordPayment marks an order paid for a payment taken outside Razorpay.
//
// Razorpay Checkout is not switched on yet, so orders rest in pending_payment
// while payment is arranged directly with the customer. This is how that
// payment gets recorded — and it is deliberately NOT a free-form status
// editor. Marking an order paid is what creates the supplier payout rows, so
// letting an admin type any status into any order would silently decide who
// gets paid. The only transition offered is the one that has a defined
// meaning: pending_payment -> paid.
//
// It runs applyOrderPaid, the same transaction the Razorpay webhook runs, so
// stock, payouts and the confirmation land identically whichever way the money
// arrived. A reference is required: an audit row that cannot answer "which
// payment?" is not much of one, and this is the entry point most exposed to
// mistakes.
func (a *API) AdminRecordPayment(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	adminID, err := adminFromRequest(r)
	if err != nil {
		a.fail(ctx, w, err)
		return
	}
	orderID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		a.fail(ctx, w, httpx.NotFound("Order not found."))
		return
	}

	var req recordPaymentRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		a.fail(ctx, w, err)
		return
	}
	reference := strings.TrimSpace(req.Reference)
	if reference == "" {
		a.fail(ctx, w, httpx.Validation(
			"A payment reference is required so this can be traced later.",
			map[string]any{"field": "reference"}))
		return
	}
	if len(reference) > 100 {
		a.fail(ctx, w, httpx.Validation("Reference is too long.",
			map[string]any{"field": "reference", "max": 100}))
		return
	}
	method := strings.TrimSpace(req.Method)
	if method == "" {
		method = "offline"
	}

	// Resolved BEFORE the transaction opens: this is an HTTP call to
	// vm-profile-api, and making it while holding a pooled connection would
	// let a slow lookup drain the pool with Postgres sitting idle on locks.
	commissions := a.supplierCommissions(ctx, supplierIDsOnOrder(ctx, a.queries, orderID))

	tx, err := a.pool.Begin(ctx)
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit
	q := a.queries.WithTx(tx)

	before, err := q.GetOrderByID(ctx, orderID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			a.fail(ctx, w, httpx.NotFound("Order not found."))
			return
		}
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	rawPayload, _ := json.Marshal(map[string]any{
		"source": "admin", "reference": reference, "method": method,
		"notes": strings.TrimSpace(req.Notes), "admin_user_id": adminID.String(),
	})

	paid, holdIDs, payoutCount, ok, err := applyOrderPaid(
		ctx, q, orderID, &reference, method, analyticsevents.PaidViaAdmin, rawPayload, a.Rates, commissions)
	if err != nil {
		// payments.provider_payment_id is unique, which is what stops one bank
		// transfer being credited to two orders. Reaching it means the admin
		// has pasted a reference already recorded elsewhere — a mistake worth
		// naming, not a server error to shrug at.
		if isUniqueViolation(err) {
			a.fail(ctx, w, httpx.Conflict("REFERENCE_ALREADY_USED",
				"That payment reference is already recorded against another "+
					"order. Check the reference and try again."))
			return
		}
		a.fail(ctx, w, httpx.Internal(err))
		return
	}
	if !ok {
		// Not pending_payment. Naming the current status matters — "already
		// paid" and "cancelled" call for very different next steps.
		a.fail(ctx, w, httpx.Conflict("INVALID_TRANSITION",
			"Only an order awaiting payment can be marked paid. This one is "+
				before.Status+"."))
		return
	}

	if err := a.writeAudit(ctx, q, adminID, actionOrderPaidManually, "order", orderID,
		map[string]any{"status": before.Status, "payment_status": before.PaymentStatus},
		map[string]any{
			"status": paid.Status, "payment_status": paid.PaymentStatus,
			"order_number": paid.OrderNumber, "reference": reference,
			"method": method, "notes": strings.TrimSpace(req.Notes),
			"total_paise": paid.TotalPaise, "payouts_created": payoutCount,
		}); err != nil {
		a.fail(ctx, w, err)
		return
	}

	if err := tx.Commit(ctx); err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	a.settleStockAfterCommit(ctx, orderID, holdIDs)

	a.logger.InfoContext(ctx, "order paid",
		slog.String("order_id", paid.ID.String()),
		slog.String("order_number", paid.OrderNumber),
		slog.String("via", "admin"),
		slog.Int("payouts", payoutCount))

	items, _ := a.queries.ListOrderItems(ctx, paid.ID)
	a.respond(ctx, w, http.StatusOK, a.toOrderResponse(paid, items, time.Now()))
}

// bpsLabel renders basis points as a human percentage: 300 -> "3%".
//
// Integer arithmetic, so 250 becomes "2.5%" without a float ever touching a
// rate that decides money.
func bpsLabel(bps int64) string { return money.FormatBPS(bps) }
