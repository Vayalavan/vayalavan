// Package cdc reads product and order events from Postgres logical
// replication (CLAUDE.md §5.4).
//
// It consumes the pgoutput stream of the `vayal_analytics` publication — the
// INSERTs into catalog.product_events_outbox and orders.order_events_outbox —
// groups them by source transaction, and hands each transaction to a handler.
// Only when the handler returns nil does the stream confirm that
// transaction's end position to Postgres. That ordering is the whole delivery
// guarantee: an event is never acknowledged before it is in the analytics
// schema, so a crash at any point replays it, and the projector's dedupe turns
// the replay into a no-op. At-least-once in, exactly-once in effect.
package cdc

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pglogrepl"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgproto3"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vayal-mikrogreenz/vm-events-consumer-api/internal/projector"
)

// Handler applies one source transaction. A nil return confirms it.
type Handler func(ctx context.Context, events []projector.Event) error

// Config is the stream's connection and slot.
type Config struct {
	ReplicationURL string
	SlotName       string
	Publication    string
	StatusInterval time.Duration
}

// Status is what /readyz reports.
type Status struct {
	Connected     bool      `json:"connected"`
	ConfirmedLSN  string    `json:"confirmed_lsn"`
	LastAppliedAt time.Time `json:"last_applied_at"`
	EventsApplied int64     `json:"events_applied"`
	LastError     string    `json:"last_error,omitempty"`
	LastErrorAt   time.Time `json:"last_error_at"`
}

// Stream is a reconnecting logical-replication consumer.
type Stream struct {
	cfg     Config
	handler Handler
	logger  *slog.Logger

	mu     sync.Mutex
	status Status
}

// New builds a stream.
func New(cfg Config, handler Handler, logger *slog.Logger) *Stream {
	return &Stream{cfg: cfg, handler: handler, logger: logger.With(slog.String("job", "cdc"))}
}

// Status returns a copy of the stream's current state.
func (s *Stream) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status
}

// Ready reports an error unless the stream is connected.
func (s *Stream) Ready(context.Context) error {
	st := s.Status()
	if !st.Connected {
		if st.LastError != "" {
			return fmt.Errorf("replication stream down: %s", st.LastError)
		}
		return errors.New("replication stream not connected")
	}
	return nil
}

func (s *Stream) setConnected(c bool) {
	s.mu.Lock()
	s.status.Connected = c
	s.mu.Unlock()
}

func (s *Stream) recordError(err error) {
	s.mu.Lock()
	s.status.Connected = false
	s.status.LastError = err.Error()
	s.status.LastErrorAt = time.Now()
	s.mu.Unlock()
}

func (s *Stream) recordApplied(lsn pglogrepl.LSN, n int) {
	s.mu.Lock()
	s.status.ConfirmedLSN = lsn.String()
	s.status.LastAppliedAt = time.Now()
	s.status.EventsApplied += int64(n)
	s.mu.Unlock()
}

// Run consumes until ctx is cancelled, reconnecting with backoff on any error.
func (s *Stream) Run(ctx context.Context) {
	backoff := time.Second
	for {
		err := s.session(ctx)
		if ctx.Err() != nil {
			s.logger.InfoContext(ctx, "replication stream stopped")
			return
		}
		s.recordError(err)
		s.logger.ErrorContext(ctx, "replication stream failed; reconnecting",
			slog.Any("error", err), slog.Duration("in", backoff),
			slog.String("alert", "analytics_cdc_down"))
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, time.Minute)
	}
}

// session is one connection's lifetime.
func (s *Stream) session(ctx context.Context) error {
	conn, err := pgconn.Connect(ctx, s.cfg.ReplicationURL)
	if err != nil {
		return fmt.Errorf("connecting: %w", err)
	}
	defer conn.Close(context.WithoutCancel(ctx))

	if err := s.ensureSlot(ctx, conn); err != nil {
		return err
	}

	// LSN 0: resume from the slot's confirmed position, which Postgres keeps.
	// Nothing about our progress lives on this side of the connection.
	if err := pglogrepl.StartReplication(ctx, conn, s.cfg.SlotName, 0,
		pglogrepl.StartReplicationOptions{PluginArgs: []string{
			"proto_version '1'",
			fmt.Sprintf("publication_names '%s'", s.cfg.Publication),
		}}); err != nil {
		return fmt.Errorf("starting replication: %w", err)
	}
	s.setConnected(true)
	s.logger.InfoContext(ctx, "replication stream started",
		slog.String("slot", s.cfg.SlotName), slog.String("publication", s.cfg.Publication))

	dec := newDecoder()
	var confirmed pglogrepl.LSN
	nextStatus := time.Now().Add(s.cfg.StatusInterval)

	for {
		if time.Now().After(nextStatus) {
			if err := sendStatus(ctx, conn, confirmed); err != nil {
				return err
			}
			nextStatus = time.Now().Add(s.cfg.StatusInterval)
		}

		recvCtx, cancel := context.WithDeadline(ctx, nextStatus)
		raw, err := conn.ReceiveMessage(recvCtx)
		cancel()
		if err != nil {
			if pgconn.Timeout(err) && ctx.Err() == nil {
				continue
			}
			return fmt.Errorf("receiving: %w", err)
		}

		switch msg := raw.(type) {
		case *pgproto3.ErrorResponse:
			return fmt.Errorf("server error: %s", msg.Message)
		case *pgproto3.CopyData:
			switch msg.Data[0] {
			case pglogrepl.PrimaryKeepaliveMessageByteID:
				ka, err := pglogrepl.ParsePrimaryKeepaliveMessage(msg.Data[1:])
				if err != nil {
					return fmt.Errorf("keepalive: %w", err)
				}
				if ka.ReplyRequested {
					nextStatus = time.Time{}
				}
			case pglogrepl.XLogDataByteID:
				xld, err := pglogrepl.ParseXLogData(msg.Data[1:])
				if err != nil {
					return fmt.Errorf("xlog: %w", err)
				}
				txn, err := dec.decode(xld.WALData)
				if err != nil {
					return err
				}
				if txn == nil {
					continue // mid-transaction
				}
				if err := s.deliver(ctx, txn); err != nil {
					return err
				}
				confirmed = txn.endLSN
				s.recordApplied(confirmed, len(txn.events))
				// Confirm promptly: the slot holds WAL until we do.
				nextStatus = time.Time{}
			}
		}
	}
}

// deliver hands a transaction to the handler, retrying until it succeeds or
// ctx ends. Retrying in place rather than reconnecting keeps the position: the
// stream does not move on past a transaction that is not yet applied.
func (s *Stream) deliver(ctx context.Context, txn *transaction) error {
	if len(txn.events) == 0 {
		return nil
	}
	backoff := 500 * time.Millisecond
	for attempt := 1; ; attempt++ {
		err := s.handler(ctx, txn.events)
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		s.logger.ErrorContext(ctx, "applying events failed; retrying",
			slog.Any("error", err), slog.Int("attempt", attempt),
			slog.String("lsn", txn.endLSN.String()), slog.Int("events", len(txn.events)))
		// Past a few attempts, drop the connection: Postgres times out a
		// replication client that stops answering, and reconnecting resends
		// this transaction anyway.
		if attempt >= 5 {
			return fmt.Errorf("applying transaction at %s: %w", txn.endLSN, err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		backoff *= 2
	}
}

// ensureSlot creates the replication slot if it does not exist yet.
func (s *Stream) ensureSlot(ctx context.Context, conn *pgconn.PgConn) error {
	_, err := pglogrepl.CreateReplicationSlot(ctx, conn, s.cfg.SlotName, "pgoutput",
		pglogrepl.CreateReplicationSlotOptions{})
	if err == nil {
		s.logger.InfoContext(ctx, "replication slot created", slog.String("slot", s.cfg.SlotName))
		return nil
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "42710" { // duplicate_object
		return nil
	}
	return fmt.Errorf("creating slot %s: %w", s.cfg.SlotName, err)
}

func sendStatus(ctx context.Context, conn *pgconn.PgConn, lsn pglogrepl.LSN) error {
	if err := pglogrepl.SendStandbyStatusUpdate(ctx, conn, pglogrepl.StandbyStatusUpdate{
		WALWritePosition: lsn, WALFlushPosition: lsn, WALApplyPosition: lsn,
	}); err != nil {
		return fmt.Errorf("sending status: %w", err)
	}
	return nil
}

// transaction is one decoded source transaction.
type transaction struct {
	endLSN pglogrepl.LSN
	events []projector.Event
}

// decoder assembles pgoutput messages into transactions.
type decoder struct {
	relations map[uint32]*pglogrepl.RelationMessage
	types     *pgtype.Map
	current   *transaction
}

func newDecoder() *decoder {
	return &decoder{relations: map[uint32]*pglogrepl.RelationMessage{}, types: pgtype.NewMap()}
}

// decode consumes one WAL message and returns a transaction when its COMMIT
// arrives, nil otherwise.
func (d *decoder) decode(wal []byte) (*transaction, error) {
	msg, err := pglogrepl.Parse(wal)
	if err != nil {
		return nil, fmt.Errorf("parsing pgoutput: %w", err)
	}
	switch m := msg.(type) {
	case *pglogrepl.RelationMessage:
		d.relations[m.RelationID] = m
	case *pglogrepl.BeginMessage:
		d.current = &transaction{}
	case *pglogrepl.InsertMessage:
		if d.current == nil {
			return nil, errors.New("insert outside a transaction")
		}
		rel, ok := d.relations[m.RelationID]
		if !ok {
			return nil, fmt.Errorf("insert for unknown relation %d", m.RelationID)
		}
		ev, err := d.event(rel, m.Tuple)
		if err != nil {
			return nil, err
		}
		d.current.events = append(d.current.events, ev)
	case *pglogrepl.CommitMessage:
		if d.current == nil {
			return nil, errors.New("commit outside a transaction")
		}
		txn := d.current
		d.current = nil
		txn.endLSN = m.TransactionEndLSN
		for i := range txn.events {
			txn.events[i].LSN = m.CommitLSN.String()
		}
		return txn, nil
	}
	// Updates, deletes and truncates are not published (publish = 'insert').
	return nil, nil
}

// event reads an outbox row out of an INSERT tuple.
func (d *decoder) event(rel *pglogrepl.RelationMessage, tuple *pglogrepl.TupleData) (projector.Event, error) {
	ev := projector.Event{Table: rel.Namespace + "." + rel.RelationName}
	if tuple == nil {
		return ev, errors.New("insert without a tuple")
	}
	for i, col := range tuple.Columns {
		if i >= len(rel.Columns) || col.DataType != pglogrepl.TupleDataTypeText {
			continue // NULL or unchanged-toast: none of the columns we need
		}
		name, oid, data := rel.Columns[i].Name, rel.Columns[i].DataType, col.Data
		var err error
		switch name {
		case "id":
			ev.ID, err = uuid.ParseBytes(data)
		case "aggregate_id":
			ev.AggregateID, err = uuid.ParseBytes(data)
		case "event_type":
			ev.EventType = string(data)
		case "actor_role":
			ev.ActorRole = string(data)
		case "schema_version":
			var v int16
			err = d.types.Scan(oid, pgtype.TextFormatCode, data, &v)
			ev.SchemaVersion = int(v)
		case "occurred_at":
			err = d.types.Scan(oid, pgtype.TextFormatCode, data, &ev.OccurredAt)
		case "payload":
			// jsonb's text form IS the JSON document.
			ev.Payload = append([]byte(nil), data...)
		}
		if err != nil {
			return ev, fmt.Errorf("%s.%s: %w", ev.Table, name, err)
		}
	}
	if ev.ID == uuid.Nil {
		return ev, fmt.Errorf("%s: row without an id", ev.Table)
	}
	return ev, nil
}
