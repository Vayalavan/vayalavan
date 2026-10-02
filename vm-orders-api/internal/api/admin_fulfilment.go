package api

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vayal-mikrogreenz/vm-go-common/httpx"
	"github.com/vayal-mikrogreenz/vm-go-common/isttime"
	"github.com/vayal-mikrogreenz/vm-go-common/money"

	"github.com/vayal-mikrogreenz/vm-orders-api/internal/analyticsevents"
	"github.com/vayal-mikrogreenz/vm-orders-api/internal/store"
)

// maxBulkOrders bounds one bulk action.
//
// Not a database limit — the UPDATE is one statement — but a blast-radius
// limit: a mis-clicked "select all" on a very large page should be refused
// rather than quietly moving thousands of orders in one audit entry.
const maxBulkOrders = 500

// ---------------------------------------------------------------------------
// POST /admin/orders/{id}/process
// ---------------------------------------------------------------------------

// AdminProcessOrder moves one paid order to processed.
//
// Processing is the step between payment and handing a parcel to a courier:
// the produce is picked and packed. It normally happens by itself at the 4pm
// IST cutoff (see the processor job), and this is the manual override for an
// admin who has packed early.
func (a *API) AdminProcessOrder(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	adminID, err := adminFromRequest(r)
	if err != nil {
		a.fail(ctx, w, err)
		return
	}
	orderID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		a.fail(ctx, w, httpx.NotFound("Order not found."))
		return
	}

	tx, err := a.pool.Begin(ctx)
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit
	q := a.queries.WithTx(tx)

	before, err := q.GetOrderByID(ctx, orderID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			a.fail(ctx, w, httpx.NotFound("Order not found."))
			return
		}
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	after, err := q.AdminMarkOrderProcessed(ctx, orderID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			a.fail(ctx, w, httpx.Conflict("INVALID_TRANSITION",
				"Only a paid order can be processed. This one is "+before.Status+"."))
			return
		}
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	if err := a.writeAudit(ctx, q, adminID, actionOrderProcessed, "order", orderID,
		map[string]any{"status": before.Status},
		map[string]any{"status": after.Status, "order_number": after.OrderNumber,
			"via": "admin"}); err != nil {
		a.fail(ctx, w, err)
		return
	}
	if err := analyticsevents.Emit(ctx, q, orderID, analyticsevents.OrderProcessed,
		analyticsevents.ActorAdmin, time.Now(), analyticsevents.Options{}); err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}
	if err := tx.Commit(ctx); err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	items, _ := a.queries.ListOrderItems(ctx, after.ID)
	a.respond(ctx, w, http.StatusOK, a.toOrderResponse(after, items, time.Now()))
}

// ---------------------------------------------------------------------------
// Bulk transitions
// ---------------------------------------------------------------------------

type bulkOrderRequest struct {
	OrderIDs []string `json:"order_ids"`
	// All processes/dispatches every eligible order in the period instead of
	// a selection. Mutually exclusive with OrderIDs.
	All bool `json:"all"`
}

// AdminBulkProcess moves selected — or all — paid orders to processed.
func (a *API) AdminBulkProcess(w http.ResponseWriter, r *http.Request) {
	a.bulkTransition(w, r, bulkSpec{
		Action:     actionOrderProcessed,
		Event:      analyticsevents.OrderProcessed,
		FromStatus: "paid",
		ToStatus:   "processed",
		Verb:       "processed",
		Selected: func(ctx context.Context, q *store.Queries, ids []uuid.UUID) ([]store.Order, error) {
			return q.AdminBulkMarkProcessed(ctx, ids)
		},
		Everything: func(ctx context.Context, q *store.Queries, from, to *time.Time) ([]store.Order, error) {
			return q.AdminProcessAllPaid(ctx, store.AdminProcessAllPaidParams{
				PlacedFrom: from, PlacedTo: to,
			})
		},
	})
}

// AdminBulkDispatch moves selected — or all — processed orders to dispatched.
//
// Dispatch is always manual: it means a parcel physically left with a courier,
// and nothing in software can observe that.
func (a *API) AdminBulkDispatch(w http.ResponseWriter, r *http.Request) {
	a.bulkTransition(w, r, bulkSpec{
		Action:     actionOrderDispatched,
		Event:      analyticsevents.OrderDispatched,
		FromStatus: "processed",
		ToStatus:   "dispatched",
		Verb:       "dispatched",
		Selected: func(ctx context.Context, q *store.Queries, ids []uuid.UUID) ([]store.Order, error) {
			return q.AdminBulkMarkDispatched(ctx, ids)
		},
		Everything: func(ctx context.Context, q *store.Queries, from, to *time.Time) ([]store.Order, error) {
			return q.AdminDispatchAllProcessed(ctx, store.AdminDispatchAllProcessedParams{
				PlacedFrom: from, PlacedTo: to,
			})
		},
	})
}

type bulkSpec struct {
	Action string
	// The analytics event each moved order emits (CLAUDE.md §5.4).
	Event      string
	FromStatus string
	ToStatus   string
	Verb       string
	Selected   func(context.Context, *store.Queries, []uuid.UUID) ([]store.Order, error)
	Everything func(context.Context, *store.Queries, *time.Time, *time.Time) ([]store.Order, error)
}

// bulkTransition runs one bulk state change, in a single transaction.
//
// The count returned is what ACTUALLY moved, not what was asked for. Orders in
// the wrong state are filtered out by the UPDATE's own WHERE clause rather
// than rejected up front, because a selection can go stale between rendering a
// page and clicking the button — another admin may have processed one in the
// meantime, and failing the whole batch for that would be worse than moving
// the rest and saying so.
func (a *API) bulkTransition(w http.ResponseWriter, r *http.Request, spec bulkSpec) {
	ctx := r.Context()

	adminID, err := adminFromRequest(r)
	if err != nil {
		a.fail(ctx, w, err)
		return
	}

	var req bulkOrderRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		a.fail(ctx, w, err)
		return
	}

	var ids []uuid.UUID
	if !req.All {
		if len(req.OrderIDs) == 0 {
			a.fail(ctx, w, httpx.Validation("Select at least one order.",
				map[string]any{"field": "order_ids"}))
			return
		}
		if len(req.OrderIDs) > maxBulkOrders {
			a.fail(ctx, w, httpx.Validation(
				"Too many orders in one action. Select "+
					strconv.Itoa(maxBulkOrders)+" or fewer.",
				map[string]any{"field": "order_ids", "max": maxBulkOrders}))
			return
		}
		ids = make([]uuid.UUID, 0, len(req.OrderIDs))
		for _, raw := range req.OrderIDs {
			parsed, parseErr := uuid.Parse(raw)
			if parseErr != nil {
				a.fail(ctx, w, httpx.Validation("An order id is not valid.",
					map[string]any{"field": "order_ids", "value": raw}))
				return
			}
			ids = append(ids, parsed)
		}
	}

	// The period is resolved ONLY for the "all" path.
	//
	// A selection carries its own order ids and has nothing to do with what
	// period is on screen. Resolving the range for it anyway meant that
	// choosing All time — which this screen now defaults to — made "Process
	// selected" fail with "range must be one of: today, 2d, 3d…", an error
	// about a parameter the action does not use.
	var placedFrom, placedTo *time.Time
	// What the audit row and the response call this batch. A selection is
	// scoped by its ids, so the period does not name it.
	periodLabel := "selection"
	if req.All {
		// Scoped to the period on screen: an admin looking at today who clicks
		// "process all" means today's orders. All time is allowed and means
		// exactly what it says — every eligible order — which is why the
		// screen names the period in the button's confirmation.
		period, err := resolveQueueRange(r)
		if err != nil {
			a.fail(ctx, w, err)
			return
		}
		periodLabel = period.Label
		if !period.All {
			from, to := istDayBounds(period.From, period.To)
			placedFrom, placedTo = &from, &to
		}
	}

	tx, err := a.pool.Begin(ctx)
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit
	q := a.queries.WithTx(tx)

	var moved []store.Order
	if req.All {
		moved, err = spec.Everything(ctx, q, placedFrom, placedTo)
	} else {
		moved, err = spec.Selected(ctx, q, ids)
	}
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	numbers := make([]string, 0, len(moved))
	now := time.Now()
	for _, order := range moved {
		numbers = append(numbers, order.OrderNumber)
		// One event per order that actually moved, unlike the audit row: each
		// is a separate order changing state, and analytics counts orders.
		if err := analyticsevents.Emit(ctx, q, order.ID, spec.Event,
			analyticsevents.ActorAdmin, now, analyticsevents.Options{}); err != nil {
			a.fail(ctx, w, httpx.Internal(err))
			return
		}
	}

	// One audit row for the batch, naming every order in it. Per-order rows
	// would bury a genuine single-order decision under a hundred bulk entries.
	if len(moved) > 0 {
		if err := a.writeAudit(ctx, q, adminID, spec.Action, "order", uuid.Nil,
			map[string]any{"status": spec.FromStatus},
			map[string]any{
				"status": spec.ToStatus, "count": len(moved),
				"order_numbers": numbers, "scope": scopeLabel(req.All, periodLabel),
			}); err != nil {
			a.fail(ctx, w, err)
			return
		}
	}

	if err := tx.Commit(ctx); err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	requested := len(req.OrderIDs)
	if req.All {
		requested = len(moved)
	}

	a.logger.InfoContext(ctx, "bulk order transition",
		slog.String("to", spec.ToStatus), slog.Int("moved", len(moved)),
		slog.Int("requested", requested), slog.String("scope", scopeLabel(req.All, periodLabel)))

	a.respond(ctx, w, http.StatusOK, map[string]any{
		spec.Verb:       len(moved),
		"requested":     requested,
		"order_numbers": numbers,
		"scope":         scopeLabel(req.All, periodLabel),
		// Named explicitly so the UI can say "3 were already handled by
		// someone else" rather than silently reporting a smaller number.
		"skipped": requested - len(moved),
	})
}

func scopeLabel(all bool, period string) string {
	if all {
		return "all eligible in " + period
	}
	return "selected"
}

// istDayBounds turns two IST calendar days into a half-open instant range.
//
// From the START of the first day to the START of the day AFTER the last, so
// the range is inclusive of both days without the off-by-one that comparing
// against the last day's midnight would cause — an order placed at 3pm on the
// final day must be included.
func istDayBounds(fromDay, toDay time.Time) (time.Time, time.Time) {
	return isttime.StartOfDayIST(fromDay), isttime.StartOfDayIST(isttime.AddDays(toDay, 1))
}

// ---------------------------------------------------------------------------
// GET /admin/orders/export.csv
// ---------------------------------------------------------------------------

// AdminExportOrders produces the courier handover sheet.
//
// One row per order with everything needed to move a parcel: recipient, phone,
// full address, what is inside and how much it weighs. Written straight to the
// response rather than buffered, so a large day does not build the whole file
// in memory first.
func (a *API) AdminExportOrders(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	adminID, err := adminFromRequest(r)
	if err != nil {
		a.fail(ctx, w, err)
		return
	}

	// The export follows the list, all time included: a courier sheet for
	// everything outstanding is a reasonable thing to ask for, and the file is
	// read by a person rather than acted on in bulk.
	period, err := resolveQueueRange(r)
	if err != nil {
		a.fail(ctx, w, err)
		return
	}
	var placedFrom, placedTo *time.Time
	if !period.All {
		from, to := istDayBounds(period.From, period.To)
		placedFrom, placedTo = &from, &to
	}

	statuses := []string{"processed"}
	if raw := r.URL.Query().Get("status"); raw == "dispatched" {
		statuses = []string{"dispatched"}
	} else if raw == "all" {
		statuses = []string{"processed", "dispatched"}
	}

	rows, err := a.queries.AdminOrdersForExport(ctx, store.AdminOrdersForExportParams{
		Column1: statuses, PlacedFrom: placedFrom, PlacedTo: placedTo,
	})
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	// The export carries customers' names, phone numbers and home addresses.
	// That is the point of it, and it is also exactly why every download is
	// recorded against the admin who took it.
	a.logger.InfoContext(ctx, "courier sheet exported",
		slog.String("admin_user_id", adminID.String()),
		slog.Int("rows", len(rows)), slog.String("period", period.Label))

	filename := "vayal-courier-all-time.csv"
	if !period.All {
		filename = "vayal-courier-" + period.From.Format("20060102") +
			"-to-" + period.To.Format("20060102") + ".csv"
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	// Never cached: it contains personal data and it changes as orders move.
	w.Header().Set("Cache-Control", "no-store")

	WriteCourierCSV(w, rows)
}

// WriteCourierCSV is the courier sheet, in one place.
//
// Shared by the admin download and the emailed daily report: two writers would
// mean the sheet a courier is handed and the sheet in the inbox could disagree
// about a column, and nobody would notice until a parcel went to the wrong
// address.
func WriteCourierCSV(w io.Writer, rows []store.AdminOrdersForExportRow) {
	cw := csv.NewWriter(w)
	defer cw.Flush()

	_ = cw.Write([]string{
		"order_number", "status", "placed_at_ist", "delivery_day",
		"expected_delivery", "recipient_name", "phone", "address_line1",
		"address_line2", "landmark", "city", "state", "pincode",
		"items", "packs", "total_weight_grams", "order_total",
	})

	for _, row := range rows {
		addr := decodeAddress(row.AddressSnapshot)
		_ = cw.Write([]string{
			row.OrderNumber,
			row.Status,
			row.PlacedAt.In(isttime.Location()).Format("2006-01-02 15:04"),
			isttime.FormatDate(row.DeliveryDay),
			isttime.FormatDate(row.ExpectedDeliveryDate),
			addr["recipient_name"],
			addr["phone"],
			addr["line1"],
			addr["line2"],
			addr["landmark"],
			addr["city"],
			addr["state"],
			addr["pincode"],
			row.Contents,
			strconv.FormatInt(row.ItemCount, 10),
			strconv.FormatInt(row.TotalGrams, 10),
			money.FormatRupees(money.Paise(row.TotalPaise)),
		})
	}
}

// decodeAddress reads the JSONB snapshot into plain strings.
//
// Missing keys come back empty rather than as "<nil>", because this lands in a
// spreadsheet a courier reads — a blank landmark column is fine, the literal
// text "<nil>" in an address is not.
func decodeAddress(raw []byte) map[string]string {
	out := map[string]string{}
	if len(raw) == 0 {
		return out
	}
	var parsed map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return out
	}
	for key, value := range parsed {
		if str, ok := value.(string); ok {
			out[key] = str
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// The 4pm processor
// ---------------------------------------------------------------------------

// processorBatch bounds one tick's work.
const processorBatch = 500

// processorLockKey is this job's fixed advisory-lock id, distinct from the
// reservation sweeper's.
const processorLockKey int64 = 8_231_004_772

// Processor advances orders through the fulfilment states a CLOCK can decide.
//
// Two transitions, both fixed at placement by CLAUDE.md §6.1:
//
//	paid      -> processed   when the order's own processing_at arrives (4pm
//	                         IST on its processing date)
//	processed -> dispatched  on its delivery day, from DispatchHourIST onwards
//
// The second exists because dispatch was manual and nothing else moved it: an
// order sat at "Being prepared" on the customer's screen days after it was
// handed to a courier, because handing it over is a physical act nobody
// recorded. A clock cannot observe that handover — it asserts the schedule we
// actually run to, and an admin can still dispatch earlier by hand, which is
// what the fulfilment queue is for.
//
// Which is also why the hour is configurable. Dispatching at midnight tells a
// customer their food is on its way while the box is still on our floor; set
// DispatchHourIST to when the courier actually collects and the claim becomes
// true when it is made.
//
// Written as a poll rather than a job that fires once at 16:00, deliberately.
// A single daily trigger misses every order if the service happens to be
// restarting at that minute, and there is no way to notice until a customer
// asks where their food is. Polling is self-healing: whatever became due while
// the process was down is picked up on the next tick.
type Processor struct {
	pool     *pgxpool.Pool
	queries  *store.Queries
	interval time.Duration
	// dispatchHour is the hour, in IST, from which a delivery day's orders may
	// be marked dispatched. 0 means as soon as the day begins.
	dispatchHour int
	// report is nil when no reporting address is configured, which is a normal
	// deployment rather than a broken one.
	report *DailyReport
	logger *slog.Logger
}

// ProcessorConfig is the job's timing. A struct rather than three positional
// arguments, because "60, 9" at a call site says nothing about which is which.
type ProcessorConfig struct {
	Interval     time.Duration
	DispatchHour int
}

func NewProcessor(
	pool *pgxpool.Pool, cfg ProcessorConfig, report *DailyReport, logger *slog.Logger,
) *Processor {
	return &Processor{
		pool: pool, queries: store.New(pool), interval: cfg.Interval,
		dispatchHour: cfg.DispatchHour, report: report,
		logger: logger.With(slog.String("job", "order-processor")),
	}
}

// Run blocks until ctx is cancelled.
func (p *Processor) Run(ctx context.Context) {
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()

	p.logger.InfoContext(ctx, "order processor started",
		slog.Duration("interval", p.interval))

	for {
		select {
		case <-ctx.Done():
			p.logger.InfoContext(ctx, "order processor stopped")
			return
		case <-ticker.C:
			p.tick(ctx)
		}
	}
}

func (p *Processor) tick(ctx context.Context) {
	conn, err := p.pool.Acquire(ctx)
	if err != nil {
		p.logger.ErrorContext(ctx, "acquiring a connection", slog.Any("error", err))
		return
	}
	defer conn.Release()

	// Only one replica does the work per tick. Without this, two instances
	// would race on the same orders — harmless thanks to the status filter,
	// but it would double the audit noise and the wasted queries.
	var locked bool
	if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", processorLockKey).
		Scan(&locked); err != nil {
		p.logger.ErrorContext(ctx, "taking the advisory lock", slog.Any("error", err))
		return
	}
	if !locked {
		return
	}
	defer func() {
		if _, err := conn.Exec(context.WithoutCancel(ctx),
			"SELECT pg_advisory_unlock($1)", processorLockKey); err != nil {
			p.logger.ErrorContext(ctx, "releasing the advisory lock", slog.Any("error", err))
		}
	}()

	processed, err := p.claimWithEvents(ctx, conn, analyticsevents.OrderProcessed,
		func(q *store.Queries) ([]store.Order, error) {
			return q.ClaimOrdersDueForProcessing(ctx, processorBatch)
		})
	if err != nil {
		p.logger.ErrorContext(ctx, "processing due orders", slog.Any("error", err))
		// Not a return: the two transitions are independent, and a failure to
		// process today's orders is no reason to leave yesterday's undelivered
		// on the customer's screen.
	} else if len(processed) > 0 {
		p.logger.InfoContext(ctx, "orders processed automatically",
			slog.Int("count", len(processed)),
			slog.Any("order_numbers", orderNumbers(processed)))
	}

	// Today in IST, decided here rather than in SQL, so there is one definition
	// of the business day and it is the one isttime's tests cover.
	if dispatchDue(time.Now(), p.dispatchHour) {
		dispatched, err := p.claimWithEvents(ctx, conn, analyticsevents.OrderDispatched,
			func(q *store.Queries) ([]store.Order, error) {
				return q.ClaimOrdersDueForDispatch(ctx, store.ClaimOrdersDueForDispatchParams{
					Limit: processorBatch, Column2: isttime.Today(),
				})
			})
		if err != nil {
			p.logger.ErrorContext(ctx, "dispatching due orders", slog.Any("error", err))
			return
		}
		if len(dispatched) > 0 {
			p.logger.InfoContext(ctx, "orders dispatched automatically",
				slog.Int("count", len(dispatched)),
				slog.Any("order_numbers", orderNumbers(dispatched)))
		}
	}

	// Last, and on the same tick: the day's courier sheet goes out once the
	// 4pm run has decided what is in it. Guarded by its own row, so this is a
	// no-op on every tick but the first.
	p.report.Run(ctx, time.Now())
}

// claimWithEvents runs one automatic transition and writes an analytics event
// for every order it moved, in one transaction — so an order can never change
// state without analytics hearing of it, nor be reported moved when it was not.
func (p *Processor) claimWithEvents(
	ctx context.Context, conn *pgxpool.Conn, eventType string,
	claim func(*store.Queries) ([]store.Order, error),
) ([]store.Order, error) {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit
	q := store.New(tx)

	moved, err := claim(q)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	for _, order := range moved {
		if err := analyticsevents.Emit(ctx, q, order.ID, eventType,
			analyticsevents.ActorSystem, now, analyticsevents.Options{}); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return moved, nil
}

// dispatchDue reports whether the automatic dispatch may run now.
//
// The hour is read in IST and never in the server's own zone (CLAUDE.md
// rule 2): a container on UTC would otherwise dispatch five and a half hours
// early, which is a customer being told their food is on its way while it is
// still on a shelf.
//
// An hour of 0 — the default — makes this always true, so dispatch happens as
// soon as the delivery day begins. Anything later delays orders whose delivery
// day has ALREADY passed too, by design: they go out with the same morning
// courier run as today's, not at 3am.
func dispatchDue(now time.Time, hour int) bool {
	return now.In(isttime.Location()).Hour() >= hour
}

// orderNumbers is what a log line should carry about a batch: the human
// references someone can search for, not opaque ids.
func orderNumbers(orders []store.Order) []string {
	numbers := make([]string, 0, len(orders))
	for _, order := range orders {
		numbers = append(numbers, order.OrderNumber)
	}
	return numbers
}
