package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vayal-mikrogreenz/vm-go-common/httpx"
	"github.com/vayal-mikrogreenz/vm-go-common/isttime"
	"github.com/vayal-mikrogreenz/vm-go-common/money"

	"github.com/vayal-mikrogreenz/vm-orders-api/internal/analyticsevents"
	"github.com/vayal-mikrogreenz/vm-orders-api/internal/catalogclient"
	"github.com/vayal-mikrogreenz/vm-orders-api/internal/mail"
	"github.com/vayal-mikrogreenz/vm-orders-api/internal/pricing"
	"github.com/vayal-mikrogreenz/vm-orders-api/internal/recurrence"
	"github.com/vayal-mikrogreenz/vm-orders-api/internal/scheduling"
	"github.com/vayal-mikrogreenz/vm-orders-api/internal/store"
)

// The charging run — CLAUDE.md §6.7.
//
// Once a minute it looks for schedules whose next delivery date is processed
// TODAY and, inside the window between ChargeAt and the cutoff, charges them:
//
//  1. plan the delivery against today's stock (deliver what is there)
//  2. check the wallet covers it, or skip the date (low balance)
//  3. reserve the stock with the catalogue
//  4. in ONE transaction: write the order, debit the wallet, mark it paid
//     (the same applyOrderPaid a card payment runs), record the occurrence,
//     and move the schedule to its next date
//  5. commit the stock in the catalogue
//
// The occurrence row's UNIQUE (schedule, date) is the idempotency guard: a run
// that crashes and restarts can attempt a date again only if nothing was
// recorded for it, and then nothing was charged either.

// scheduleRunLockKey keeps two replicas from charging the same schedules.
// Distinct from the sweeper's and the processor's keys.
const scheduleRunLockKey int64 = 8_231_004_775

// scheduleRunBatch bounds one tick. A backlog drains over later ticks.
const scheduleRunBatch = 200

// ScheduleRunner is the background job. It borrows API for the order-writing
// and payment code the checkout already uses.
type ScheduleRunner struct {
	api      *API
	interval time.Duration
	logger   *slog.Logger
	// now is overridable for tests.
	now func() time.Time
	// onlyCustomer narrows the run to one customer when set.
	onlyCustomer *uuid.UUID
}

// NewScheduleRunner builds the job.
func NewScheduleRunner(api *API, interval time.Duration, logger *slog.Logger) *ScheduleRunner {
	if interval <= 0 {
		interval = time.Minute
	}
	return &ScheduleRunner{
		api: api, interval: interval, now: time.Now,
		logger: logger.With(slog.String("job", "schedule-runner")),
	}
}

// WithClock replaces the clock, for tests.
func (s *ScheduleRunner) WithClock(now func() time.Time) *ScheduleRunner {
	s.now = now
	return s
}

// ForCustomer narrows every run to one customer's schedules — for tests
// against a shared database, and for an operator re-running one account.
func (s *ScheduleRunner) ForCustomer(customerID uuid.UUID) *ScheduleRunner {
	s.onlyCustomer = &customerID
	return s
}

// Run ticks until ctx is cancelled.
func (s *ScheduleRunner) Run(ctx context.Context) {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	s.logger.InfoContext(ctx, "schedule runner started", slog.Duration("interval", s.interval))
	for {
		select {
		case <-ctx.Done():
			s.logger.InfoContext(ctx, "schedule runner stopped")
			return
		case <-ticker.C:
			if err := s.RunOnce(ctx); err != nil {
				s.logger.ErrorContext(ctx, "schedule run failed", slog.Any("error", err))
			}
		}
	}
}

// RunOnce processes every schedule due now. Exported for tests and for an
// operator to trigger by hand.
func (s *ScheduleRunner) RunOnce(ctx context.Context) error {
	a := s.api
	conn, err := a.pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()

	var locked bool
	if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", scheduleRunLockKey).
		Scan(&locked); err != nil {
		return err
	}
	if !locked {
		return nil
	}
	defer func() {
		if _, err := conn.Exec(context.WithoutCancel(ctx),
			"SELECT pg_advisory_unlock($1)", scheduleRunLockKey); err != nil {
			s.logger.WarnContext(ctx, "could not release the advisory lock", slog.Any("error", err))
		}
	}()

	now := s.now()
	today := recurrence.Day(now)
	// The delivery date processed today, and the moment it may be charged.
	target := isttime.AddDays(today, 2)
	chargeAt := recurrence.ChargeAt(target, a.cutoffHour, a.schedules.ChargeLead)
	cutoff := isttime.AtHourIST(today, a.cutoffHour)
	inWindow := !now.Before(chargeAt) && now.Before(cutoff)

	// Outside the window only dates ALREADY past today's target are handled —
	// those the run never reached (the service was down through a window).
	// Inside it, today's target too.
	horizon := isttime.AddDays(target, -1)
	if inWindow {
		horizon = target
	}
	due, err := a.queries.ListDueSchedules(ctx, store.ListDueSchedulesParams{
		Horizon: horizon, CustomerID: s.onlyCustomer, MaxRows: scheduleRunBatch,
	})
	if err != nil {
		return err
	}
	if len(due) == 0 {
		return nil
	}

	var catalogue *catalogclient.Today
	var remaining map[uuid.UUID]int32
	for _, schedule := range due {
		next := recurrence.Day(*schedule.NextDeliveryDate)

		if next.Before(target) {
			s.recordAndAdvance(ctx, schedule, next, occurrenceMissed,
				"This date was not charged because the service was unavailable.")
			continue
		}

		// A date the customer skipped, or one already attempted.
		if _, err := a.queries.GetOccurrence(ctx, store.GetOccurrenceParams{
			ScheduleID: schedule.ID, DeliveryDate: next,
		}); err == nil {
			s.advance(ctx, schedule, next, schedule.LowBalanceSkips)
			continue
		} else if !errors.Is(err, pgx.ErrNoRows) {
			s.logger.ErrorContext(ctx, "reading an occurrence", slog.Any("error", err))
			continue
		}

		// One catalogue fetch per tick, shared by every schedule in it.
		if catalogue == nil {
			fetched, err := a.catalog.TodaysCatalogue(ctx)
			if err != nil {
				// Leave everything for the next tick: the window is 30
				// minutes of ticks, and a brief catalogue outage should not
				// skip anyone's delivery.
				return fmt.Errorf("fetching today's catalogue: %w", err)
			}
			catalogue = &fetched
			remaining = copyGrams(fetched.RemainingGrams)
		}

		if err := s.charge(ctx, schedule, next, now, catalogue, remaining); err != nil {
			s.logger.ErrorContext(ctx, "charging a scheduled delivery failed; will retry next tick",
				slog.String("schedule_id", schedule.ID.String()),
				slog.String("delivery_date", isttime.FormatISODate(next)),
				slog.Any("error", err))
		}
	}
	return nil
}

func copyGrams(in map[uuid.UUID]int32) map[uuid.UUID]int32 {
	out := make(map[uuid.UUID]int32, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// charge attempts one schedule's delivery date. A returned error means
// NOTHING was recorded or charged, so the next tick may try again.
func (s *ScheduleRunner) charge(
	ctx context.Context, schedule store.Schedule, delivery, now time.Time,
	catalogue *catalogclient.Today, remaining map[uuid.UUID]int32,
) error {
	a := s.api
	items, err := a.queries.ListScheduleItems(ctx, schedule.ID)
	if err != nil {
		return err
	}
	wants := make([]scheduling.Want, 0, len(items))
	for _, item := range items {
		wants = append(wants, scheduling.Want{
			UnitID: item.ProductUnitID, Qty: item.Qty,
			Name: item.ProductNameSnapshot + " · " + item.UnitLabelSnapshot,
		})
	}

	plan := scheduling.Build(wants, catalogue.Units, remaining)
	if plan.Empty() {
		note := plan.Note()
		s.recordAndAdvance(ctx, schedule, delivery, occurrenceSkippedNoStock, note)
		s.notify(ctx, schedule.ID, mail.ScheduleNotice{
			DeliveryDate: delivery, Reason: mail.ScheduleNoStock, Note: note,
		})
		return nil
	}
	breakdown := priceOf(plan, a.pricing)

	// Checked before reserving so a low wallet never holds stock. Checked
	// again, under the lock, inside the transaction.
	balance := a.walletBalance(ctx, schedule.CustomerID)
	if balance < breakdown.TotalPaise {
		giveBack(plan, remaining)
		s.skipLowBalance(ctx, schedule, delivery, breakdown.TotalPaise, balance)
		return nil
	}

	orderID := uuid.New()
	holds, err := a.catalog.Reserve(ctx, orderID, a.reserveTTL, reserveLinesOf(plan))
	if err != nil {
		var appErr *httpx.Error
		if !errors.As(err, &appErr) {
			giveBack(plan, remaining)
			return fmt.Errorf("reserving: %w", err)
		}
		// Stock moved between the catalogue read and the reservation —
		// another customer bought it. Re-plan once against fresh figures.
		fresh, fetchErr := a.catalog.TodaysCatalogue(ctx)
		if fetchErr != nil {
			return fmt.Errorf("refetching the catalogue: %w", fetchErr)
		}
		*catalogue = fresh
		for k := range remaining {
			delete(remaining, k)
		}
		for k, v := range fresh.RemainingGrams {
			remaining[k] = v
		}
		plan = scheduling.Build(wants, fresh.Units, remaining)
		if plan.Empty() {
			note := plan.Note()
			s.recordAndAdvance(ctx, schedule, delivery, occurrenceSkippedNoStock, note)
			s.notify(ctx, schedule.ID, mail.ScheduleNotice{
				DeliveryDate: delivery, Reason: mail.ScheduleNoStock, Note: note,
			})
			return nil
		}
		breakdown = priceOf(plan, a.pricing)
		if balance < breakdown.TotalPaise {
			giveBack(plan, remaining)
			s.skipLowBalance(ctx, schedule, delivery, breakdown.TotalPaise, balance)
			return nil
		}
		holds, err = a.catalog.Reserve(ctx, orderID, a.reserveTTL, reserveLinesOf(plan))
		if err != nil {
			giveBack(plan, remaining)
			if errors.As(err, &appErr) {
				s.recordAndAdvance(ctx, schedule, delivery, occurrenceSkippedNoStock,
					"The produce sold out while your delivery was being packed.")
				s.notify(ctx, schedule.ID, mail.ScheduleNotice{
					DeliveryDate: delivery, Reason: mail.ScheduleNoStock,
				})
				return nil
			}
			return fmt.Errorf("reserving again: %w", err)
		}
	}

	supplierIDs := make([]uuid.UUID, 0, len(plan.Lines))
	for _, line := range plan.Lines {
		supplierIDs = append(supplierIDs, line.Unit.SupplierID)
	}
	commissions := a.supplierCommissions(ctx, supplierIDs)

	order, holdIDs, err := s.writePaidOrder(ctx, schedule, delivery, now, orderID, plan,
		breakdown, holds, commissions)
	if err != nil {
		a.releaseHolds(ctx, holds)
		giveBack(plan, remaining)
		if errors.Is(err, errInsufficientBalance) {
			// The balance fell between the check and the lock — another
			// schedule of the same customer charged first.
			s.skipLowBalance(ctx, schedule, delivery, breakdown.TotalPaise,
				a.walletBalance(ctx, schedule.CustomerID))
			return nil
		}
		if isUniqueViolation(err) {
			// Recorded by someone else meanwhile; nothing of ours was kept.
			return nil
		}
		return err
	}

	a.settleStockAfterCommit(ctx, order.ID, holdIDs)
	s.logger.InfoContext(ctx, "scheduled delivery placed",
		slog.String("schedule_id", schedule.ID.String()),
		slog.String("order_number", order.OrderNumber),
		slog.String("delivery_date", isttime.FormatISODate(delivery)),
		slog.Int64("total_paise", order.TotalPaise),
		slog.Int("short_lines", len(plan.Short)))
	return nil
}

func priceOf(plan scheduling.Plan, cfg pricing.Config) pricing.Breakdown {
	lines := make([]pricing.Line, 0, len(plan.Lines))
	for _, line := range plan.Lines {
		lines = append(lines, pricing.Line{
			SupplierID: line.Unit.SupplierID, UnitPricePaise: line.Unit.PricePaise, Qty: int64(line.Qty),
		})
	}
	return pricing.Compute(lines, cfg)
}

func reserveLinesOf(plan scheduling.Plan) []catalogclient.ReserveLine {
	grams := plan.GramsBySizeCode()
	lines := make([]catalogclient.ReserveLine, 0, len(grams))
	for sizeCodeID, g := range grams {
		lines = append(lines, catalogclient.ReserveLine{SizeCodeID: sizeCodeID, Grams: g})
	}
	// The lock order catalog uses, as at checkout.
	sort.Slice(lines, func(i, j int) bool {
		return lines[i].SizeCodeID.String() < lines[j].SizeCodeID.String()
	})
	return lines
}

// giveBack returns a plan's grams to the tick's budget when it was not kept.
func giveBack(plan scheduling.Plan, remaining map[uuid.UUID]int32) {
	for sizeCodeID, grams := range plan.GramsBySizeCode() {
		remaining[sizeCodeID] += grams
	}
}

// writePaidOrder is step 4: order, wallet debit, paid, occurrence, next date —
// one transaction, so money and goods can never disagree.
func (s *ScheduleRunner) writePaidOrder(
	ctx context.Context, schedule store.Schedule, delivery, now time.Time, orderID uuid.UUID,
	plan scheduling.Plan, breakdown pricing.Breakdown, holds []catalogclient.Hold,
	commissions map[uuid.UUID]int64,
) (store.Order, []uuid.UUID, error) {
	a := s.api
	var address addressSnapshot
	if err := json.Unmarshal(schedule.AddressSnapshot, &address); err != nil {
		return store.Order{}, nil, err
	}

	lines := make([]resolvedLine, 0, len(plan.Lines))
	for _, line := range plan.Lines {
		lines = append(lines, resolvedLine{
			item: store.CartItem{
				ProductUnitID: line.Unit.ID, ProductID: line.Unit.ProductID,
				SupplierID: line.Unit.SupplierID, Qty: line.Qty,
			},
			unit: line.Unit,
		})
	}

	tx, err := a.pool.Begin(ctx)
	if err != nil {
		return store.Order{}, nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit
	q := a.queries.WithTx(tx)

	// The schedule is re-read under its lock: a customer who paused or
	// cancelled a second ago must not be charged.
	current, err := q.LockSchedule(ctx, schedule.ID)
	if err != nil {
		return store.Order{}, nil, err
	}
	if current.Status != scheduleActive || current.NextDeliveryDate == nil ||
		!recurrence.Day(*current.NextDeliveryDate).Equal(delivery) {
		return store.Order{}, nil, errScheduleMoved
	}

	order, err := a.writeOrder(ctx, q, persistArgs{
		orderID:    orderID,
		customerID: schedule.CustomerID,
		address:    address,
		breakdown:  breakdown,
		schedule:   recurrence.Timeline(delivery, now, a.cutoffHour),
		items:      lines,
		holds:      holds,
		now:        now,
	})
	if err != nil {
		return store.Order{}, nil, err
	}
	if err := q.SetOrderSchedule(ctx, store.SetOrderScheduleParams{
		ID: order.ID, ScheduleID: &schedule.ID,
	}); err != nil {
		return store.Order{}, nil, err
	}
	if err := analyticsevents.Emit(ctx, q, order.ID, analyticsevents.OrderPlaced,
		analyticsevents.ActorSystem, order.PlacedAt, analyticsevents.Options{}); err != nil {
		return store.Order{}, nil, err
	}
	if _, err := q.CreateWalletPayment(ctx, store.CreateWalletPaymentParams{
		OrderID: order.ID, AmountPaise: order.TotalPaise,
	}); err != nil {
		return store.Order{}, nil, err
	}

	debited := order.ID
	entry, err := moveWallet(ctx, q, schedule.CustomerID, -money.Paise(order.TotalPaise),
		walletKindOrderDebit, walletRefs{orderID: &debited})
	if err != nil {
		return store.Order{}, nil, err
	}

	// The same transition a captured card payment runs: reservations
	// committed, supplier payouts created, confirmation email queued.
	reference := "wallet:" + entry.ID.String()
	_, holdIDs, _, ok, err := applyOrderPaid(ctx, q, order.ID, &reference, "wallet", analyticsevents.PaidViaWallet, nil,
		a.Rates, commissions)
	if err != nil {
		return store.Order{}, nil, err
	}
	if !ok {
		return store.Order{}, nil, errors.New("a new scheduled order was not payable")
	}

	note := plan.Note()
	var notePtr *string
	if note != "" {
		notePtr = &note
	}
	if _, err := q.CreateOccurrence(ctx, store.CreateOccurrenceParams{
		ScheduleID: schedule.ID, DeliveryDate: delivery, Status: occurrencePlaced,
		OrderID: &order.ID, Note: notePtr,
	}); err != nil {
		return store.Order{}, nil, err
	}
	if err := advanceIn(ctx, q, current, delivery, 0); err != nil {
		return store.Order{}, nil, err
	}

	if note != "" {
		payload, _ := json.Marshal(scheduleNoticePayload{
			ScheduleID: schedule.ID.String(), DeliveryDate: isttime.FormatISODate(delivery),
			Reason: mail.SchedulePartial, Note: note, OrderNumber: order.OrderNumber,
		})
		if _, err := q.EnqueueOutbox(ctx, store.EnqueueOutboxParams{
			AggregateType: "schedule", AggregateID: schedule.ID,
			EventType: eventScheduleNotice, Payload: payload,
		}); err != nil {
			return store.Order{}, nil, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return store.Order{}, nil, err
	}
	return order, holdIDs, nil
}

// errScheduleMoved means the schedule changed under the run — paused,
// cancelled or already advanced. Nothing is charged; the next tick re-reads.
var errScheduleMoved = errors.New("the schedule changed while it was being charged")

// advanceIn moves a schedule past delivery to its next date, completing it
// when none is left.
func advanceIn(
	ctx context.Context, q *store.Queries, schedule store.Schedule, delivery time.Time, lowSkips int32,
) error {
	status := schedule.Status
	next, found := ruleOf(schedule).After(delivery)
	var nextPtr *time.Time
	if found {
		nextPtr = &next
	} else if status == scheduleActive {
		status = scheduleCompleted
	}
	_, err := q.SetScheduleStatus(ctx, store.SetScheduleStatusParams{
		ID: schedule.ID, Status: status, NextDeliveryDate: nextPtr, LowBalanceSkips: lowSkips,
	})
	return err
}

// advance moves the schedule on without charging (the date was skipped or
// already recorded).
func (s *ScheduleRunner) advance(ctx context.Context, schedule store.Schedule, delivery time.Time, lowSkips int32) {
	if err := advanceIn(ctx, s.api.queries, schedule, delivery, lowSkips); err != nil {
		s.logger.ErrorContext(ctx, "advancing a schedule", slog.Any("error", err),
			slog.String("schedule_id", schedule.ID.String()))
	}
}

// recordAndAdvance records an uncharged outcome and moves on, in one
// transaction so a date is never both recorded and still due.
func (s *ScheduleRunner) recordAndAdvance(
	ctx context.Context, schedule store.Schedule, delivery time.Time, status, note string,
) {
	s.recordAndAdvanceWith(ctx, schedule, delivery, status, note, schedule.LowBalanceSkips, false)
}

func (s *ScheduleRunner) recordAndAdvanceWith(
	ctx context.Context, schedule store.Schedule, delivery time.Time,
	status, note string, lowSkips int32, pause bool,
) {
	a := s.api
	err := func() error {
		tx, err := a.pool.Begin(ctx)
		if err != nil {
			return err
		}
		defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit
		q := a.queries.WithTx(tx)

		var notePtr *string
		if note != "" {
			notePtr = &note
		}
		if _, err := q.CreateOccurrence(ctx, store.CreateOccurrenceParams{
			ScheduleID: schedule.ID, DeliveryDate: delivery, Status: status, Note: notePtr,
		}); err != nil && !isUniqueViolation(err) {
			return err
		}
		if pause {
			schedule.Status = schedulePaused
		}
		if err := advanceIn(ctx, q, schedule, delivery, lowSkips); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}()
	if err != nil {
		s.logger.ErrorContext(ctx, "recording a schedule outcome", slog.Any("error", err),
			slog.String("schedule_id", schedule.ID.String()), slog.String("status", status))
	}
}

// skipLowBalance records a low-balance skip, pausing the schedule after
// LowBalancePauseAfter in a row, and tells the customer.
func (s *ScheduleRunner) skipLowBalance(
	ctx context.Context, schedule store.Schedule, delivery time.Time, needed, balance money.Paise,
) {
	skips := schedule.LowBalanceSkips + 1
	pause := int(skips) >= s.api.schedules.LowBalancePauseAfter
	note := fmt.Sprintf("Needed %s; the wallet held %s.",
		money.FormatRupees(needed), money.FormatRupees(balance))
	s.recordAndAdvanceWith(ctx, schedule, delivery, occurrenceSkippedLowBalance, note, skips, pause)
	s.notify(ctx, schedule.ID, mail.ScheduleNotice{
		DeliveryDate: delivery, Reason: mail.ScheduleLowBalance,
		Needed: needed, Balance: balance, Paused: pause,
	})
}

// ---------------------------------------------------------------------------
// Notices, through the outbox
// ---------------------------------------------------------------------------

const eventScheduleNotice = "schedule.notice"

type scheduleNoticePayload struct {
	ScheduleID   string `json:"schedule_id"`
	DeliveryDate string `json:"delivery_date"`
	Reason       string `json:"reason"`
	Note         string `json:"note,omitempty"`
	NeededPaise  int64  `json:"needed_paise,omitempty"`
	BalancePaise int64  `json:"balance_paise,omitempty"`
	Paused       bool   `json:"paused,omitempty"`
	OrderNumber  string `json:"order_number,omitempty"`
}

// notify queues a notice outside any transaction — for outcomes whose record
// has already committed. A lost notice is a missed email, never lost money.
func (s *ScheduleRunner) notify(ctx context.Context, scheduleID uuid.UUID, notice mail.ScheduleNotice) {
	payload, err := json.Marshal(scheduleNoticePayload{
		ScheduleID:   scheduleID.String(),
		DeliveryDate: isttime.FormatISODate(notice.DeliveryDate),
		Reason:       notice.Reason,
		Note:         notice.Note,
		NeededPaise:  notice.Needed.Int64(),
		BalancePaise: notice.Balance.Int64(),
		Paused:       notice.Paused,
	})
	if err != nil {
		return
	}
	if _, err := s.api.queries.EnqueueOutbox(ctx, store.EnqueueOutboxParams{
		AggregateType: "schedule", AggregateID: scheduleID,
		EventType: eventScheduleNotice, Payload: payload,
	}); err != nil {
		s.logger.ErrorContext(ctx, "could not queue a schedule notice", slog.Any("error", err))
	}
}
