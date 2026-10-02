package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vayal-mikrogreenz/vm-go-common/httpx"
	"github.com/vayal-mikrogreenz/vm-go-common/isttime"
	"github.com/vayal-mikrogreenz/vm-go-common/money"
	"github.com/vayal-mikrogreenz/vm-go-common/produce"

	"github.com/vayal-mikrogreenz/vm-orders-api/internal/analyticsevents"
	"github.com/vayal-mikrogreenz/vm-orders-api/internal/catalogclient"
	"github.com/vayal-mikrogreenz/vm-orders-api/internal/pricing"
	"github.com/vayal-mikrogreenz/vm-orders-api/internal/store"
	"github.com/vayal-mikrogreenz/vm-orders-api/internal/timeline"
)

// resolvedLine pairs a cart row with the live catalogue unit it resolved to.
type resolvedLine struct {
	item store.CartItem
	unit catalogclient.Unit
}

type placeOrderRequest struct {
	AddressID string `json:"address_id"`
}

// PlaceOrder creates an order and reserves its stock — CLAUDE.md §6.3.
//
// Idempotent via the Idempotency-Key header (rule 6): replaying a request
// returns the ORIGINAL order rather than reserving stock twice. This matters
// because the client retries on a flaky connection, and the customer must not
// end up with two orders for one checkout.
//
// The order is left in pending_payment. Razorpay is deliberately out of scope
// for this phase.
func (a *API) PlaceOrder(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	customerID, err := customerFromRequest(r)
	if err != nil {
		a.fail(ctx, w, err)
		return
	}

	idempotencyKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if idempotencyKey == "" {
		a.fail(ctx, w, httpx.BadRequest(
			"An Idempotency-Key header is required to place an order."))
		return
	}
	if len(idempotencyKey) > 200 {
		a.fail(ctx, w, httpx.BadRequest("Idempotency-Key is too long."))
		return
	}

	// Fast path: a key we have already seen returns the original order without
	// touching stock at all.
	if existing, err := a.queries.LookupIdempotencyKey(ctx, store.LookupIdempotencyKeyParams{
		CustomerID: customerID, Key: idempotencyKey,
	}); err == nil {
		a.respondWithOrder(ctx, w, customerID, existing.OrderID, http.StatusOK)
		return
	} else if !errors.Is(err, pgx.ErrNoRows) {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	var req placeOrderRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		a.fail(ctx, w, err)
		return
	}
	addressID, err := uuid.Parse(req.AddressID)
	if err != nil {
		a.fail(ctx, w, httpx.Validation("A delivery address is required.",
			map[string]any{"address_id": "must be a UUID"}))
		return
	}

	address, err := a.fetchAddress(ctx, customerID, addressID)
	if err != nil {
		a.fail(ctx, w, err)
		return
	}

	cart, items, units, err := a.cartUnits(ctx, customerID)
	if err != nil {
		a.fail(ctx, w, err)
		return
	}
	if len(items) == 0 {
		a.fail(ctx, w, httpx.Conflict("CART_EMPTY", "Your cart is empty."))
		return
	}

	// Re-validate EVERY line against live availability. The cart was priced
	// when it was last read; between then and now a supplier may have changed
	// a price or sold out.
	lines := make([]pricing.Line, 0, len(items))
	// Keyed by GRADE, because that is what owns the gram pool: two lines of
	// the same produce at different grades draw on different crates.
	gramsBySizeCode := map[uuid.UUID]int32{}
	resolvedItems := make([]resolvedLine, 0, len(items))

	for _, item := range items {
		unit, found := units[item.ProductUnitID]
		if !found {
			a.fail(ctx, w, httpx.Conflict("ITEM_UNAVAILABLE",
				"An item in your cart is no longer available. Please review your cart."))
			return
		}
		if !unit.Purchasable {
			a.fail(ctx, w, httpx.Conflict("INSUFFICIENT_STOCK",
				fmt.Sprintf("%s is sold out in that pack size. Please review your cart.",
					unitDisplayName(unit))))
			return
		}

		lines = append(lines, pricing.Line{
			SupplierID:     unit.SupplierID,
			UnitPricePaise: unit.PricePaise,
			Qty:            int64(item.Qty),
		})
		gramsBySizeCode[unit.SizeCodeID] += unit.WeightGrams * item.Qty
		resolvedItems = append(resolvedItems, resolvedLine{item: item, unit: unit})
	}

	breakdown := pricing.Compute(lines, a.pricing)

	// The order id is minted before reserving so the holds can reference it.
	orderID := uuid.New()

	reserveLines := make([]catalogclient.ReserveLine, 0, len(gramsBySizeCode))
	for sizeCodeID, grams := range gramsBySizeCode {
		reserveLines = append(reserveLines, catalogclient.ReserveLine{
			SizeCodeID: sizeCodeID, Grams: grams,
		})
	}
	// Deterministic order, matching the lock order catalog uses — by size code
	// id, which is what it now locks on.
	sort.Slice(reserveLines, func(i, j int) bool {
		return reserveLines[i].SizeCodeID.String() < reserveLines[j].SizeCodeID.String()
	})

	// Stock is held BEFORE the order row exists. If the write below fails, the
	// holds expire on their own (that is what the TTL is for) rather than
	// silently locking stock forever.
	holds, err := a.catalog.Reserve(ctx, orderID, a.reserveTTL, reserveLines)
	if err != nil {
		// Catalog's conflict errors are already customer-safe.
		var appErr *httpx.Error
		if errors.As(err, &appErr) {
			a.fail(ctx, w, appErr)
			return
		}
		a.fail(ctx, w, httpx.Unavailable("We could not hold your items. Please try again."))
		return
	}

	// Computed ONCE, here, and stored (CLAUDE.md §6.1).
	now := time.Now()
	schedule := timeline.Compute(now, a.cutoffHour)

	order, err := a.persistOrder(ctx, persistArgs{
		orderID:        orderID,
		customerID:     customerID,
		cartID:         cart.ID,
		idempotencyKey: idempotencyKey,
		address:        address,
		breakdown:      breakdown,
		schedule:       schedule,
		items:          resolvedItems,
		holds:          holds,
		now:            now,
	})
	if err != nil {
		// Release immediately rather than waiting for the sweeper: the
		// customer may well retry within seconds.
		a.releaseHolds(ctx, holds)

		// A duplicate key means a concurrent request with the SAME
		// Idempotency-Key won the race. Return that order.
		if isUniqueViolation(err) {
			if existing, lookupErr := a.queries.LookupIdempotencyKey(ctx,
				store.LookupIdempotencyKeyParams{CustomerID: customerID, Key: idempotencyKey},
			); lookupErr == nil {
				a.respondWithOrder(ctx, w, customerID, existing.OrderID, http.StatusOK)
				return
			}
		}
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	// Register the order with Razorpay so the browser can open Checkout
	// against it (CLAUDE.md §6.4 step 1). Done AFTER our own commit: if the
	// provider is down, the order exists in pending_payment and the customer
	// can retry payment rather than losing the whole checkout.
	rzpOrder, rzpErr := a.razorpay.CreateOrder(ctx,
		money.Paise(order.TotalPaise), order.OrderNumber)
	if rzpErr != nil {
		a.logger.ErrorContext(ctx, "could not create the Razorpay order",
			slog.String("order_id", order.ID.String()), slog.Any("error", rzpErr))
	} else {
		if _, err := a.queries.SetOrderRazorpayOrderID(ctx,
			store.SetOrderRazorpayOrderIDParams{ID: order.ID, RazorpayOrderID: &rzpOrder.ID},
		); err != nil {
			a.logger.ErrorContext(ctx, "could not store the Razorpay order id",
				slog.String("order_id", order.ID.String()), slog.Any("error", err))
		} else {
			order.RazorpayOrderID = &rzpOrder.ID
		}
		// The payment row tracks the attempt from creation, so a capture
		// webhook always has something to update.
		if _, err := a.queries.CreatePayment(ctx, store.CreatePaymentParams{
			OrderID: order.ID, ProviderOrderID: &rzpOrder.ID,
			AmountPaise: order.TotalPaise, Status: "created",
		}); err != nil {
			a.logger.ErrorContext(ctx, "could not record the payment attempt",
				slog.Any("error", err))
		}
	}

	orderItems, err := a.queries.ListOrderItems(ctx, order.ID)
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	response := a.toOrderResponse(order, orderItems, time.Now())
	// What the browser needs to open Checkout. The key ID is public by
	// design; the key SECRET never leaves this service.
	response.RazorpayOrderID = order.RazorpayOrderID
	response.RazorpayKeyID = a.razorpayKeyID
	a.respond(ctx, w, http.StatusCreated, response)
}

type persistArgs struct {
	orderID        uuid.UUID
	customerID     uuid.UUID
	cartID         uuid.UUID
	idempotencyKey string
	address        addressSnapshot
	breakdown      pricing.Breakdown
	schedule       timeline.Schedule
	items          []resolvedLine
	holds          []catalogclient.Hold
	now            time.Time
}

// persistOrder writes the order, its items, the reservation mirror, the
// idempotency claim and the outbox event in ONE transaction.
func (a *API) persistOrder(ctx context.Context, args persistArgs) (store.Order, error) {
	tx, err := a.pool.Begin(ctx)
	if err != nil {
		return store.Order{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit

	q := a.queries.WithTx(tx)
	order, err := a.writeOrder(ctx, q, args)
	if err != nil {
		return store.Order{}, err
	}
	// Emitted by the callers of writeOrder rather than inside it: a scheduled
	// run links the order to its schedule AFTER writeOrder, and the event must
	// carry that link.
	if err := analyticsevents.Emit(ctx, q, order.ID, analyticsevents.OrderPlaced,
		analyticsevents.ActorCustomer, order.PlacedAt, analyticsevents.Options{}); err != nil {
		return store.Order{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return store.Order{}, err
	}
	return order, nil
}

// writeOrder is persistOrder's body, against a transaction the CALLER owns.
//
// Split out for the scheduled-delivery run (CLAUDE.md §6.7), which must write
// the order, debit the wallet and mark it paid in one transaction: an order
// without its debit, or a debit without its order, is money out of step with
// goods. A scheduled order has no cart and no Idempotency-Key — its schedule
// occurrence is the idempotency guard — so both steps are skipped when their
// argument is empty.
func (a *API) writeOrder(ctx context.Context, q *store.Queries, args persistArgs) (store.Order, error) {
	orderNumber, err := a.nextOrderNumber(ctx, q, args.now)
	if err != nil {
		return store.Order{}, err
	}

	addressJSON, err := json.Marshal(args.address)
	if err != nil {
		return store.Order{}, err
	}

	order, err := q.CreateOrder(ctx, store.CreateOrderParams{
		OrderNumber:          orderNumber,
		CustomerID:           args.customerID,
		Status:               statusPendingPayment,
		AddressSnapshot:      addressJSON,
		SubtotalPaise:        args.breakdown.SubtotalPaise.Int64(),
		PlatformFeePaise:     args.breakdown.PlatformFeePaise.Int64(),
		DeliveryFeePaise:     args.breakdown.DeliveryFeePaise.Int64(),
		TotalPaise:           args.breakdown.TotalPaise.Int64(),
		PlacedAt:             args.now,
		ProcessingAt:         args.schedule.ProcessingAt,
		DeliveryDay:          args.schedule.DeliveryDay,
		ExpectedDeliveryDate: args.schedule.ExpectedDeliveryDate,
	})
	if err != nil {
		return store.Order{}, err
	}

	for i, entry := range args.items {
		grade := entry.unit.Grade
		var gradePtr *string
		if grade != "" {
			gradePtr = &grade
		}
		// The category, snapshotted for the same reason as the grade: it lives
		// in the catalog schema, which this service may not read, so a line
		// without it can never be categorised afterwards. An empty string
		// stores as NULL and reports as unrecorded rather than as 'other'.
		productType := entry.unit.Type
		var typePtr *string
		if productType != "" {
			typePtr = &productType
		}
		// The GRADE, snapshotted for the same reason: it decides the price and
		// lives in the catalog schema this service may not read. The implicit
		// 'STD' of an ungraded listing is deliberately NOT recorded — writing
		// it would invent a grade the customer never chose.
		var sizeCodePtr, sizeMetaPtr *string
		var sizeCodeID *uuid.UUID
		if grade := produce.GradeLabel(entry.unit.SizeCode); grade != "" {
			sizeCode := grade
			sizeCodePtr = &sizeCode
			id := entry.unit.SizeCodeID
			sizeCodeID = &id
			if entry.unit.SizeMeta != "" {
				sizeMeta := entry.unit.SizeMeta
				sizeMetaPtr = &sizeMeta
			}
		}
		// Everything is snapshotted: the order must render correctly even if
		// the product is later edited or archived (CLAUDE.md §5.3).
		qty := entry.item.Qty
		if _, err := q.CreateOrderItem(ctx, store.CreateOrderItemParams{
			OrderID:             order.ID,
			SupplierID:          entry.unit.SupplierID,
			ProductID:           entry.unit.ProductID,
			ProductUnitID:       entry.unit.ID,
			SizeCodeID:          sizeCodeID,
			SizeCodeSnapshot:    sizeCodePtr,
			SizeMetaSnapshot:    sizeMetaPtr,
			ProductNameSnapshot: entry.unit.ProductName,
			UnitLabelSnapshot:   entry.unit.Label,
			GradeSnapshot:       gradePtr,
			ProductTypeSnapshot: typePtr,
			WeightGrams:         entry.unit.WeightGrams,
			UnitPricePaise:      entry.unit.PricePaise.Int64(),
			Qty:                 qty,
			LineTotalPaise:      args.breakdown.LineTotals[i].Int64(),
			// The markup is snapshotted for the same reason as the price: an
			// admin repricing this product tomorrow must not restate what
			// this grower was owed today. unit_price_paise above is what the
			// CUSTOMER paid and includes this amount; the payout query
			// subtracts it (CLAUDE.md §6.2).
			MarkupBps:       entry.unit.MarkupBPS,
			MarkupPaise:     entry.unit.MarkupPaise.Int64(),
			LineMarkupPaise: entry.unit.MarkupPaise.Mul(int64(qty)).Int64(),
		}); err != nil {
			return store.Order{}, err
		}
	}

	for _, hold := range args.holds {
		if _, err := q.CreateStockReservation(ctx, store.CreateStockReservationParams{
			OrderID:              order.ID,
			ProductID:            hold.ProductID,
			CatalogReservationID: hold.HoldID,
			AvailableOn:          isttime.Today(),
			Grams:                hold.Grams,
			ExpiresAt:            hold.ExpiresAt,
		}); err != nil {
			return store.Order{}, err
		}
	}

	// The idempotency claim is inside the transaction: it commits with the
	// order or not at all, so a key can never point at an order that does not
	// exist.
	if args.idempotencyKey != "" {
		if _, err := q.ClaimIdempotencyKey(ctx, store.ClaimIdempotencyKeyParams{
			Key: args.idempotencyKey, CustomerID: args.customerID, OrderID: order.ID,
		}); err != nil {
			return store.Order{}, err
		}
	}

	// Same transaction as the state change (CLAUDE.md §5.3), so an email can
	// never be promised for an order that rolled back.
	payload, _ := json.Marshal(map[string]any{
		"order_id": order.ID.String(), "order_number": order.OrderNumber,
	})
	if _, err := q.EnqueueOutbox(ctx, store.EnqueueOutboxParams{
		AggregateType: "order", AggregateID: order.ID,
		EventType: "order.created", Payload: payload,
	}); err != nil {
		return store.Order{}, err
	}

	// The cart is consumed by a successful checkout.
	if args.cartID != uuid.Nil {
		if _, err := q.ClearCart(ctx, args.cartID); err != nil {
			return store.Order{}, err
		}
	}

	return order, nil
}

// nextOrderNumber builds VM-YYMMDD-NNNN, serial within the IST day.
//
// Human-friendly because it is what a customer reads out to support. The
// counter is per IST day, matching how the operations team thinks about work.
func (a *API) nextOrderNumber(ctx context.Context, q *store.Queries, now time.Time) (string, error) {
	today := isttime.StartOfDayIST(now)
	count, err := q.CountOrdersPlacedOn(ctx, today)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("VM-%s-%04d", today.Format("060102"), count+1), nil
}

func (a *API) releaseHolds(ctx context.Context, holds []catalogclient.Hold) {
	ids := make([]uuid.UUID, 0, len(holds))
	for _, hold := range holds {
		ids = append(ids, hold.HoldID)
	}
	if err := a.catalog.Settle(ctx, outcomeReleased, ids); err != nil {
		// Not fatal: the sweeper releases them when the TTL elapses.
		a.logger.WarnContext(ctx, "could not release holds; leaving them to expire",
			slog2(err))
	}
}

func (a *API) respondWithOrder(
	ctx context.Context, w http.ResponseWriter, customerID, orderID uuid.UUID, status int,
) {
	order, err := a.queries.GetOrderForCustomer(ctx, store.GetOrderForCustomerParams{
		ID: orderID, CustomerID: customerID,
	})
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}
	items, err := a.queries.ListOrderItems(ctx, order.ID)
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}
	a.respond(ctx, w, status, a.toOrderResponse(order, items, time.Now()))
}

// ---------------------------------------------------------------------------
// GET /orders and GET /orders/{id}
// ---------------------------------------------------------------------------

// ListOrders returns the customer's own orders, newest first.
func (a *API) ListOrders(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	customerID, err := customerFromRequest(r)
	if err != nil {
		a.fail(ctx, w, err)
		return
	}

	limit, offset := int32(20), int32(0)
	if v, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && v > 0 && v <= 100 {
		limit = int32(v)
	}
	if v, err := strconv.Atoi(r.URL.Query().Get("offset")); err == nil && v >= 0 {
		offset = int32(v)
	}

	orders, err := a.queries.ListOrdersForCustomer(ctx, store.ListOrdersForCustomerParams{
		CustomerID: customerID, Limit: limit, Offset: offset,
	})
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}
	total, err := a.queries.CountOrdersForCustomer(ctx, customerID)
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	// One query for every order's items rather than N+1.
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
	// Thumbnails for the cards. One catalogue call for the whole page, and a
	// failure leaves placeholders rather than breaking the history.
	a.attachItemImages(ctx, out)

	a.respond(ctx, w, http.StatusOK, map[string]any{
		"orders": out, "total": total, "limit": limit, "offset": offset,
	})
}

// GetOrder returns one of the customer's own orders with its live timeline.
func (a *API) GetOrder(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	customerID, err := customerFromRequest(r)
	if err != nil {
		a.fail(ctx, w, err)
		return
	}
	orderID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		a.fail(ctx, w, httpx.NotFound("Order not found."))
		return
	}

	// Ownership is in the WHERE clause: another customer's id returns no rows.
	order, err := a.queries.GetOrderForCustomer(ctx, store.GetOrderForCustomerParams{
		ID: orderID, CustomerID: customerID,
	})
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
	// Milestones are derived from `now` here, at read time.
	response := a.toOrderResponse(order, items, time.Now())
	// The same shape as the list, so a card and the page it opens cannot show
	// one produce photo between them.
	a.attachItemImages(ctx, []orderResponse{response})

	// What the browser needs to RESUME payment. An order only reaches this
	// page unpaid when Checkout was dismissed or the tab was closed, and
	// without these the customer would have no way back to the payment sheet
	// short of rebuilding the cart — the stock is already reserved against
	// this order, so a second order could not even be placed for it.
	// Sent only while payment is still open: a paid order has nothing to pay.
	if order.Status == statusPendingPayment {
		response.RazorpayOrderID = order.RazorpayOrderID
		response.RazorpayKeyID = a.razorpayKeyID
	}
	a.respond(ctx, w, http.StatusOK, response)
}

var _ = money.Paise(0) // keep the import meaningful across build tags
