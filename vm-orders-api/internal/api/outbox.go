package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vayal-mikrogreenz/vm-go-common/isttime"
	"github.com/vayal-mikrogreenz/vm-go-common/money"

	"github.com/vayal-mikrogreenz/vm-orders-api/internal/mail"
	"github.com/vayal-mikrogreenz/vm-orders-api/internal/store"
	"github.com/vayal-mikrogreenz/vm-orders-api/internal/timeline"
)

// outboxBatch bounds one tick's work. Small: each row is an SMTP round trip,
// and a relay that has gone slow should delay a handful of emails rather than
// tie the worker up for minutes.
const outboxBatch = 20

// OutboxDispatcher sends the emails the order flow promised.
//
// CLAUDE.md §5.3 has payment write an `order.confirmed` row to `outbox` in the
// same transaction as the state change, so an email is never promised for an
// order that rolled back. This is the other half of that: without it the rows
// simply accumulated, and every customer who paid received nothing at all.
//
// Claim, then send, then mark — never sending inside the claiming transaction.
// An SMTP round trip while holding a pooled connection is how a slow relay
// drains the pool with Postgres sitting idle. A send that fails releases its
// row, so the next tick tries again and nothing is lost.
type OutboxDispatcher struct {
	pool     *pgxpool.Pool
	queries  *store.Queries
	api      *API
	sender   mail.Sender
	interval time.Duration
	support  string
	logger   *slog.Logger
}

// NewOutboxDispatcher builds the worker, or nil when no sender is configured.
func NewOutboxDispatcher(
	pool *pgxpool.Pool, api *API, sender mail.Sender,
	interval time.Duration, supportEmail string, logger *slog.Logger,
) *OutboxDispatcher {
	if sender == nil {
		return nil
	}
	if interval <= 0 {
		interval = time.Minute
	}
	return &OutboxDispatcher{
		pool: pool, queries: store.New(pool), api: api, sender: sender,
		interval: interval, support: supportEmail,
		logger: logger.With(slog.String("job", "outbox-dispatcher")),
	}
}

// Run drains the outbox until the context is cancelled.
func (d *OutboxDispatcher) Run(ctx context.Context) {
	if d == nil {
		return
	}
	ticker := time.NewTicker(d.interval)
	defer ticker.Stop()

	d.logger.InfoContext(ctx, "outbox dispatcher started",
		slog.Duration("interval", d.interval))

	for {
		select {
		case <-ctx.Done():
			d.logger.InfoContext(ctx, "outbox dispatcher stopped")
			return
		case <-ticker.C:
			d.tick(ctx)
		}
	}
}

// Tick runs one pass. Exported for the dev tool in internal/devtools, so a
// deployment can be checked without waiting a minute for the ticker.
func (d *OutboxDispatcher) Tick(ctx context.Context) { d.tick(ctx) }

func (d *OutboxDispatcher) tick(ctx context.Context) {
	claimed, err := d.claim(ctx)
	if err != nil {
		d.logger.ErrorContext(ctx, "claiming outbox rows", slog.Any("error", err))
		return
	}

	for _, row := range claimed {
		if err := d.deliver(ctx, row); err != nil {
			// Released rather than left published, so the next tick retries.
			// The error is stored on the row, so a message that keeps failing
			// can be found without reading logs.
			if _, relErr := d.queries.ReleaseOutbox(ctx, store.ReleaseOutboxParams{
				ID: row.ID, LastError: ptr(err.Error()),
			}); relErr != nil {
				d.logger.ErrorContext(ctx, "could not release a failed outbox row",
					slog.Any("error", relErr), slog.String("outbox_id", row.ID.String()),
					slog.String("alert", "outbox_stuck"))
			}
			d.logger.WarnContext(ctx, "outbox delivery failed; will retry",
				slog.Any("error", err), slog.String("event_type", row.EventType),
				slog.String("aggregate_id", row.AggregateID.String()))
		}
	}
}

// claim marks a batch published up-front, inside one short transaction.
//
// Optimistic on purpose: SKIP LOCKED plus the published_at stamp means two
// replicas can never take the same row, and the release path above covers the
// failure. The alternative — holding the rows locked across the SMTP call —
// trades a rare duplicate for a routine connection leak.
func (d *OutboxDispatcher) claim(ctx context.Context) ([]store.Outbox, error) {
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit
	q := d.queries.WithTx(tx)

	rows, err := q.ClaimOutboxBatch(ctx, outboxBatch)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		if _, err := q.MarkOutboxPublished(ctx, row.ID); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return rows, nil
}

// deliver turns one outbox row into an email.
//
// An event type nobody handles is NOT an error: it stays published, because
// leaving it to be retried forever would bury the rows that do matter. It is
// logged once, which is enough to notice a producer that shipped ahead of a
// consumer.
func (d *OutboxDispatcher) deliver(ctx context.Context, row store.Outbox) error {
	switch row.EventType {
	case "order.confirmed":
		return d.sendOrderConfirmation(ctx, row.AggregateID)
	case eventScheduleNotice:
		return d.sendScheduleNotice(ctx, row)
	}
	d.logger.InfoContext(ctx, "outbox event with no handler; dropping",
		slog.String("event_type", row.EventType))
	return nil
}

func (d *OutboxDispatcher) sendOrderConfirmation(ctx context.Context, orderID uuid.UUID) error {
	order, err := d.queries.OrderForEmail(ctx, orderID)
	if err != nil {
		return err
	}
	items, err := d.queries.ListOrderItems(ctx, orderID)
	if err != nil {
		return err
	}

	contacts, err := d.api.customerContacts(ctx, []uuid.UUID{order.CustomerID})
	if err != nil {
		return err
	}
	contact, ok := contacts[order.CustomerID]
	if !ok {
		// No address on file — a phone-only signup. Nothing to retry: the row
		// stays published rather than failing on every tick forever.
		d.logger.InfoContext(ctx, "no email address for this customer; nothing sent",
			slog.String("order_number", order.OrderNumber))
		return nil
	}

	schedule := timeline.Schedule{
		PlacedAt:             order.PlacedAt,
		ProcessingAt:         order.ProcessingAt,
		DeliveryDay:          order.DeliveryDay,
		ExpectedDeliveryDate: order.ExpectedDeliveryDate,
	}

	lines := make([]mail.OrderLine, 0, len(items))
	for _, item := range items {
		lines = append(lines, mail.OrderLine{
			ProductName: item.ProductNameSnapshot,
			UnitLabel:   item.UnitLabelSnapshot,
			Grade:       deref(item.GradeSnapshot),
			SizeCode:    deref(item.SizeCodeSnapshot),
			Qty:         item.Qty,
			LineTotal:   money.Paise(item.LineTotalPaise),
		})
	}

	milestones := make([]mail.Milestone, 0, 3)
	for _, milestone := range schedule.MilestonesForStatus(time.Now(), order.Status) {
		milestones = append(milestones, mail.Milestone{
			Name: milestone.Name, When: milestone.At, Done: milestone.Completed,
		})
	}

	var address addressSnapshot
	_ = json.Unmarshal(order.AddressSnapshot, &address)

	message := mail.BuildOrderConfirmation(mail.OrderConfirmation{
		CustomerName:         contact.Name,
		OrderNumber:          order.OrderNumber,
		PlacedAt:             isttime.ToIST(order.PlacedAt),
		Lines:                lines,
		Subtotal:             money.Paise(order.SubtotalPaise),
		PlatformFee:          money.Paise(order.PlatformFeePaise),
		DeliveryFee:          money.Paise(order.DeliveryFeePaise),
		Total:                money.Paise(order.TotalPaise),
		Milestones:           milestones,
		ExpectedDeliveryDate: order.ExpectedDeliveryDate,
		DeliveryAddress:      address.Lines(),
		SupportEmail:         d.support,
	})
	message.To = contact.Email

	if err := d.sender.Send(ctx, message); err != nil {
		return err
	}
	d.logger.InfoContext(ctx, "order confirmation sent",
		slog.String("order_number", order.OrderNumber))
	return nil
}

// sendScheduleNotice emails a skipped, paused or partial scheduled delivery —
// CLAUDE.md §6.7.
func (d *OutboxDispatcher) sendScheduleNotice(ctx context.Context, row store.Outbox) error {
	var payload scheduleNoticePayload
	if err := json.Unmarshal(row.Payload, &payload); err != nil {
		// A payload this code wrote and cannot read will not improve on retry.
		d.logger.ErrorContext(ctx, "unreadable schedule notice; dropping", slog.Any("error", err))
		return nil
	}
	schedule, err := d.queries.GetScheduleByID(ctx, row.AggregateID)
	if err != nil {
		return err
	}
	contacts, err := d.api.customerContacts(ctx, []uuid.UUID{schedule.CustomerID})
	if err != nil {
		return err
	}
	contact, ok := contacts[schedule.CustomerID]
	if !ok {
		return nil
	}
	day, err := isttime.ParseISODate(payload.DeliveryDate)
	if err != nil {
		return nil
	}

	message := mail.BuildScheduleNotice(mail.ScheduleNotice{
		CustomerName: contact.Name,
		DeliveryDate: day,
		Reason:       payload.Reason,
		Note:         payload.Note,
		Needed:       money.Paise(payload.NeededPaise),
		Balance:      money.Paise(payload.BalancePaise),
		Paused:       payload.Paused,
		OrderNumber:  payload.OrderNumber,
		SupportEmail: d.support,
	})
	message.To = contact.Email
	return d.sender.Send(ctx, message)
}

func ptr[T any](value T) *T { return &value }
