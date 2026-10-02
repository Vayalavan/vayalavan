package api

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vayal-mikrogreenz/vm-go-common/isttime"

	"github.com/vayal-mikrogreenz/vm-orders-api/internal/mail"
	"github.com/vayal-mikrogreenz/vm-orders-api/internal/store"
)

// DailyReport emails the day's courier sheet once the 4pm run has happened.
//
// It rides on the processor's tick rather than a cron schedule, for the same
// reason the processing transition does: a job that fires once at 16:00 misses
// the day entirely if the service happens to be restarting that minute, and
// nobody finds out until someone asks where the sheet is. A poll with a
// send-once row in the database is self-healing — whatever was missed goes out
// on the next tick.
//
// The report is the dispatch QUEUE as it stands when the 4pm run finishes:
// every order in `processed`, which is exactly what the admin's fulfilment
// screen shows and exactly what a courier is handed. See ordersFor.
type DailyReport struct {
	queries    *store.Queries
	sender     mail.Sender
	recipients string
	subject    string
	cutoffHour int
	logger     *slog.Logger
}

// DailyReportConfig is what the job needs to run. An empty Recipients disables
// it — a deployment without a reporting address is a normal deployment, not a
// misconfigured one.
type DailyReportConfig struct {
	Recipients string
	Subject    string
	// CutoffHour is ORDER_CUTOFF_HOUR_IST — the same hour that decides each
	// order's processing date. Nothing is emailed before it, because before it
	// the day's packing list is still changing. One setting for both, so
	// moving the cutoff moves the report with it.
	CutoffHour int
}

// NewDailyReport builds the job, or returns nil when it is not configured.
func NewDailyReport(
	queries *store.Queries, sender mail.Sender, cfg DailyReportConfig, logger *slog.Logger,
) *DailyReport {
	if strings.TrimSpace(cfg.Recipients) == "" || sender == nil {
		return nil
	}
	subject := cfg.Subject
	if subject == "" {
		subject = "Vayal Daily Orders Report"
	}
	return &DailyReport{
		queries:    queries,
		sender:     sender,
		recipients: cfg.Recipients,
		subject:    subject,
		cutoffHour: cfg.CutoffHour,
		logger:     logger.With(slog.String("job", "daily-report")),
	}
}

// Run sends today's report if it is due and has not gone out yet.
//
// Safe to call every tick: the claim row makes the second call a no-op, and
// the whole thing is two cheap queries before the cutoff has even passed.
func (d *DailyReport) Run(ctx context.Context, now time.Time) {
	if d == nil {
		return
	}

	// Nothing to report before the cutoff: the orders being packed tonight are
	// only decided at 16:00, and a sheet sent at noon would be missing exactly
	// the orders that came in during the morning.
	istNow := now.In(isttime.Location())
	if istNow.Hour() < d.cutoffHour {
		return
	}
	// Today only, and no catch-up for a day the service was down — deliberately.
	//
	// A back-fill was written here and removed: the sheet is the dispatch QUEUE
	// as it stands, not a window over a past day (see ordersFor). Anything that
	// missed yesterday's mail is either still waiting, in which case it is on
	// today's sheet already, or it has been dispatched and no longer belongs on
	// any sheet. Re-sending would just be today's list under yesterday's date.
	//
	// So a missed day self-heals through the next day's send rather than
	// through a second email.
	d.SendFor(ctx, isttime.Today())
}

// SendFor sends one day's report, if it has not gone out already.
//
// Exported so the day can be named: the scheduled path always asks for today,
// but a report that failed, or one being checked by hand after a deployment,
// has to be sendable for a specific date without waiting a day to find out
// whether it works.
func (d *DailyReport) SendFor(ctx context.Context, today time.Time) {
	if d == nil {
		return
	}

	rows, err := d.ordersFor(ctx, today)
	if err != nil {
		d.logger.ErrorContext(ctx, "reading today's orders", slog.Any("error", err))
		return
	}
	if len(rows) == 0 {
		// Nothing waiting to go out, so there is nothing to hand a courier.
		// No claim either: an order may still be processed before midnight,
		// and claiming now would suppress the real report when it is.
		return
	}

	// Claimed BEFORE sending, so two replicas cannot both mail the sheet. The
	// send itself happens outside any transaction — an SMTP round trip while
	// holding a pooled connection is how a slow relay drains the pool.
	if _, err := d.queries.ClaimDailyReport(ctx, store.ClaimDailyReportParams{
		ReportDate: today, OrderCount: int32(len(rows)), Recipients: d.recipients,
	}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return // Someone else already sent it. The normal case, every tick.
		}
		d.logger.ErrorContext(ctx, "claiming the day", slog.Any("error", err))
		return
	}

	var csv bytes.Buffer
	WriteCourierCSV(&csv, rows)

	stamp := isttime.FormatISODate(today)
	message := mail.Message{
		To:      d.recipients,
		Subject: fmt.Sprintf("%s — %s", d.subject, stamp),
		Body:    reportBody(today, rows),
		Attachments: []mail.Attachment{{
			Filename:    "vayal-orders-" + strings.ReplaceAll(stamp, "-", "") + ".csv",
			ContentType: "text/csv; charset=utf-8",
			Content:     csv.Bytes(),
		}},
	}

	if err := d.sender.Send(ctx, message); err != nil {
		d.logger.ErrorContext(ctx, "sending the report", slog.Any("error", err))
		// The claim is released so the next tick tries again — a report that
		// failed to send must not look like one that did.
		if _, relErr := d.queries.ReleaseDailyReport(ctx, today); relErr != nil {
			d.logger.ErrorContext(ctx, "releasing the claim after a failed send",
				slog.Any("error", relErr),
				slog.String("alert", "daily_report_stuck"))
		}
		return
	}

	d.logger.InfoContext(ctx, "daily report sent",
		slog.String("report_date", stamp),
		slog.Int("orders", len(rows)),
		slog.String("recipients", d.recipients))
}

// ordersFor reads the sheet's rows: everything currently awaiting dispatch.
//
// A QUEUE, not a time window — and that distinction was got wrong twice before
// it was got right. Windowed on placed_at, an order placed at 6pm yesterday and
// packed today falls off both days' sheets. Windowed on processing_at, an order
// an admin processed early — its scheduled 4pm slot still a day away — is
// missing from tonight's handover even though the box is packed and standing
// there.
//
// What a courier sheet is actually for is "what goes out", so it lists exactly
// what the admin's own dispatch queue lists: every order sitting in `processed`
// at the moment the report is built. Orders already dispatched are off it,
// because they have gone. If nobody dispatches, tomorrow's sheet shows them
// again — which is correct: they are still waiting.
//
// The `day` argument therefore names the report, not a filter: it is which
// day's send this is, and the guard that stops a second one.
func (d *DailyReport) ordersFor(
	ctx context.Context, _ time.Time,
) ([]store.AdminOrdersForExportRow, error) {
	return d.queries.AdminOrdersForExport(ctx, store.AdminOrdersForExportParams{
		Column1: []string{"processed"},
	})
}

// reportBody is the covering note. Short on purpose: the CSV is the report,
// and the mail exists to deliver it and to say what it covers.
func reportBody(day time.Time, rows []store.AdminOrdersForExportRow) string {
	var packs, grams int64
	for _, row := range rows {
		packs += row.ItemCount
		grams += row.TotalGrams
	}

	var body strings.Builder
	fmt.Fprintf(&body, "Vayalavan — orders awaiting dispatch as of %s (IST).\r\n\r\n",
		isttime.FormatDate(day))
	fmt.Fprintf(&body, "Orders: %d\r\n", len(rows))
	fmt.Fprintf(&body, "Packs: %d\r\n", packs)
	fmt.Fprintf(&body, "Total weight: %.1f kg\r\n\r\n", float64(grams)/1000)
	body.WriteString("The attached CSV is the courier sheet: one row per order, " +
		"with the delivery address as it was when the order was placed.\r\n")
	return body.String()
}
