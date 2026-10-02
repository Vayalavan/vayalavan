// Command api is the vm-orders-api entrypoint.
//
// Run the server:      go run ./cmd/api
// Run migrations:      go run ./cmd/api -migrate up
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vayal-mikrogreenz/vm-go-common/config"
	"github.com/vayal-mikrogreenz/vm-go-common/db"
	"github.com/vayal-mikrogreenz/vm-go-common/logging"
	"github.com/vayal-mikrogreenz/vm-go-common/migrate"
	"github.com/vayal-mikrogreenz/vm-go-common/server"

	"github.com/vayal-mikrogreenz/vm-orders-api/internal/api"
	"github.com/vayal-mikrogreenz/vm-orders-api/internal/app"
	"github.com/vayal-mikrogreenz/vm-orders-api/internal/catalogclient"
	"github.com/vayal-mikrogreenz/vm-orders-api/internal/mail"
	"github.com/vayal-mikrogreenz/vm-orders-api/internal/razorpay"
	"github.com/vayal-mikrogreenz/vm-orders-api/internal/store"
	"github.com/vayal-mikrogreenz/vm-orders-api/internal/sweeper"
	"github.com/vayal-mikrogreenz/vm-orders-api/migrations"
)

func main() {
	if err := run(); err != nil {
		// Written to stderr rather than the structured logger: a failure
		// here usually means config never loaded, so no logger exists yet.
		fmt.Fprintf(os.Stderr, "%s: %v\n", app.ServiceName, err)
		os.Exit(1)
	}
}

func run() error {
	migrateCmd := flag.String("migrate", "",
		"run migrations (up|down|status) and exit instead of serving")
	flag.Parse()

	// Local development convenience. Absent in deployed environments, where
	// real env vars are set by the platform — and those always win.
	if err := config.LoadRootDotEnv(); err != nil {
		return err
	}

	cfg, err := app.LoadConfig()
	if err != nil {
		return err
	}

	logger := logging.New(cfg.Base.ServiceName, cfg.Base.LogLevel)
	ctx := context.Background()

	migrationCfg := migrate.Config{
		DatabaseURL: cfg.Database.URL,
		FS:          migrations.FS,
		Dir:         migrations.Dir,
		Schema:      app.Schema,
	}

	// Migration mode: run and exit, without opening a listener.
	if *migrateCmd != "" {
		direction, err := migrate.ParseDirection(*migrateCmd)
		if err != nil {
			return err
		}
		return migrate.Run(ctx, migrationCfg, direction, logger)
	}

	pool, err := db.NewPool(ctx, cfg.Database, logger)
	if err != nil {
		return err
	}
	defer pool.Close()

	// The reservation sweeper runs alongside the server. Safe on every
	// replica: a Postgres advisory lock means only one sweeps per tick
	// (CLAUDE.md §6.3 step 4).
	sweepCtx, stopSweeper := context.WithCancel(ctx)
	defer stopSweeper()
	go sweeper.New(
		pool,
		catalogclient.New(cfg.CatalogAPIURL, cfg.InternalToken),
		razorpay.NewClient(cfg.Razorpay.KeyID, cfg.Razorpay.KeySecret),
		time.Minute,
		logger,
	).WithEventRetention(cfg.Fulfilment.EventRetention).Run(sweepCtx)

	// Moves paid orders to processed once their own 4pm IST cutoff has passed
	// (CLAUDE.md §6.1). Polls rather than firing once a day: a single daily
	// trigger misses everything if the service happens to be restarting at
	// that minute, and nobody notices until a customer asks where their food
	// is. Same advisory-lock discipline as the sweeper.
	//
	// The same tick also dispatches orders whose delivery day has arrived, and
	// emails the day's courier sheet once there is one to send.
	processorCtx, stopProcessor := context.WithCancel(ctx)
	defer stopProcessor()
	go api.NewProcessor(
		pool,
		api.ProcessorConfig{
			Interval:     cfg.Fulfilment.ProcessorInterval,
			DispatchHour: cfg.Fulfilment.DispatchHourIST,
		},
		dailyReport(cfg, pool, logger),
		logger,
	).Run(processorCtx)

	// The other half of the outbox pattern (CLAUDE.md §5.3). Payment writes the
	// confirmation row; this sends it. Without it every customer who paid
	// received nothing, because the rows had no consumer at all.
	// Charges scheduled deliveries from the wallet shortly before each
	// processing day's cutoff — CLAUDE.md §6.7.
	scheduleCtx, stopSchedules := context.WithCancel(ctx)
	defer stopSchedules()
	go api.NewScheduleRunner(
		app.NewAPI(cfg, pool, logger), cfg.Schedules.RunInterval, logger,
	).Run(scheduleCtx)

	outboxCtx, stopOutbox := context.WithCancel(ctx)
	defer stopOutbox()
	go outboxDispatcher(cfg, pool, logger).Run(outboxCtx)

	return server.Run(ctx, cfg.Base, app.NewRouter(cfg, pool, logger), logger)
}

// dailyReport builds the courier-sheet mailer, or nil when it is switched off.
//
// Its own relay rather than the transactional one: SMTP_* points at MailHog in
// development, and a report nobody receives is not a report. Nil is a normal
// outcome — no MAIL_TO means no reporting address, which is how a developer's
// machine stays out of the team's inbox.
func dailyReport(cfg app.Config, pool *pgxpool.Pool, logger *slog.Logger) *api.DailyReport {
	if cfg.Report.To == "" || cfg.Report.From == "" {
		return nil
	}
	sender := mail.NewSMTPSender(mail.Config{
		Host: cfg.Report.Host,
		Port: cfg.Report.Port,
		// Gmail authenticates as the sending account, with an app password.
		Username: cfg.Report.From,
		Password: cfg.Report.Password,
		From:     cfg.Report.From,
	}, logger)

	return api.NewDailyReport(store.New(pool), sender, api.DailyReportConfig{
		Recipients: cfg.Report.To,
		Subject:    cfg.Report.Subject,
		CutoffHour: cfg.Fulfilment.CutoffHourIST,
	}, logger)
}

// outboxDispatcher builds the transactional email worker.
//
// It uses the SMTP_* relay — MailHog in development — not the report's Gmail
// account: an order confirmation must never escape a laptop, and the daily
// courier sheet must reach real inboxes. Two relays, deliberately.
//
// The api.New instance here exists solely to reach the customer-contact lookup
// in internal/api/profile_lookup.go, which is a method on *API. It is given
// only what that call needs; nothing else on this instance is used, and the
// dispatcher calls no handler.
func outboxDispatcher(
	cfg app.Config, pool *pgxpool.Pool, logger *slog.Logger,
) *api.OutboxDispatcher {
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

	return api.NewOutboxDispatcher(
		pool, lookups, sender, time.Minute, cfg.SupportEmail, logger)
}
