package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vayal-mikrogreenz/vm-go-common/httpx"
	"github.com/vayal-mikrogreenz/vm-go-common/money"

	"github.com/vayal-mikrogreenz/vm-orders-api/internal/analyticsevents"
	"github.com/vayal-mikrogreenz/vm-orders-api/internal/razorpay"
	"github.com/vayal-mikrogreenz/vm-orders-api/internal/store"
)

// maxWebhookBody bounds what we will read before verifying anything.
const maxWebhookBody = 1 << 20 // 1 MiB

// Razorpay event types we act on (CLAUDE.md §6.4 step 4).
const (
	eventPaymentCaptured = "payment.captured"
	eventPaymentFailed   = "payment.failed"
	eventRefundProcessed = "refund.processed"
)

// razorpayEvent is the subset of the webhook payload we use.
type razorpayEvent struct {
	Event   string `json:"event"`
	Payload struct {
		Payment struct {
			Entity struct {
				ID       string `json:"id"`
				OrderID  string `json:"order_id"`
				Amount   int64  `json:"amount"`
				Currency string `json:"currency"`
				Status   string `json:"status"`
				Method   string `json:"method"`
			} `json:"entity"`
		} `json:"payment"`
		Refund struct {
			Entity struct {
				ID        string `json:"id"`
				PaymentID string `json:"payment_id"`
				Amount    int64  `json:"amount"`
			} `json:"entity"`
		} `json:"refund"`
	} `json:"payload"`
}

// RazorpayWebhook is the SOURCE OF TRUTH for payment state (CLAUDE.md §6.4).
//
// The browser callback is a hint; this is what actually moves an order to
// paid. The ordering below is deliberate and load-bearing:
//
//  1. read the RAW body — before any parsing, because the signature is over
//     bytes and re-serialising JSON changes them
//  2. verify the HMAC in constant time — before trusting a single field
//  3. dedupe by event id — before doing any work
//  4. validate the amount against OUR stored total — before marking paid
//
// Always returns 200 for an event we have already processed, so Razorpay
// stops retrying (step 5).
func (a *API) RazorpayWebhook(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// --- 1. raw body ---------------------------------------------------------
	rawBody, err := io.ReadAll(io.LimitReader(r.Body, maxWebhookBody+1))
	if err != nil {
		a.fail(ctx, w, httpx.BadRequest("Could not read the request body."))
		return
	}
	if len(rawBody) > maxWebhookBody {
		a.fail(ctx, w, httpx.BadRequest("Request body is too large."))
		return
	}

	// --- 2. signature --------------------------------------------------------
	signature := r.Header.Get("X-Razorpay-Signature")
	if err := razorpay.VerifyWebhookSignature(rawBody, signature, a.razorpayWebhookSecret); err != nil {
		// Logged as a warning, not an error: unsigned probes to a public
		// webhook URL are routine background noise. A sudden burst is worth
		// alerting on, which is why the reason is recorded.
		a.logger.WarnContext(ctx, "rejected webhook with an invalid signature",
			slog.String("remote_addr", r.RemoteAddr),
			slog.Int("body_bytes", len(rawBody)))
		// 401 rather than 400: this is an authentication failure.
		a.fail(ctx, w, httpx.Unauthorized("Invalid signature."))
		return
	}

	// Only now is the body worth parsing.
	var event razorpayEvent
	if err := json.Unmarshal(rawBody, &event); err != nil {
		a.fail(ctx, w, httpx.BadRequest("Body is not valid JSON."))
		return
	}

	// --- 3. dedupe -----------------------------------------------------------
	eventID := r.Header.Get("X-Razorpay-Event-Id")
	if eventID == "" {
		a.fail(ctx, w, httpx.BadRequest("X-Razorpay-Event-Id is required."))
		return
	}

	recorded, err := a.queries.RecordWebhookEvent(ctx, store.RecordWebhookEventParams{
		ProviderEventID: eventID,
		EventType:       event.Event,
		Payload:         rawBody,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Already closed: processed, or failed for good. Razorpay retries
			// until it gets a 200, so this must be a success.
			a.logger.InfoContext(ctx, "webhook replay ignored",
				slog.String("event_id", eventID), slog.String("event", event.Event))
			a.respond(ctx, w, http.StatusOK, map[string]any{
				"status": "already_processed", "event_id": eventID,
			})
			return
		}
		a.fail(ctx, w, httpx.Internal(err))
		return
	}
	// A redelivery of an event whose last attempt failed for a temporary
	// reason arrives here too, and is processed again. That is safe because
	// every handler below is idempotent: the order moves to paid only from
	// pending_payment, payouts are unique per (order, supplier), a top-up is
	// credited only from 'created'.

	// --- 4. act --------------------------------------------------------------
	var handlerErr error
	switch event.Event {
	case eventPaymentCaptured:
		handlerErr = a.handlePaymentCaptured(ctx, event, rawBody)
	case eventPaymentFailed:
		handlerErr = a.handlePaymentFailed(ctx, event, rawBody)
	case eventRefundProcessed:
		handlerErr = a.handleRefundProcessed(ctx, event)
	default:
		// An event type we do not act on is still a delivered event. Recording
		// and acknowledging it stops Razorpay retrying forever.
		a.logger.InfoContext(ctx, "webhook event ignored",
			slog.String("event", event.Event))
	}

	// --- 5. close the event, or leave it open for a retry --------------------
	switch webhookOutcomeFor(handlerErr, int(recorded.Attempts), a.webhookMaxAttempts) {
	case webhookDone:
		a.closeWebhookEvent(ctx, eventID, nil)
		a.respond(ctx, w, http.StatusOK, map[string]any{"status": "processed"})

	case webhookFailedForGood:
		// Retrying cannot fix it: an amount we did not ask for, an order we do
		// not have — or a temporary-looking error that has now failed every
		// attempt. Close it and answer 200, so Razorpay stops redelivering:
		// it disables the WHOLE webhook after 24 hours of failures, which
		// would stop every payment notification, not just this one.
		errText := handlerErr.Error()
		if !isPermanent(handlerErr) {
			errText = fmt.Sprintf("gave up after %d attempts: %s", recorded.Attempts, errText)
			a.enqueueAlert(ctx, uuid.Nil, "webhook.gave_up", map[string]any{
				"event_id": eventID, "event": event.Event,
				"attempts": recorded.Attempts, "error": handlerErr.Error(),
			})
		}
		a.logger.ErrorContext(ctx, "webhook failed and needs a human",
			slog.String("event_id", eventID), slog.String("event", event.Event),
			slog.Int("attempts", int(recorded.Attempts)),
			slog.String("alert", "webhook_failed"),
			slog.Any("error", handlerErr))
		a.closeWebhookEvent(ctx, eventID, &errText)
		a.respond(ctx, w, http.StatusOK, map[string]any{
			"status": "failed", "event_id": eventID,
		})

	case webhookRetry:
		// A temporary failure: a database blip, a restart mid-request. Left
		// open, so Razorpay's next redelivery runs it again; a 5xx is what
		// asks for that redelivery. Meanwhile the sweeper keeps a captured
		// order's stock (CLAUDE.md §6.3 step 4).
		errText := handlerErr.Error()
		a.logger.WarnContext(ctx, "webhook processing failed; Razorpay will retry",
			slog.String("event_id", eventID), slog.String("event", event.Event),
			slog.Int("attempts", int(recorded.Attempts)),
			slog.Int("max_attempts", a.webhookMaxAttempts),
			slog.Any("error", handlerErr))
		if _, err := a.queries.MarkWebhookRetryable(ctx, store.MarkWebhookRetryableParams{
			ProviderEventID: eventID, Error: &errText,
		}); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			a.logger.ErrorContext(ctx, "could not record webhook failure", slog.Any("error", err))
		}
		a.fail(ctx, w, httpx.Internal(handlerErr))
	}
}

func (a *API) closeWebhookEvent(ctx context.Context, eventID string, errText *string) {
	if _, err := a.queries.MarkWebhookProcessed(ctx, store.MarkWebhookProcessedParams{
		ProviderEventID: eventID, Error: errText,
	}); err != nil {
		a.logger.ErrorContext(ctx, "could not close webhook event",
			slog.String("event_id", eventID), slog.Any("error", err))
	}
}

// DefaultWebhookMaxAttempts is how many deliveries of one event may fail for
// a temporary reason before it is closed and left to a human.
const DefaultWebhookMaxAttempts = 10

type webhookOutcome int

const (
	webhookDone webhookOutcome = iota
	webhookRetry
	webhookFailedForGood
)

// webhookOutcomeFor decides what one processing attempt means for the event.
//
// Success closes it. A permanent failure closes it at once, because no retry
// can fix it. Anything else is assumed temporary and left open for Razorpay's
// redelivery — until it has failed maxAttempts times, when an error that
// looked temporary has shown it is not, and it is closed for a human.
func webhookOutcomeFor(handlerErr error, attempts, maxAttempts int) webhookOutcome {
	switch {
	case handlerErr == nil:
		return webhookDone
	case isPermanent(handlerErr), attempts >= maxAttempts:
		return webhookFailedForGood
	default:
		return webhookRetry
	}
}

// permanentError marks a webhook failure that no retry can fix.
type permanentError struct{ error }

func (p permanentError) Unwrap() error { return p.error }

// permanent marks err as one no retry can fix.
func permanent(err error) error { return permanentError{err} }

func isPermanent(err error) bool {
	var p permanentError
	return errors.As(err, &p)
}

// errAmountMismatch means the provider reported an amount that is not what we
// asked for. Never resolved automatically.
var errAmountMismatch = errors.New("payment amount does not match the order total")

// handlePaymentCaptured performs CLAUDE.md §6.4 step 6 in ONE transaction:
// order -> paid, reservations -> committed, payouts created, email enqueued.
func (a *API) handlePaymentCaptured(
	ctx context.Context, event razorpayEvent, rawBody []byte,
) error {
	entity := event.Payload.Payment.Entity

	// A Razorpay order is either one of ours or a wallet top-up (CLAUDE.md
	// §6.7). Top-ups are looked up first because they are the cheaper miss.
	if topup, err := a.queries.GetWalletTopupByRazorpayOrderID(ctx, &entity.OrderID); err == nil {
		return a.handleTopupCaptured(ctx, topup, entity.ID, entity.Amount, entity.Currency)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}

	order, err := a.queries.GetOrderByRazorpayOrderID(ctx, &entity.OrderID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Nothing to apply it to, now or on any retry.
			return permanent(errors.New("no order matches razorpay order " + entity.OrderID))
		}
		return err
	}

	// --- amount validation, BEFORE marking anything paid ---------------------
	//
	// CLAUDE.md §6.4 step 7. Razorpay amounts are in paise, same as ours, so
	// this is an exact integer comparison. A mismatch means either a tampered
	// request or a serious bug, and either way the order must not be marked
	// paid on an amount we did not ask for.
	if entity.Amount != order.TotalPaise {
		a.logger.ErrorContext(ctx, "PAYMENT AMOUNT MISMATCH — order not marked paid",
			slog.String("order_id", order.ID.String()),
			slog.String("order_number", order.OrderNumber),
			slog.String("razorpay_payment_id", entity.ID),
			slog.Int64("expected_paise", order.TotalPaise),
			slog.Int64("received_paise", entity.Amount),
			slog.String("expected", money.FormatRupees(money.Paise(order.TotalPaise))),
			slog.String("received", money.FormatRupees(money.Paise(entity.Amount))),
			slog.String("alert", "payment_amount_mismatch"),
		)
		// Recorded in the outbox so the alert survives log rotation and can be
		// picked up by an operator workflow.
		a.enqueueAlert(ctx, order.ID, "payment.amount_mismatch", map[string]any{
			"order_number":   order.OrderNumber,
			"expected_paise": order.TotalPaise,
			"received_paise": entity.Amount,
			"payment_id":     entity.ID,
		})
		return permanent(errAmountMismatch)
	}
	if entity.Currency != "" && entity.Currency != "INR" {
		return permanent(errors.New("unexpected currency " + entity.Currency))
	}

	// Resolved BEFORE the transaction opens: this is an HTTP call to
	// vm-profile-api, and making it while holding a pooled connection would
	// let a slow lookup drain the pool with Postgres sitting idle on locks.
	commissions := a.supplierCommissions(ctx, supplierIDsOnOrder(ctx, a.queries, order.ID))

	tx, err := a.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit
	q := a.queries.WithTx(tx)

	// The shared transition — identical to the admin's manual "record payment"
	// path, by construction rather than by care. See applyOrderPaid.
	paid, holdIDs, payoutCount, ok, err := applyOrderPaid(
		ctx, q, order.ID, &entity.ID, entity.Method, analyticsevents.PaidViaWebhook, rawBody, a.Rates, commissions)
	if err != nil {
		return err
	}
	if !ok {
		// A replay of an already-paid order is routine and harmless. A capture
		// against a TERMINAL order is not: money has been taken for something
		// we will not fulfil, and the stock has already gone back on sale.
		// This used to be an Info log, which is how a real captured payment
		// sat unnoticed against an order the database called failed.
		if order.Status != "paid" {
			a.logger.ErrorContext(ctx, "captured payment for an unpayable order",
				slog.String("order_id", order.ID.String()),
				slog.String("order_number", order.OrderNumber),
				slog.String("status", order.Status),
				slog.String("payment_id", entity.ID),
				slog.Int64("amount_paise", entity.Amount),
				slog.String("amount", money.FormatRupees(money.Paise(entity.Amount))),
				slog.String("alert", "payment_captured_for_terminal_order"))

			a.enqueueAlert(ctx, order.ID, "payment.captured_for_terminal_order",
				map[string]any{
					"order_number": order.OrderNumber,
					"status":       order.Status,
					"payment_id":   entity.ID,
					"amount_paise": entity.Amount,
				})
			return nil
		}

		a.logger.InfoContext(ctx, "capture replayed for an already-paid order",
			slog.String("order_id", order.ID.String()))
		return nil
	}

	if err := tx.Commit(ctx); err != nil {
		return err
	}

	a.settleStockAfterCommit(ctx, order.ID, holdIDs)

	a.logger.InfoContext(ctx, "order paid",
		slog.String("order_id", paid.ID.String()),
		slog.String("order_number", paid.OrderNumber),
		slog.String("via", "razorpay webhook"),
		slog.Int("payouts", payoutCount))
	return nil
}

// handlePaymentFailed records a failed payment ATTEMPT — CLAUDE.md §6.4.
//
// It deliberately does NOT terminalize the order or release its stock. A
// Razorpay order accepts several attempts, and `payment.failed` reports that
// one of them did not go through, not that the customer has given up. Treating
// the first failure as final was a real bug: the order went to
// `payment_failed`, the customer retried from the same screen and paid
// successfully, and the resulting `payment.captured` then found the order out
// of `pending_payment` and silently did nothing — money captured, order dead,
// no payout and no confirmation email.
//
// So the order stays in `pending_payment` holding its stock, and the sweeper's
// reservation TTL (CLAUDE.md §6.3 step 4) is what ends it if the customer
// really has walked away. The cost is that grams stay reserved until the TTL
// after a genuine failure; the alternative loses paid orders.
func (a *API) handlePaymentFailed(
	ctx context.Context, event razorpayEvent, rawBody []byte,
) error {
	entity := event.Payload.Payment.Entity

	// A failed top-up attempt changes nothing: the top-up stays open for the
	// customer to retry from the same sheet, as an order does.
	if topup, err := a.queries.GetWalletTopupByRazorpayOrderID(ctx, &entity.OrderID); err == nil {
		a.logger.InfoContext(ctx, "top-up payment attempt failed; left payable",
			slog.String("topup_id", topup.ID.String()), slog.String("payment_id", entity.ID))
		return nil
	}

	order, err := a.queries.GetOrderByRazorpayOrderID(ctx, &entity.OrderID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil // nothing of ours to fail
		}
		return err
	}

	// Only the attempt is recorded. No order transition, so no transaction is
	// needed: this is a single statement against the payments row.
	if _, err := a.queries.MarkPaymentFailed(ctx, store.MarkPaymentFailedParams{
		OrderID: order.ID, ProviderPaymentID: &entity.ID, RawPayload: rawBody,
	}); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}

	a.logger.InfoContext(ctx, "payment attempt failed; order left payable",
		slog.String("order_id", order.ID.String()),
		slog.String("status", order.Status),
		slog.String("payment_id", entity.ID))
	return nil
}

// handleRefundProcessed records a refund against the order.
//
// Stock is deliberately NOT returned: the produce has already been picked and
// probably dispatched, so a refund is a money event, not a stock event.
func (a *API) handleRefundProcessed(ctx context.Context, event razorpayEvent) error {
	refund := event.Payload.Refund.Entity

	a.logger.InfoContext(ctx, "refund processed",
		slog.String("refund_id", refund.ID),
		slog.String("payment_id", refund.PaymentID),
		slog.Int64("amount_paise", refund.Amount))

	// Payouts for a refunded order need an operator decision — the supplier
	// may already have been paid — so this raises a task rather than guessing.
	a.enqueueAlert(ctx, uuid.Nil, "payment.refunded", map[string]any{
		"refund_id": refund.ID, "payment_id": refund.PaymentID,
		"amount_paise": refund.Amount,
	})
	return nil
}

// enqueueAlert writes an operator-facing event to the outbox.
func (a *API) enqueueAlert(
	ctx context.Context, orderID uuid.UUID, eventType string, detail map[string]any,
) {
	payload, err := json.Marshal(detail)
	if err != nil {
		return
	}
	if _, err := a.queries.EnqueueOutbox(ctx, store.EnqueueOutboxParams{
		AggregateType: "alert", AggregateID: orderID,
		EventType: eventType, Payload: payload,
	}); err != nil {
		a.logger.ErrorContext(ctx, "could not enqueue alert",
			slog.String("event_type", eventType), slog.Any("error", err))
	}
}
