package api

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vayal-mikrogreenz/vm-go-common/httpx"

	"github.com/vayal-mikrogreenz/vm-orders-api/internal/razorpay"
	"github.com/vayal-mikrogreenz/vm-orders-api/internal/store"
)

type verifyPaymentRequest struct {
	RazorpayOrderID   string `json:"razorpay_order_id"`
	RazorpayPaymentID string `json:"razorpay_payment_id"`
	RazorpaySignature string `json:"razorpay_signature"`
}

// VerifyPayment checks the signature Razorpay Checkout hands the browser —
// CLAUDE.md §6.4 step 3.
//
// A UX HINT ONLY. It deliberately does NOT mark the order paid, create
// payouts, or commit stock. The browser is attacker-controlled, so treating a
// call here as proof of payment would let anyone mark their own order paid by
// replaying a signature they legitimately obtained for a one-rupee order.
//
// The webhook (step 4) is the sole path to `paid`. All this endpoint does is
// tell the UI whether to show "confirming your payment" or "something went
// wrong", so the customer is not left staring at a spinner.
func (a *API) VerifyPayment(w http.ResponseWriter, r *http.Request) {
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

	var req verifyPaymentRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		a.fail(ctx, w, err)
		return
	}

	// Ownership in the WHERE clause: another customer's order returns no rows.
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

	// The signature must be for THIS order's provider id, not some other one
	// the caller happens to hold a valid signature for.
	if order.RazorpayOrderID == nil || *order.RazorpayOrderID != req.RazorpayOrderID {
		a.fail(ctx, w, httpx.BadRequest("That payment does not belong to this order."))
		return
	}

	if err := razorpay.VerifyCallbackSignature(
		req.RazorpayOrderID, req.RazorpayPaymentID, req.RazorpaySignature, a.razorpayKeySecret,
	); err != nil {
		a.logger.WarnContext(ctx, "browser callback signature rejected",
			slog.String("order_id", order.ID.String()))
		a.fail(ctx, w, httpx.Unauthorized("We could not verify that payment."))
		return
	}

	// Verified — but still only a hint. The order's real status is whatever
	// the webhook has (or has not yet) set.
	a.respond(ctx, w, http.StatusOK, map[string]any{
		"signature_valid": true,
		"order_status":    order.Status,
		"payment_status":  order.PaymentStatus,
		// Tells the UI to poll rather than declare success.
		"awaiting_confirmation": order.Status == statusPendingPayment,
		"message": "Payment received. We're confirming it with the bank — " +
			"your order will update shortly.",
	})
}
