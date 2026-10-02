// Package projector turns product and order events into analytics rows
// (CLAUDE.md §5.4).
//
// Events arrive grouped by the source transaction that wrote them, in commit
// order. One source transaction is applied in ONE analytics transaction, and
// only after that commits does the stream confirm its position to Postgres —
// so a crash between the two replays the transaction, and the processed_events
// claim makes the replay a no-op.
//
// Each event runs inside its own savepoint. An event that can never be
// applied (a payload that does not parse, a schema version this build does not
// know, data a constraint rejects) is parked in dead_letter_events and the
// rest carry on: a poison event must not stop every event behind it. Anything
// else — the database going away — fails the whole transaction, which is
// retried as a unit.
package projector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vayal-mikrogreenz/vm-events-consumer-api/internal/store"
)

// Source tables, as the replication stream names them.
const (
	SupplierEvents = "profile.supplier_events_outbox"
	ProductEvents  = "catalog.product_events_outbox"
	OrderEvents    = "orders.order_events_outbox"
)

// Event is one outbox row, as decoded from the replication stream.
type Event struct {
	ID            uuid.UUID
	Table         string
	AggregateID   uuid.UUID
	EventType     string
	SchemaVersion int
	OccurredAt    time.Time
	ActorRole     string
	Payload       []byte
	// LSN is the commit position of the source transaction, for tracing a
	// row back to the WAL.
	LSN string
}

// permanentError marks an event retrying cannot fix.
type permanentError struct{ error }

func (p permanentError) Unwrap() error { return p.error }

func permanent(format string, args ...any) error {
	return permanentError{fmt.Errorf(format, args...)}
}

// IsPermanent reports whether err is one no retry can fix.
func IsPermanent(err error) bool {
	var p permanentError
	if errors.As(err, &p) {
		return true
	}
	// Data exceptions (22xxx) and integrity violations (23xxx) come from the
	// event's content, not the database's health: replaying it fails the same
	// way every time.
	var pg *pgconn.PgError
	if errors.As(err, &pg) && len(pg.Code) == 5 {
		return pg.Code[:2] == "22" || pg.Code[:2] == "23"
	}
	return false
}

// Projector applies events to the analytics schema.
type Projector struct {
	pool   *pgxpool.Pool
	logger *slog.Logger
}

// New builds a projector writing through pool (the analytics writer role).
func New(pool *pgxpool.Pool, logger *slog.Logger) *Projector {
	return &Projector{pool: pool, logger: logger}
}

// Result counts what one ApplyTransaction did.
type Result struct {
	Applied, Duplicates, DeadLettered int
}

// ApplyTransaction applies one source transaction's events atomically.
//
// A returned error means NOTHING was applied and the caller must retry the
// same events; the position must not be confirmed.
func (p *Projector) ApplyTransaction(ctx context.Context, events []Event) (Result, error) {
	var res Result
	if len(events) == 0 {
		return res, nil
	}

	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return res, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit

	for _, ev := range events {
		outcome, err := p.applyOne(ctx, tx, ev)
		if err != nil {
			return Result{}, err
		}
		switch outcome {
		case applied:
			res.Applied++
		case duplicate:
			res.Duplicates++
		case deadLettered:
			res.DeadLettered++
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return Result{}, err
	}
	return res, nil
}

type outcome int

const (
	applied outcome = iota
	duplicate
	deadLettered
)

func (p *Projector) applyOne(ctx context.Context, tx pgx.Tx, ev Event) (outcome, error) {
	q := store.New(tx)

	if _, err := q.ClaimEvent(ctx, store.ClaimEventParams{
		EventID: ev.ID, SourceTable: ev.Table, Lsn: ev.LSN,
	}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return duplicate, nil
		}
		return 0, err
	}

	savepoint, err := tx.Begin(ctx)
	if err != nil {
		return 0, err
	}
	projectErr := project(ctx, store.New(savepoint), ev)
	if projectErr == nil {
		if err := savepoint.Commit(ctx); err != nil {
			return 0, err
		}
		return applied, nil
	}
	if err := savepoint.Rollback(ctx); err != nil {
		return 0, err
	}
	if !IsPermanent(projectErr) {
		return 0, projectErr
	}

	// Parked, and still claimed: a replay must not try it again either.
	p.logger.ErrorContext(ctx, "event dead-lettered",
		slog.String("event_id", ev.ID.String()), slog.String("event_type", ev.EventType),
		slog.String("table", ev.Table), slog.Any("error", projectErr),
		slog.String("alert", "analytics_event_dead_lettered"))
	eventType := ev.EventType
	// A payload that is not valid JSON cannot go in the JSONB column, and a
	// failed insert would abort the whole transaction — so it is parked
	// without one, and the error text says why.
	var payload []byte
	if json.Valid(ev.Payload) {
		payload = ev.Payload
	}
	if err := q.DeadLetter(ctx, store.DeadLetterParams{
		EventID: ev.ID, SourceTable: ev.Table, EventType: &eventType,
		Payload: payload, Error: projectErr.Error(), Lsn: ev.LSN,
	}); err != nil {
		return 0, err
	}
	return deadLettered, nil
}

// project routes one event to its projection.
func project(ctx context.Context, q *store.Queries, ev Event) error {
	switch ev.Table {
	case OrderEvents:
		return projectOrder(ctx, q, ev)
	case ProductEvents:
		return projectProduct(ctx, q, ev)
	case SupplierEvents:
		return projectSupplier(ctx, q, ev)
	default:
		return permanent("no projection for table %s", ev.Table)
	}
}
