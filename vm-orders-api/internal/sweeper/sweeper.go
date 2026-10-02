// Package sweeper releases stock held by orders that were never paid for.
//
// CLAUDE.md §6.3 step 4. Without it, an abandoned checkout would hold grams
// until the end of the day and quietly make produce look sold out.
package sweeper

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vayal-mikrogreenz/vm-orders-api/internal/analyticsevents"
	"github.com/vayal-mikrogreenz/vm-orders-api/internal/catalogclient"
	"github.com/vayal-mikrogreenz/vm-orders-api/internal/razorpay"
	"github.com/vayal-mikrogreenz/vm-orders-api/internal/store"
)

// advisoryLockKey is an arbitrary but FIXED application-chosen id for this
// job. Any replica that holds it is the one sweeping.
const advisoryLockKey int64 = 8_231_004_771

// batchSize bounds one pass, so a large backlog is worked through over
// several ticks rather than in one long transaction.
const batchSize = 200

// paymentCheckTimeout bounds one question to Razorpay. A sweep asks one per
// lapsed order, so a hung call must not stall the pass.
const paymentCheckTimeout = 5 * time.Second

// PaymentChecker is the slice of the Razorpay client the sweeper needs.
type PaymentChecker interface {
	OrderPayments(ctx context.Context, orderID string) ([]razorpay.Payment, error)
}

// Sweeper releases expired reservations on an interval.
type Sweeper struct {
	pool     *pgxpool.Pool
	queries  *store.Queries
	catalog  *catalogclient.Client
	payments PaymentChecker
	interval time.Duration
	logger   *slog.Logger
	// How long analytics event rows are kept; 0 keeps them forever.
	eventRetention time.Duration
}

// WithEventRetention makes each pass also prune order_events_outbox rows
// older than d (OUTBOX_RETENTION_DAYS). CDC has read them from the WAL by
// then; the table only holds a replay window (CLAUDE.md §5.4).
func (s *Sweeper) WithEventRetention(d time.Duration) *Sweeper {
	s.eventRetention = d
	return s
}

// New builds a sweeper.
func New(
	pool *pgxpool.Pool, catalog *catalogclient.Client, payments PaymentChecker,
	interval time.Duration, logger *slog.Logger,
) *Sweeper {
	if interval <= 0 {
		interval = time.Minute
	}
	return &Sweeper{
		pool: pool, queries: store.New(pool), catalog: catalog, payments: payments,
		interval: interval, logger: logger,
	}
}

// Run sweeps until the context is cancelled.
//
// Safe to run on every replica: the advisory lock means only one actually
// does the work on each tick, and the rest return immediately.
func (s *Sweeper) Run(ctx context.Context) {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	s.logger.InfoContext(ctx, "reservation sweeper started",
		slog.Duration("interval", s.interval))

	for {
		select {
		case <-ctx.Done():
			s.logger.InfoContext(ctx, "reservation sweeper stopped")
			return
		case <-ticker.C:
			if err := s.SweepOnce(ctx); err != nil {
				// Never fatal: the next tick tries again.
				s.logger.ErrorContext(ctx, "sweep failed", slog.Any("error", err))
			}
		}
	}
}

// SweepOnce runs a single pass. Exported so a test can drive it directly
// rather than waiting for a tick.
func (s *Sweeper) SweepOnce(ctx context.Context) error {
	// A session-scoped advisory lock, held only for this pass.
	//
	// pg_try_advisory_lock returns immediately rather than queueing: if
	// another replica is already sweeping there is nothing useful to wait
	// for, and queueing would just stack up ticks.
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()

	var acquired bool
	if err := conn.QueryRow(ctx,
		"SELECT pg_try_advisory_lock($1)", advisoryLockKey).Scan(&acquired); err != nil {
		return err
	}
	if !acquired {
		s.logger.DebugContext(ctx, "another replica is sweeping; skipping")
		return nil
	}
	defer func() {
		if _, err := conn.Exec(context.WithoutCancel(ctx),
			"SELECT pg_advisory_unlock($1)", advisoryLockKey); err != nil {
			s.logger.WarnContext(ctx, "could not release advisory lock",
				slog.Any("error", err))
		}
	}()

	if s.eventRetention > 0 {
		pruned, err := s.queries.PruneOrderEvents(ctx, time.Now().Add(-s.eventRetention))
		if err != nil {
			// Housekeeping only: never a reason to skip releasing stock.
			s.logger.WarnContext(ctx, "pruning order events failed", slog.Any("error", err))
		} else if pruned > 0 {
			s.logger.InfoContext(ctx, "pruned order events", slog.Int64("rows", pruned))
		}
	}

	// Ask Razorpay about every lapsed order that went to it, BEFORE the
	// transaction opens: these are network calls, and making them while
	// holding row locks would let a slow provider block checkout's settles.
	// Only the orders it clears may have their holds claimed below.
	lapsed, err := s.queries.ListLapsedRazorpayOrders(ctx, batchSize)
	if err != nil {
		return err
	}
	cleared := s.clearedOrders(ctx, lapsed)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit
	q := s.queries.WithTx(tx)

	// SKIP LOCKED so a second sweeper (or a concurrent settle) never blocks.
	expired, err := q.ClaimExpiredReservations(ctx, store.ClaimExpiredReservationsParams{
		Cleared: cleared, RowLimit: batchSize,
	})
	if err != nil {
		return err
	}
	if len(expired) == 0 {
		return tx.Commit(ctx)
	}

	holdIDs := make([]uuid.UUID, 0, len(expired))
	orderIDs := map[uuid.UUID]struct{}{}

	for _, reservation := range expired {
		if _, err := q.SettleReservation(ctx, store.SettleReservationParams{
			ID: reservation.ID, Status: "released",
		}); err != nil {
			return err
		}
		holdIDs = append(holdIDs, reservation.CatalogReservationID)
		orderIDs[reservation.OrderID] = struct{}{}
	}

	// An order whose stock has gone cannot be paid for.
	//
	// Guarded on pending_payment inside the SQL. Unguarded, a payment that
	// committed between this sweep claiming the reservation and updating the
	// order would be overwritten with 'expired' — money captured, stock
	// released, order dead. Zero rows updated is the normal outcome for an
	// order that beat us to it, not an error.
	for orderID := range orderIDs {
		expired, err := q.ExpireOrderIfUnpaid(ctx, orderID)
		if err != nil {
			return err
		}
		if expired == 0 {
			s.logger.InfoContext(ctx, "reservation lapsed but the order had moved on",
				slog.String("order_id", orderID.String()))
			continue
		}
		if err := analyticsevents.Emit(ctx, q, orderID, analyticsevents.OrderExpired,
			analyticsevents.ActorSystem, time.Now(), analyticsevents.Options{}); err != nil {
			return err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return err
	}

	// Catalog is told AFTER our own commit. If this call fails, nothing retries
	// it: the catalogue has no expiry pass of its own (ClaimExpiredHolds is
	// never called), and these reservations are already 'released' here, so
	// the next sweep will not claim them again. The grams stay reserved in
	// catalog.stock_holds for the rest of that day — stock under-reported, not
	// oversold, which is the safe way to be wrong. The runbook's stuck-holds
	// query finds them, and they are released by hand.
	if err := s.catalog.Settle(ctx, "released", holdIDs); err != nil {
		s.logger.ErrorContext(ctx, "released locally but could not tell the catalogue",
			slog.Any("error", err), slog.Int("holds", len(holdIDs)))
		return err
	}

	s.logger.InfoContext(ctx, "released expired reservations",
		slog.Int("reservations", len(expired)),
		slog.Int("orders_expired", len(orderIDs)))
	return nil
}

// clearedOrders asks Razorpay about each lapsed order and returns those it
// says took no money — the only ones whose stock may go back on sale.
//
// Anything short of a clear answer keeps the stock: a paying customer losing
// their produce costs far more than an abandoned checkout holding grams a
// little longer. So an order with money taken is held (its webhook is late,
// and will complete it when it lands), and an error stops the asking for this
// pass — Razorpay is down or refusing us, every later call would wait out the
// same timeout, and the next tick asks again.
func (s *Sweeper) clearedOrders(
	ctx context.Context, lapsed []store.ListLapsedRazorpayOrdersRow,
) []uuid.UUID {
	cleared := make([]uuid.UUID, 0, len(lapsed))
	for _, order := range lapsed {
		callCtx, cancel := context.WithTimeout(ctx, paymentCheckTimeout)
		payments, err := s.payments.OrderPayments(callCtx, order.RazorpayOrderID)
		cancel()

		switch {
		case errors.Is(err, razorpay.ErrOrderNotFound):
			// Razorpay has never heard of it, so nothing was paid on it.
			cleared = append(cleared, order.ID)
		case err != nil:
			s.logger.ErrorContext(ctx,
				"could not check payments with Razorpay; holding lapsed stock until it answers",
				slog.String("order_id", order.ID.String()),
				slog.Int("unchecked_orders", len(lapsed)-len(cleared)),
				slog.Any("error", err))
			return cleared
		case moneyTaken(payments):
			s.logger.WarnContext(ctx,
				"Razorpay took payment but no capture webhook has arrived; holding stock",
				slog.String("order_id", order.ID.String()),
				slog.String("razorpay_order_id", order.RazorpayOrderID),
				slog.String("alert", "payment_without_webhook"))
		default:
			cleared = append(cleared, order.ID)
		}
	}
	return cleared
}

func moneyTaken(payments []razorpay.Payment) bool {
	for _, payment := range payments {
		if payment.MoneyTaken() {
			return true
		}
	}
	return false
}
