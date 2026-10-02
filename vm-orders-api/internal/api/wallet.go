package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vayal-mikrogreenz/vm-go-common/httpx"
	"github.com/vayal-mikrogreenz/vm-go-common/money"

	"github.com/vayal-mikrogreenz/vm-orders-api/internal/razorpay"
	"github.com/vayal-mikrogreenz/vm-orders-api/internal/store"
)

// The prepaid wallet — CLAUDE.md §6.7.
//
// Closed-loop: money comes in only as a Razorpay-captured top-up, goes out
// only as a scheduled delivery or as an admin refund to the payment it came
// from. There is no customer withdrawal and no transfer between customers.
//
// Every movement of balance goes through moveWallet, under the wallet's row
// lock, and writes a ledger row stating the balance it left — so the history
// always reconciles, and the database CHECK refuses an overdraft even if an
// application check were wrong.

// WalletLimits bounds top-ups and balances. Env-driven (rule 3).
type WalletLimits struct {
	MinTopupPaise   money.Paise
	MaxTopupPaise   money.Paise
	MaxBalancePaise money.Paise
}

// ScheduleConfig configures scheduled deliveries. Env-driven (rule 3).
type ScheduleConfig struct {
	ChargeLead           time.Duration
	LowBalancePauseAfter int
}

const (
	walletKindTopup          = "topup"
	walletKindOrderDebit     = "order_debit"
	walletKindRefund         = "refund"
	walletKindRefundReversal = "refund_reversal"
)

// errInsufficientBalance means a debit would take the wallet below zero.
var errInsufficientBalance = errors.New("wallet balance is too low")

// walletRefs names what a ledger row is about. Exactly one is set, matching
// its kind — the database CHECK enforces the same.
type walletRefs struct {
	topupID  *uuid.UUID
	orderID  *uuid.UUID
	refundID *uuid.UUID
}

// moveWallet changes a customer's balance by delta (positive credits, negative
// debits) and records it. The caller owns the transaction.
func moveWallet(
	ctx context.Context, q *store.Queries, customerID uuid.UUID,
	delta money.Paise, kind string, refs walletRefs,
) (store.WalletTransaction, error) {
	if _, err := q.EnsureWallet(ctx, customerID); err != nil {
		return store.WalletTransaction{}, err
	}
	wallet, err := q.LockWallet(ctx, customerID)
	if err != nil {
		return store.WalletTransaction{}, err
	}

	next := money.Paise(wallet.BalancePaise).Add(delta)
	if next < 0 {
		return store.WalletTransaction{}, errInsufficientBalance
	}
	if _, err := q.SetWalletBalance(ctx, store.SetWalletBalanceParams{
		CustomerID: customerID, BalancePaise: next.Int64(),
	}); err != nil {
		return store.WalletTransaction{}, err
	}
	return q.InsertWalletTransaction(ctx, store.InsertWalletTransactionParams{
		CustomerID:        customerID,
		Kind:              kind,
		AmountPaise:       delta.Int64(),
		BalanceAfterPaise: next.Int64(),
		TopupID:           refs.topupID,
		OrderID:           refs.orderID,
		RefundID:          refs.refundID,
	})
}

// ---------------------------------------------------------------------------
// Customer: GET /wallet
// ---------------------------------------------------------------------------

type walletTransactionResponse struct {
	ID           string  `json:"id"`
	Kind         string  `json:"kind"`
	Label        string  `json:"label"`
	AmountPaise  int64   `json:"amount_paise"`
	Amount       string  `json:"amount_display"`
	BalanceAfter string  `json:"balance_after_display"`
	OrderID      *string `json:"order_id,omitempty"`
	OrderNumber  *string `json:"order_number,omitempty"`
	CreatedAt    string  `json:"created_at"`
}

type walletLimitsResponse struct {
	MinTopupPaise   int64  `json:"min_topup_paise"`
	MaxTopupPaise   int64  `json:"max_topup_paise"`
	MaxBalancePaise int64  `json:"max_balance_paise"`
	MinTopup        string `json:"min_topup_display"`
	MaxTopup        string `json:"max_topup_display"`
	MaxBalance      string `json:"max_balance_display"`
}

func (a *API) walletLimitsResponse() walletLimitsResponse {
	return walletLimitsResponse{
		MinTopupPaise:   a.wallet.MinTopupPaise.Int64(),
		MaxTopupPaise:   a.wallet.MaxTopupPaise.Int64(),
		MaxBalancePaise: a.wallet.MaxBalancePaise.Int64(),
		MinTopup:        money.FormatRupees(a.wallet.MinTopupPaise),
		MaxTopup:        money.FormatRupees(a.wallet.MaxTopupPaise),
		MaxBalance:      money.FormatRupees(a.wallet.MaxBalancePaise),
	}
}

func walletTransactionLabel(kind string, orderNumber *string) string {
	switch kind {
	case walletKindTopup:
		return "Money added"
	case walletKindOrderDebit:
		if orderNumber != nil {
			return "Scheduled delivery " + *orderNumber
		}
		return "Scheduled delivery"
	case walletKindRefund:
		return "Refunded to your payment method"
	case walletKindRefundReversal:
		return "Refund reversed — returned to wallet"
	}
	return kind
}

func toWalletTransactions(rows []store.ListWalletTransactionsRow) []walletTransactionResponse {
	out := make([]walletTransactionResponse, 0, len(rows))
	for _, row := range rows {
		var orderID *string
		if row.OrderID != nil {
			id := row.OrderID.String()
			orderID = &id
		}
		out = append(out, walletTransactionResponse{
			ID:           row.ID.String(),
			Kind:         row.Kind,
			Label:        walletTransactionLabel(row.Kind, row.OrderNumber),
			AmountPaise:  row.AmountPaise,
			Amount:       money.FormatRupees(money.Paise(row.AmountPaise)),
			BalanceAfter: money.FormatRupees(money.Paise(row.BalanceAfterPaise)),
			OrderID:      orderID,
			OrderNumber:  row.OrderNumber,
			CreatedAt:    row.CreatedAt.Format(time.RFC3339),
		})
	}
	return out
}

// GetWallet returns the balance and the ledger, newest first.
func (a *API) GetWallet(w http.ResponseWriter, r *http.Request) {
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

	// Created on first read, so a customer who has never topped up sees a
	// zero balance rather than an error.
	wallet, err := a.queries.EnsureWallet(ctx, customerID)
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}
	rows, err := a.queries.ListWalletTransactions(ctx, store.ListWalletTransactionsParams{
		CustomerID: customerID, Limit: limit, Offset: offset,
	})
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}
	total, err := a.queries.CountWalletTransactions(ctx, customerID)
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	a.respond(ctx, w, http.StatusOK, map[string]any{
		"balance_paise":   wallet.BalancePaise,
		"balance_display": money.FormatRupees(money.Paise(wallet.BalancePaise)),
		"transactions":    toWalletTransactions(rows),
		"total":           total,
		"limit":           limit,
		"offset":          offset,
		"limits":          a.walletLimitsResponse(),
	})
}

// ---------------------------------------------------------------------------
// Customer: POST /wallet/topups, GET /wallet/topups/{id},
//           POST /wallet/topups/{id}/verify
// ---------------------------------------------------------------------------

type createTopupRequest struct {
	AmountPaise int64 `json:"amount_paise"`
}

type topupResponse struct {
	ID              string  `json:"id"`
	AmountPaise     int64   `json:"amount_paise"`
	Amount          string  `json:"amount_display"`
	Status          string  `json:"status"`
	RazorpayOrderID *string `json:"razorpay_order_id,omitempty"`
	RazorpayKeyID   string  `json:"razorpay_key_id,omitempty"`
}

func (a *API) toTopupResponse(topup store.WalletTopup) topupResponse {
	out := topupResponse{
		ID:          topup.ID.String(),
		AmountPaise: topup.AmountPaise,
		Amount:      money.FormatRupees(money.Paise(topup.AmountPaise)),
		Status:      topup.Status,
	}
	// Only while it can still be paid, as for an order.
	if topup.Status == "created" {
		out.RazorpayOrderID = topup.RazorpayOrderID
		out.RazorpayKeyID = a.razorpayKeyID
	}
	return out
}

// CreateTopup opens a Razorpay order for adding money. Nothing is credited
// here: the capture webhook is the only path to a balance (CLAUDE.md §6.4).
//
// Idempotent via Idempotency-Key (rule 6): a retried "Add money" returns the
// first top-up rather than opening a second payment.
func (a *API) CreateTopup(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	customerID, err := customerFromRequest(r)
	if err != nil {
		a.fail(ctx, w, err)
		return
	}

	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" || len(key) > 200 {
		a.fail(ctx, w, httpx.BadRequest("An Idempotency-Key header is required to add money."))
		return
	}
	if existing, err := a.queries.GetWalletTopupByKey(ctx, store.GetWalletTopupByKeyParams{
		CustomerID: customerID, IdempotencyKey: key,
	}); err == nil {
		a.respond(ctx, w, http.StatusOK, a.toTopupResponse(existing))
		return
	} else if !errors.Is(err, pgx.ErrNoRows) {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	var req createTopupRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		a.fail(ctx, w, err)
		return
	}
	amount := money.Paise(req.AmountPaise)
	if amount < a.wallet.MinTopupPaise || amount > a.wallet.MaxTopupPaise {
		a.fail(ctx, w, httpx.Validation(
			fmt.Sprintf("Add between %s and %s at a time.",
				money.FormatRupees(a.wallet.MinTopupPaise),
				money.FormatRupees(a.wallet.MaxTopupPaise)),
			map[string]any{"amount_paise": "out of range"}))
		return
	}

	wallet, err := a.queries.EnsureWallet(ctx, customerID)
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}
	// Checked against the balance now. Two top-ups opened together can still
	// pass this and both be paid; the cap is a sensible ceiling, not a
	// regulatory line, so a small overshoot is accepted rather than refusing
	// money the customer has already sent.
	if money.Paise(wallet.BalancePaise).Add(amount) > a.wallet.MaxBalancePaise {
		a.fail(ctx, w, httpx.Validation(
			fmt.Sprintf("Your wallet can hold up to %s.",
				money.FormatRupees(a.wallet.MaxBalancePaise)),
			map[string]any{"amount_paise": "exceeds the wallet limit"}))
		return
	}

	topup, err := a.queries.CreateWalletTopup(ctx, store.CreateWalletTopupParams{
		CustomerID: customerID, AmountPaise: amount.Int64(), IdempotencyKey: key,
	})
	if err != nil {
		if isUniqueViolation(err) {
			if existing, lookupErr := a.queries.GetWalletTopupByKey(ctx,
				store.GetWalletTopupByKeyParams{CustomerID: customerID, IdempotencyKey: key},
			); lookupErr == nil {
				a.respond(ctx, w, http.StatusOK, a.toTopupResponse(existing))
				return
			}
		}
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	// "WT-" and the id's first block: the dashboard row traces back to ours,
	// and Razorpay caps a receipt at 40 characters.
	receipt := "WT-" + strings.ToUpper(topup.ID.String()[:8])
	rzpOrder, err := a.razorpay.CreateOrder(ctx, amount, receipt)
	if err != nil {
		a.logger.ErrorContext(ctx, "could not create the Razorpay order for a top-up",
			slog.String("topup_id", topup.ID.String()), slog.Any("error", err))
		a.fail(ctx, w, httpx.Unavailable("We could not start the payment. Please try again."))
		return
	}
	topup, err = a.queries.SetWalletTopupRazorpayOrder(ctx, store.SetWalletTopupRazorpayOrderParams{
		ID: topup.ID, RazorpayOrderID: &rzpOrder.ID,
	})
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	a.respond(ctx, w, http.StatusCreated, a.toTopupResponse(topup))
}

// GetTopup lets the client poll for the webhook's credit after Checkout.
func (a *API) GetTopup(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	customerID, err := customerFromRequest(r)
	if err != nil {
		a.fail(ctx, w, err)
		return
	}
	topupID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		a.fail(ctx, w, httpx.NotFound("Top-up not found."))
		return
	}
	topup, err := a.queries.GetWalletTopupForCustomer(ctx, store.GetWalletTopupForCustomerParams{
		ID: topupID, CustomerID: customerID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			a.fail(ctx, w, httpx.NotFound("Top-up not found."))
			return
		}
		a.fail(ctx, w, httpx.Internal(err))
		return
	}
	a.respond(ctx, w, http.StatusOK, a.toTopupResponse(topup))
}

// VerifyTopup checks the Checkout callback signature. A UX hint only, exactly
// like VerifyPayment: the webhook alone credits the wallet.
func (a *API) VerifyTopup(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	customerID, err := customerFromRequest(r)
	if err != nil {
		a.fail(ctx, w, err)
		return
	}
	topupID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		a.fail(ctx, w, httpx.NotFound("Top-up not found."))
		return
	}
	var req verifyPaymentRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		a.fail(ctx, w, err)
		return
	}

	topup, err := a.queries.GetWalletTopupForCustomer(ctx, store.GetWalletTopupForCustomerParams{
		ID: topupID, CustomerID: customerID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			a.fail(ctx, w, httpx.NotFound("Top-up not found."))
			return
		}
		a.fail(ctx, w, httpx.Internal(err))
		return
	}
	if topup.RazorpayOrderID == nil || *topup.RazorpayOrderID != req.RazorpayOrderID {
		a.fail(ctx, w, httpx.BadRequest("That payment does not belong to this top-up."))
		return
	}
	if err := razorpay.VerifyCallbackSignature(
		req.RazorpayOrderID, req.RazorpayPaymentID, req.RazorpaySignature, a.razorpayKeySecret,
	); err != nil {
		a.fail(ctx, w, httpx.Unauthorized("We could not verify that payment."))
		return
	}

	a.respond(ctx, w, http.StatusOK, map[string]any{
		"signature_valid":       true,
		"status":                topup.Status,
		"awaiting_confirmation": topup.Status == "created",
		"message": "Payment received. Your balance updates as soon as the bank " +
			"confirms it — usually within a minute.",
	})
}

// handleTopupCaptured credits a wallet from a captured payment. Called by the
// webhook when the Razorpay order is a top-up rather than an order.
func (a *API) handleTopupCaptured(
	ctx context.Context, topup store.WalletTopup, paymentID string, amount int64, currency string,
) error {
	// Same rule as an order (CLAUDE.md §6.4 step 7): never credit an amount we
	// did not ask for.
	if amount != topup.AmountPaise {
		a.logger.ErrorContext(ctx, "TOP-UP AMOUNT MISMATCH — wallet not credited",
			slog.String("topup_id", topup.ID.String()),
			slog.String("payment_id", paymentID),
			slog.Int64("expected_paise", topup.AmountPaise),
			slog.Int64("received_paise", amount),
			slog.String("alert", "payment_amount_mismatch"))
		a.enqueueAlert(ctx, topup.ID, "wallet.topup_amount_mismatch", map[string]any{
			"topup_id": topup.ID.String(), "payment_id": paymentID,
			"expected_paise": topup.AmountPaise, "received_paise": amount,
		})
		return permanent(errAmountMismatch)
	}
	if currency != "" && currency != "INR" {
		return permanent(errors.New("unexpected currency " + currency))
	}

	tx, err := a.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit
	q := a.queries.WithTx(tx)

	// Filtered on status='created': a replay finds no row and credits nothing.
	captured, err := q.CaptureWalletTopup(ctx, store.CaptureWalletTopupParams{
		ID: topup.ID, RazorpayPaymentID: &paymentID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			a.logger.InfoContext(ctx, "top-up capture replayed; already credited",
				slog.String("topup_id", topup.ID.String()))
			return nil
		}
		return err
	}

	topupID := captured.ID
	entry, err := moveWallet(ctx, q, captured.CustomerID, money.Paise(captured.AmountPaise),
		walletKindTopup, walletRefs{topupID: &topupID})
	if err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}

	a.logger.InfoContext(ctx, "wallet topped up",
		slog.String("topup_id", captured.ID.String()),
		slog.Int64("amount_paise", captured.AmountPaise),
		slog.Int64("balance_paise", entry.BalanceAfterPaise))
	return nil
}

// ---------------------------------------------------------------------------
// Admin: GET /admin/wallets, GET /admin/wallets/{customer_id},
//        POST /admin/wallets/{customer_id}/refund
// ---------------------------------------------------------------------------

// AdminListWallets lists wallets holding money, largest first, with the
// customer's name and email for the refund desk.
func (a *API) AdminListWallets(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	limit, offset := int32(50), int32(0)
	if v, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && v > 0 && v <= 200 {
		limit = int32(v)
	}
	if v, err := strconv.Atoi(r.URL.Query().Get("offset")); err == nil && v >= 0 {
		offset = int32(v)
	}

	rows, err := a.queries.AdminListWallets(ctx, store.AdminListWalletsParams{
		Limit: limit, Offset: offset,
		IncludeEmpty: r.URL.Query().Get("include_empty") == "true",
	})
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}
	totals, err := a.queries.AdminWalletTotals(ctx)
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	ids := make([]uuid.UUID, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.CustomerID)
	}
	// A failed lookup leaves names blank rather than failing the page: the
	// balances are what the page is for.
	contacts, err := a.customerContacts(ctx, ids)
	if err != nil {
		a.logger.WarnContext(ctx, "could not resolve wallet owners", slog.Any("error", err))
		contacts = map[uuid.UUID]CustomerContact{}
	}

	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		contact := contacts[row.CustomerID]
		out = append(out, map[string]any{
			"customer_id":      row.CustomerID.String(),
			"customer_name":    contact.Name,
			"customer_email":   contact.Email,
			"balance_paise":    row.BalancePaise,
			"balance_display":  money.FormatRupees(money.Paise(row.BalancePaise)),
			"active_schedules": row.ActiveSchedules,
			"updated_at":       row.UpdatedAt.Format(time.RFC3339),
		})
	}

	a.respond(ctx, w, http.StatusOK, map[string]any{
		"wallets":               out,
		"funded_wallets":        totals.FundedWallets,
		"total_balance_paise":   totals.TotalBalancePaise,
		"total_balance_display": money.FormatRupees(money.Paise(totals.TotalBalancePaise)),
		"limit":                 limit,
		"offset":                offset,
	})
}

// AdminGetWallet shows one customer's balance, ledger and refunds.
func (a *API) AdminGetWallet(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	customerID, err := uuid.Parse(chi.URLParam(r, "customer_id"))
	if err != nil {
		a.fail(ctx, w, httpx.NotFound("Wallet not found."))
		return
	}
	wallet, err := a.queries.GetWallet(ctx, customerID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			a.fail(ctx, w, httpx.NotFound("This customer has no wallet."))
			return
		}
		a.fail(ctx, w, httpx.Internal(err))
		return
	}
	rows, err := a.queries.ListWalletTransactions(ctx, store.ListWalletTransactionsParams{
		CustomerID: customerID, Limit: 100, Offset: 0,
	})
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}
	refunds, err := a.queries.ListWalletRefundsForCustomer(ctx, customerID)
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}
	contacts, _ := a.customerContacts(ctx, []uuid.UUID{customerID})
	contact := contacts[customerID]

	refundOut := make([]map[string]any, 0, len(refunds))
	for _, refund := range refunds {
		refundOut = append(refundOut, map[string]any{
			"id":                 refund.ID.String(),
			"amount_display":     money.FormatRupees(money.Paise(refund.AmountPaise)),
			"amount_paise":       refund.AmountPaise,
			"status":             refund.Status,
			"razorpay_refund_id": refund.RazorpayRefundID,
			"notes":              refund.Notes,
			"error":              refund.Error,
			"created_at":         refund.CreatedAt.Format(time.RFC3339),
		})
	}

	a.respond(ctx, w, http.StatusOK, map[string]any{
		"customer_id":     customerID.String(),
		"customer_name":   contact.Name,
		"customer_email":  contact.Email,
		"balance_paise":   wallet.BalancePaise,
		"balance_display": money.FormatRupees(money.Paise(wallet.BalancePaise)),
		"transactions":    toWalletTransactions(rows),
		"refunds":         refundOut,
	})
}

type walletRefundRequest struct {
	AmountPaise int64  `json:"amount_paise"`
	Notes       string `json:"notes"`
}

// refundSlice is one Razorpay refund call: part of the total, against one
// top-up's payment.
type refundSlice struct {
	refund    store.WalletRefund
	paymentID string
}

// AdminRefundWallet sends balance back to the payments it came from.
//
// Two phases, because the Razorpay call cannot sit inside our transaction:
//
//  1. In one transaction: take the amount out of the wallet, split it across
//     top-ups newest first, write a pending refund row per slice, and audit
//     it. The balance is gone before any call is made, so the customer cannot
//     spend money that is on its way back to their card.
//  2. Per slice, call Razorpay. A slice it refuses is marked failed and its
//     amount returned to the wallet (a refund_reversal), so a failed refund
//     never loses the customer's money.
func (a *API) AdminRefundWallet(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	adminID, err := adminFromRequest(r)
	if err != nil {
		a.fail(ctx, w, err)
		return
	}
	customerID, err := uuid.Parse(chi.URLParam(r, "customer_id"))
	if err != nil {
		a.fail(ctx, w, httpx.NotFound("Wallet not found."))
		return
	}
	var req walletRefundRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		a.fail(ctx, w, err)
		return
	}
	amount := money.Paise(req.AmountPaise)
	if amount <= 0 {
		a.fail(ctx, w, httpx.Validation("Enter an amount to refund.",
			map[string]any{"amount_paise": "must be positive"}))
		return
	}
	notes := strings.TrimSpace(req.Notes)
	if notes == "" {
		a.fail(ctx, w, httpx.Validation("Say why the balance is being refunded.",
			map[string]any{"notes": "required for the audit log"}))
		return
	}
	if len(notes) > 500 {
		notes = notes[:500]
	}

	slices, balanceAfter, err := a.reserveWalletRefund(ctx, adminID, customerID, amount, notes)
	if err != nil {
		switch {
		case errors.Is(err, errInsufficientBalance):
			a.fail(ctx, w, httpx.Conflict("INSUFFICIENT_BALANCE",
				"That is more than the wallet holds."))
		case errors.Is(err, pgx.ErrNoRows):
			a.fail(ctx, w, httpx.NotFound("This customer has no wallet."))
		default:
			a.fail(ctx, w, httpx.Internal(err))
		}
		return
	}

	results := make([]map[string]any, 0, len(slices))
	var failed money.Paise
	for _, slice := range slices {
		refund := slice.refund
		rzp, callErr := a.razorpay.RefundPayment(ctx, slice.paymentID,
			money.Paise(refund.AmountPaise), "WR-"+strings.ToUpper(refund.ID.String()[:8]))
		if callErr == nil {
			if _, err := a.queries.MarkWalletRefundIssued(ctx, store.MarkWalletRefundIssuedParams{
				ID: refund.ID, RazorpayRefundID: &rzp.ID,
			}); err != nil {
				// Razorpay HAS refunded; only our record lags. Loud, not fatal.
				a.logger.ErrorContext(ctx, "refund issued but not recorded",
					slog.String("refund_id", refund.ID.String()),
					slog.String("razorpay_refund_id", rzp.ID), slog.Any("error", err),
					slog.String("alert", "wallet_refund_unrecorded"))
			}
			results = append(results, map[string]any{
				"id": refund.ID.String(), "status": "issued",
				"amount_display": money.FormatRupees(money.Paise(refund.AmountPaise)),
			})
			continue
		}

		a.logger.ErrorContext(ctx, "razorpay refused a wallet refund; returning it to the wallet",
			slog.String("refund_id", refund.ID.String()), slog.Any("error", callErr))
		if err := a.reverseWalletRefund(ctx, refund, callErr.Error()); err != nil {
			a.logger.ErrorContext(ctx, "could not reverse a failed wallet refund",
				slog.String("refund_id", refund.ID.String()), slog.Any("error", err),
				slog.String("alert", "wallet_refund_reversal_failed"))
		}
		failed = failed.Add(money.Paise(refund.AmountPaise))
		results = append(results, map[string]any{
			"id": refund.ID.String(), "status": "failed",
			"amount_display": money.FormatRupees(money.Paise(refund.AmountPaise)),
			"error":          callErr.Error(),
		})
	}

	status := http.StatusOK
	if failed > 0 {
		status = http.StatusMultiStatus
	}
	a.respond(ctx, w, status, map[string]any{
		"refunds":               results,
		"failed_paise":          failed.Int64(),
		"balance_after_display": money.FormatRupees(balanceAfter.Add(failed)),
	})
}

func (a *API) reserveWalletRefund(
	ctx context.Context, adminID, customerID uuid.UUID, amount money.Paise, notes string,
) ([]refundSlice, money.Paise, error) {
	tx, err := a.pool.Begin(ctx)
	if err != nil {
		return nil, 0, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit
	q := a.queries.WithTx(tx)

	wallet, err := q.LockWallet(ctx, customerID)
	if err != nil {
		return nil, 0, err
	}
	if money.Paise(wallet.BalancePaise) < amount {
		return nil, 0, errInsufficientBalance
	}

	topups, err := q.LockRefundableTopups(ctx, customerID)
	if err != nil {
		return nil, 0, err
	}

	slices := []refundSlice{}
	left := amount
	var balance money.Paise
	for _, topup := range topups {
		if left == 0 {
			break
		}
		if topup.RazorpayPaymentID == nil {
			continue
		}
		take := money.Paise(topup.AmountPaise - topup.RefundedPaise)
		if take > left {
			take = left
		}
		refund, err := q.CreateWalletRefund(ctx, store.CreateWalletRefundParams{
			CustomerID: customerID, TopupID: topup.ID, AmountPaise: take.Int64(),
			AdminUserID: adminID, Notes: &notes,
		})
		if err != nil {
			return nil, 0, err
		}
		if _, err := q.AddTopupRefunded(ctx, store.AddTopupRefundedParams{
			ID: topup.ID, RefundedPaise: take.Int64(),
		}); err != nil {
			return nil, 0, err
		}
		refundID := refund.ID
		entry, err := moveWallet(ctx, q, customerID, -take, walletKindRefund,
			walletRefs{refundID: &refundID})
		if err != nil {
			return nil, 0, err
		}
		balance = money.Paise(entry.BalanceAfterPaise)
		slices = append(slices, refundSlice{refund: refund, paymentID: *topup.RazorpayPaymentID})
		left -= take
	}
	// Every rupee in a wallet arrived as a top-up, so the top-ups always cover
	// the balance. Not covering it means the ledger is wrong — refuse.
	if left > 0 {
		return nil, 0, fmt.Errorf("top-ups cover %s less than the refund",
			money.FormatRupees(left))
	}

	before, _ := json.Marshal(map[string]any{"balance_paise": wallet.BalancePaise})
	after, _ := json.Marshal(map[string]any{
		"balance_paise": balance.Int64(), "refund_paise": amount.Int64(),
		"slices": len(slices), "notes": notes,
	})
	if _, err := q.InsertAuditLog(ctx, store.InsertAuditLogParams{
		AdminUserID: adminID, Action: "wallet.refund", EntityType: "wallet",
		EntityID: &customerID, Before: before, After: after,
	}); err != nil {
		return nil, 0, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, 0, err
	}
	return slices, balance, nil
}

// reverseWalletRefund marks a refund Razorpay refused as failed and puts its
// amount back in the wallet, in one transaction.
func (a *API) reverseWalletRefund(ctx context.Context, refund store.WalletRefund, reason string) error {
	tx, err := a.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit
	q := a.queries.WithTx(tx)

	if len(reason) > 500 {
		reason = reason[:500]
	}
	if _, err := q.MarkWalletRefundFailed(ctx, store.MarkWalletRefundFailedParams{
		ID: refund.ID, Error: &reason,
	}); err != nil {
		return err
	}
	if _, err := q.AddTopupRefunded(ctx, store.AddTopupRefundedParams{
		ID: refund.TopupID, RefundedPaise: -refund.AmountPaise,
	}); err != nil {
		return err
	}
	refundID := refund.ID
	if _, err := moveWallet(ctx, q, refund.CustomerID, money.Paise(refund.AmountPaise),
		walletKindRefundReversal, walletRefs{refundID: &refundID}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
