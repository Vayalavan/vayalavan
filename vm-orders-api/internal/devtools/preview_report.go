// Inspects the daily courier report, and — only with -send — mails it.
//
//	go run ./internal/devtools [YYYY-MM-DD]         # dry run, sends nothing
//	go run ./internal/devtools [YYYY-MM-DD] -send   # actually emails it
//	go run ./internal/devtools -outbox              # run ONE outbox tick
//	go run ./internal/devtools -smtp-check          # one test mail via SMTP_*
//	go run ./internal/devtools -suppress-outbox     # count the unsent backlog
//	go run ./internal/devtools -suppress-outbox -yes  # mark it published, unsent
//	go run ./internal/devtools -emit-snapshots    # backfill analytics: one order.snapshot per order
//
// A dev tool, not part of the service: the scheduled job needs none of this.
// It exists so a report can be checked against real data without waiting for
// tomorrow's 4pm run, and it goes through the SAME job the scheduler calls, so
// a successful test proves the real path rather than a copy of it.
package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vayal-mikrogreenz/vm-go-common/config"
	"github.com/vayal-mikrogreenz/vm-go-common/isttime"
	"github.com/vayal-mikrogreenz/vm-go-common/logging"

	"github.com/vayal-mikrogreenz/vm-orders-api/internal/api"
	"github.com/vayal-mikrogreenz/vm-orders-api/internal/app"
	"github.com/vayal-mikrogreenz/vm-orders-api/internal/mail"
	"github.com/vayal-mikrogreenz/vm-orders-api/internal/store"
)

func main() {
	if err := config.LoadRootDotEnv(); err != nil {
		fmt.Println("no .env:", err)
		return
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, os.Getenv("ORDERS_DATABASE_URL"))
	if err != nil {
		fmt.Println("connect:", err)
		return
	}
	defer pool.Close()

	if len(os.Args) > 2 && os.Args[1] == "-order" {
		var status string
		var customerID uuid.UUID
		if err := pool.QueryRow(ctx,
			`SELECT status, customer_id FROM orders WHERE id = $1`, os.Args[2],
		).Scan(&status, &customerID); err != nil {
			fmt.Println("lookup:", err)
			return
		}
		fmt.Printf("status=%s customer_id=%s\n", status, customerID)
		return
	}

	if len(os.Args) > 1 && os.Args[1] == "-suppress-outbox" {
		suppressBacklog(ctx, pool, len(os.Args) > 2 && os.Args[2] == "-yes")
		return
	}

	if len(os.Args) > 1 && os.Args[1] == "-smtp-check" {
		smtpCheck(ctx)
		return
	}

	if len(os.Args) > 1 && os.Args[1] == "-emit-snapshots" {
		emitSnapshots(ctx, pool)
		return
	}

	if len(os.Args) > 1 && os.Args[1] == "-outbox" {
		runOutboxTick(ctx, pool)
		return
	}

	// A date may be passed in; defaults to today.
	today := isttime.Today()
	if len(os.Args) > 1 {
		parsed, err := isttime.ParseISODate(os.Args[1])
		if err != nil {
			fmt.Println("bad date:", err)
			return
		}
		today = parsed
	}

	summary, err := pool.Query(ctx, `
		SELECT (processing_at AT TIME ZONE 'Asia/Kolkata')::date AS day,
		       status, count(*)
		FROM orders
		WHERE status IN ('processed','dispatched')
		GROUP BY 1, 2 ORDER BY 1 DESC, 2`)
	if err == nil {
		fmt.Println("processed/dispatched orders by processing day (IST):")
		for summary.Next() {
			var day time.Time
			var status string
			var n int64
			if err := summary.Scan(&day, &status, &n); err == nil {
				fmt.Printf("  %s  %-12s %d\n", isttime.FormatISODate(day), status, n)
			}
		}
		summary.Close()
		fmt.Println()
	}
	rows, err := store.New(pool).AdminOrdersForExport(ctx, store.AdminOrdersForExportParams{
		Column1: []string{"processed"},
	})
	if err != nil {
		fmt.Println("query:", err)
		return
	}

	fmt.Printf("now (IST):   %s\n", time.Now().In(isttime.Location()).Format("2006-01-02 15:04"))
	fmt.Printf("report date: %s\n", isttime.FormatISODate(today))
	fmt.Printf("rows:        %d\n\n", len(rows))

	var csv bytes.Buffer
	api.WriteCourierCSV(&csv, rows)
	lines := bytes.SplitN(csv.Bytes(), []byte("\n"), 6)
	for i, line := range lines {
		if i == 5 {
			fmt.Println("...")
			break
		}
		fmt.Println(string(line))
	}

	fmt.Printf("\nrecipients:  %s\n", os.Getenv("MAIL_TO"))
	fmt.Printf("from:        %s\n", os.Getenv("MAIL_FROM"))
	fmt.Printf("subject:     %s — %s\n", os.Getenv("MAIL_SUBJECT"), isttime.FormatISODate(today))
	fmt.Printf("csv bytes:   %d\n", csv.Len())

	var claimed *time.Time
	_ = pool.QueryRow(ctx,
		`SELECT sent_at FROM daily_report_sends WHERE report_date = $1`, today).Scan(&claimed)
	if claimed == nil {
		fmt.Println("already sent: no")
	} else {
		fmt.Println("already sent:", claimed.In(isttime.Location()).Format("15:04"))
	}

	if len(os.Args) > 2 && os.Args[2] == "-send" {
		fmt.Println("\nsending…")
		send(ctx, pool, today)
	}
}

// send runs the real job for one day.
//
// Configuration comes from app.LoadConfig, the same loader the service uses,
// rather than from os.Getenv here — reading the environment a second way is
// how this tool first tried to dial port 587 on an empty host while the
// service itself would have defaulted to smtp.gmail.com.
func send(ctx context.Context, pool *pgxpool.Pool, day time.Time) {
	cfg, err := app.LoadConfig()
	if err != nil {
		fmt.Println("config:", err)
		return
	}
	logger := logging.New("vm-orders-api-devtool", "info")

	report := api.NewDailyReport(store.New(pool), mail.NewSMTPSender(mail.Config{
		Host:     cfg.Report.Host,
		Port:     cfg.Report.Port,
		Username: cfg.Report.From,
		Password: cfg.Report.Password,
		From:     cfg.Report.From,
	}, logger), api.DailyReportConfig{
		Recipients: cfg.Report.To,
		Subject:    cfg.Report.Subject,
		CutoffHour: cfg.Fulfilment.CutoffHourIST,
	}, logger)

	if report == nil {
		fmt.Println("not configured: MAIL_TO and MAIL_FROM must both be set")
		return
	}
	report.SendFor(ctx, day)
}

// runOutboxTick drains one batch of transactional email.
//
// One tick, not a loop: enough to prove the path — claim, look the customer up
// in vm-profile-api, render, send — without draining a backlog in the
// background while someone is reading the output.
func runOutboxTick(ctx context.Context, pool *pgxpool.Pool) {
	cfg, err := app.LoadConfig()
	if err != nil {
		fmt.Println("config:", err)
		return
	}
	logger := logging.New("vm-orders-api-devtool", "info")

	lookups := api.New(api.Deps{
		Pool:          pool,
		ProfileAPIURL: cfg.ProfileAPIURL,
		InternalToken: cfg.InternalToken,
		SupportEmail:  cfg.SupportEmail,
		Logger:        logger,
	})
	sender := mail.NewSMTPSender(mail.Config{
		Host:     cfg.SMTP.Host,
		Port:     cfg.SMTP.Port,
		Username: cfg.SMTP.User,
		Password: cfg.SMTP.Password,
		From:     cfg.SMTP.From,
	}, logger)

	fmt.Printf("outbox tick: smtp %s:%d, from %s\n",
		cfg.SMTP.Host, cfg.SMTP.Port, cfg.SMTP.From)
	api.NewOutboxDispatcher(pool, lookups, sender, time.Minute, cfg.SupportEmail, logger).
		Tick(ctx)
}

// suppressBacklog marks the pre-dispatcher outbox rows published, unsent.
//
// Counts first and does nothing without -yes, because this is not reversible:
// a row marked published is a customer who will never be emailed about that
// order. That is the intent here — the backlog predates the dispatcher and is
// all about orders long since finished — but it should take a deliberate
// second argument to do it.
func suppressBacklog(ctx context.Context, pool *pgxpool.Pool, apply bool) {
	queries := store.New(pool)

	// One instant, used for both the count and the update, so the number
	// printed is exactly the number changed and nothing written in between is
	// caught by it.
	cutoff := time.Now()

	pending, err := queries.CountUnsentOutbox(ctx, cutoff)
	if err != nil {
		fmt.Println("counting:", err)
		return
	}
	fmt.Printf("unsent outbox rows written before %s: %d\n",
		cutoff.Format(time.RFC3339), pending)

	var total, published, suppressedAlready int64
	_ = pool.QueryRow(ctx, `
		SELECT count(*),
		       count(*) FILTER (WHERE published_at IS NOT NULL),
		       count(*) FILTER (WHERE last_error LIKE 'suppressed:%')
		FROM outbox`).Scan(&total, &published, &suppressedAlready)
	fmt.Printf("outbox totals: %d rows, %d published (%d of them suppressed unsent)\n",
		total, published, suppressedAlready)

	if !apply {
		fmt.Println("\nnothing changed. re-run with -yes to mark these published without sending.")
		return
	}
	if pending == 0 {
		return
	}

	suppressed, err := queries.SuppressOutboxBacklog(ctx, cutoff)
	if err != nil {
		fmt.Println("suppressing:", err)
		return
	}
	fmt.Printf("marked %d rows published without sending\n", suppressed)
}

// smtpCheck sends one message through the TRANSACTIONAL relay.
//
// Proves the SMTP_* settings and the envelope handling against the real server
// without waiting for a customer to place an order. Addressed to MAIL_TO — the
// team — never to a customer.
func smtpCheck(ctx context.Context) {
	cfg, err := app.LoadConfig()
	if err != nil {
		fmt.Println("config:", err)
		return
	}
	logger := logging.New("vm-orders-api-devtool", "info")

	fmt.Printf("relay %s:%d as %s, from %q\n",
		cfg.SMTP.Host, cfg.SMTP.Port, cfg.SMTP.User, cfg.SMTP.From)

	sender := mail.NewSMTPSender(mail.Config{
		Host: cfg.SMTP.Host, Port: cfg.SMTP.Port,
		Username: cfg.SMTP.User, Password: cfg.SMTP.Password, From: cfg.SMTP.From,
	}, logger)

	if err := sender.Send(ctx, mail.Message{
		To:      cfg.Report.To,
		Subject: "Vayal SMTP check",
		Body: "This is a test of the transactional relay (SMTP_*).\r\n\r\n" +
			"If you are reading it in an inbox rather than MailHog, order " +
			"confirmations will now reach real customers.\r\n",
	}); err != nil {
		fmt.Println("FAILED:", err)
		return
	}
	fmt.Println("sent")
}
