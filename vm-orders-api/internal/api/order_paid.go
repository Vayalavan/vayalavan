package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vayal-mikrogreenz/vm-go-common/money"

	"github.com/vayal-mikrogreenz/vm-orders-api/internal/analyticsevents"
	"github.com/vayal-mikrogreenz/vm-orders-api/internal/pricing"
	"github.com/vayal-mikrogreenz/vm-orders-api/internal/store"
)

// applyOrderPaid performs every in-transaction step of an order becoming paid.
//
// Extracted so the Razorpay webhook and the admin's manual "record payment"
// run the SAME code. These two paths must agree exactly — both commit stock,
// both create supplier payout rows, both queue the confirmation — and two
// hand-written copies would drift the moment one was edited. A supplier's
// settlement depends on the payout row existing, so a path that forgets it
// means a grower is never paid.
//
// The caller owns the transaction and is responsible for telling the catalogue
// about the returned hold IDs AFTER commit.
//
// Returns ok=false when the order was not in pending_payment — already paid,
// or cancelled. That is not an error: it is what a replay looks like.
func applyOrderPaid(
	ctx context.Context,
	q *store.Queries,
	orderID uuid.UUID,
	// Provider payment reference. For an offline payment this is the admin's
	// own reference (a UTR, a cash receipt number), not a Razorpay id.
	paymentRef *string,
	method string,
	// Which path paid it, for analytics: analyticsevents.PaidVia*.
	paidVia string,
	rawPayload []byte,
	rates pricing.Rates,
	// commissions maps supplier id -> their negotiated rate in basis points.
	// Resolved by the CALLER before the transaction opened, because filling it
	// in here would mean an HTTP round trip to vm-profile-api while holding a
	// pooled connection and locks on the order being paid. A supplier absent
	// from the map is on the platform default.
	commissions map[uuid.UUID]int64,
) (order store.Order, holdIDs []uuid.UUID, payouts int, ok bool, err error) {
	// Filtered on status='pending_payment' inside the UPDATE, so this is the
	// concurrency guard as well as the state transition: two simultaneous
	// callers cannot both win.
	paid, err := q.MarkOrderPaid(ctx, store.MarkOrderPaidParams{
		ID: orderID, RazorpayPaymentID: paymentRef,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return store.Order{}, nil, 0, false, nil
		}
		return store.Order{}, nil, 0, false, err
	}

	if _, err := q.MarkPaymentCaptured(ctx, store.MarkPaymentCapturedParams{
		OrderID: orderID, ProviderPaymentID: paymentRef,
		Method: &method, RawPayload: rawPayload,
	}); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return store.Order{}, nil, 0, false, err
	}

	// Reservations -> committed. The catalogue side (reserved_grams ->
	// sold_grams) is settled by the caller after commit.
	reservations, err := q.ListHeldReservationsForOrder(ctx, orderID)
	if err != nil {
		return store.Order{}, nil, 0, false, err
	}
	holdIDs = make([]uuid.UUID, 0, len(reservations))
	for _, reservation := range reservations {
		if _, err := q.SettleReservation(ctx, store.SettleReservationParams{
			ID: reservation.ID, Status: "committed",
		}); err != nil {
			return store.Order{}, nil, 0, false, err
		}
		holdIDs = append(holdIDs, reservation.CatalogReservationID)
	}

	// One payout row per supplier on the order (CLAUDE.md §5.3). Suppliers
	// receive their listed prices in full; our fees are not deducted.
	shares, err := q.SumOrderItemsBySupplier(ctx, orderID)
	if err != nil {
		return store.Order{}, nil, 0, false, err
	}
	for _, share := range shares {
		// The payout row stores what the supplier actually RECEIVES: their
		// line totals less our commission. Storing the gross and deducting at
		// display time would mean the settlement screen, the CSV export and
		// the supplier's own page each had to remember to subtract, and one of
		// them eventually would not.
		// Their own rate if they have one, the platform default otherwise.
		var override *int64
		if bps, found := commissions[share.SupplierID]; found {
			override = &bps
		}
		payable := int64(rates.SupplierPayableAt(money.Paise(share.AmountPaise), override))

		// ON CONFLICT DO NOTHING: idempotent, so a replay cannot double-pay.
		if _, err := q.CreateSupplierPayout(ctx, store.CreateSupplierPayoutParams{
			SupplierID: share.SupplierID, OrderID: orderID, AmountPaise: payable,
		}); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return store.Order{}, nil, 0, false, err
		}
	}

	// Written to the outbox in the SAME transaction as the state change
	// (CLAUDE.md §5.3), so an email is never promised for an order that
	// rolled back.
	payload, _ := json.Marshal(map[string]any{
		"order_id": orderID.String(), "order_number": paid.OrderNumber,
	})
	if _, err := q.EnqueueOutbox(ctx, store.EnqueueOutboxParams{
		AggregateType: "order", AggregateID: orderID,
		EventType: "order.confirmed", Payload: payload,
	}); err != nil {
		return store.Order{}, nil, 0, false, err
	}

	// Here rather than in each caller, for the reason this function exists:
	// every path that pays an order must report it, and one copy cannot forget.
	actor := analyticsevents.ActorSystem
	if paidVia == analyticsevents.PaidViaAdmin {
		actor = analyticsevents.ActorAdmin
	}
	if err := analyticsevents.Emit(ctx, q, orderID, analyticsevents.OrderPaid, actor,
		time.Now(), analyticsevents.Options{PaymentMethod: method, PaidVia: paidVia}); err != nil {
		return store.Order{}, nil, 0, false, err
	}

	return paid, holdIDs, len(shares), true, nil
}

// settleStockAfterCommit tells the catalogue that held grams are now sold.
//
// Always called AFTER our own commit. If it fails the grams stay reserved in
// the catalogue rather than sold — stock is under-reported until reconciled,
// which is far safer than releasing grams for an order that IS paid.
func (a *API) settleStockAfterCommit(
	ctx context.Context, orderID uuid.UUID, holdIDs []uuid.UUID,
) {
	if err := a.catalog.Settle(ctx, outcomeCommitted, holdIDs); err != nil {
		a.logger.ErrorContext(ctx, "order paid but stock not committed in the catalogue",
			slog.String("order_id", orderID.String()), slog.Any("error", err),
			slog.String("alert", "stock_commit_failed"))
	}
}
