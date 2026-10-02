package app

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vayal-mikrogreenz/vm-go-common/db"
	"github.com/vayal-mikrogreenz/vm-go-common/httpx"

	"github.com/vayal-mikrogreenz/vm-orders-api/internal/api"
	"github.com/vayal-mikrogreenz/vm-orders-api/internal/catalogclient"
	"github.com/vayal-mikrogreenz/vm-orders-api/internal/pricing"
	"github.com/vayal-mikrogreenz/vm-orders-api/internal/razorpay"
)

// NewRouter builds the service's HTTP handler.
//
// Foundation only: the health probes are the whole surface. Domain routes
// (identity, customers, addresses, suppliers) mount under the commented
// section below once the schema exists.
// NewAPI builds the handler set. Shared by the router and by main's background
// jobs, so the charging run (CLAUDE.md §6.7) prices, reserves and pays with
// exactly the configuration a checkout does.
func NewAPI(cfg Config, pool *pgxpool.Pool, logger *slog.Logger) *api.API {
	catalog := catalogclient.New(cfg.CatalogAPIURL, cfg.InternalToken)

	// The whole commercial model, in one struct. Validated here so a
	// nonsensical configuration — a delivery margin larger than the delivery
	// charge, a commission above 100% — stops the service at boot rather than
	// mispaying every supplier quietly (CLAUDE.md rule 3).
	rates := pricing.Rates{
		PlatformFeeBPS:        cfg.Pricing.PlatformFeeBPS,
		SupplierCommissionBPS: cfg.Pricing.SupplierCommissionBPS,
		DeliveryFeePaise:      cfg.Pricing.DeliveryFeePaise,
		DeliveryMarginPaise:   cfg.Pricing.DeliveryMarginPaise,
	}
	if err := pricing.ValidateRates(rates); err != nil {
		logger.Error("invalid pricing configuration", slog.Any("error", err))
		panic(err)
	}

	rzp := razorpay.NewClient(cfg.Razorpay.KeyID, cfg.Razorpay.KeySecret)

	return api.New(api.Deps{
		Pool:          pool,
		Catalog:       catalog,
		ProfileAPIURL: cfg.ProfileAPIURL,
		InternalToken: cfg.InternalToken,
		Pricing: pricing.Config{
			PlatformFeeBPS:   cfg.Pricing.PlatformFeeBPS,
			DeliveryFeePaise: cfg.Pricing.DeliveryFeePaise,
		},
		Rates:                 rates,
		CutoffHourIST:         cfg.Fulfilment.CutoffHourIST,
		ReserveTTL:            cfg.Fulfilment.ReservationTTL,
		SupportEmail:          cfg.SupportEmail,
		Razorpay:              rzp,
		RazorpayKeyID:         cfg.Razorpay.KeyID,
		RazorpayKeySecret:     cfg.Razorpay.KeySecret,
		RazorpayWebhookSecret: cfg.Razorpay.WebhookSecret,
		WebhookMaxAttempts:    cfg.Razorpay.WebhookMaxAttempts,
		Wallet: api.WalletLimits{
			MinTopupPaise:   cfg.Wallet.MinTopupPaise,
			MaxTopupPaise:   cfg.Wallet.MaxTopupPaise,
			MaxBalancePaise: cfg.Wallet.MaxBalancePaise,
		},
		Schedules: api.ScheduleConfig{
			ChargeLead:           cfg.Schedules.ChargeLead,
			LowBalancePauseAfter: cfg.Schedules.LowBalancePauseAfter,
		},
		Logger: logger,
	})
}

func NewRouter(cfg Config, pool *pgxpool.Pool, logger *slog.Logger) http.Handler {
	handlers := NewAPI(cfg, pool, logger)

	r := chi.NewRouter()

	// Order matters. RequestID is first so every subsequent log line is
	// correlated. RequestLogger wraps Recoverer rather than the reverse, so
	// a recovered panic is still recorded as a completed 500 rather than
	// vanishing from the access log.
	r.Use(httpx.RequestID)
	r.Use(httpx.LoggerContext(logger))
	r.Use(httpx.RequestLogger(logger))
	r.Use(httpx.Recoverer(logger))
	// Counters for the scrape endpoint below. After Recoverer, so a panicked
	// request is still counted as the 500 the client received.
	metrics := httpx.NewMetrics(ServiceName)
	r.Use(httpx.MetricsMiddleware(metrics))
	r.Use(httpx.InternalAuth(cfg.InternalToken, logger))
	r.Use(httpx.ActorContext)

	r.Get(httpx.HealthzPath, httpx.Healthz(ServiceName))
	// Internal only: the gateway does not route to /metrics, so it is reachable
	// from inside the network exactly like the rest of this service.
	r.Get(httpx.MetricsPath, metrics.Handler())
	r.Get(httpx.ReadyzPath, httpx.Readyz(ServiceName, logger,
		httpx.Check{Name: "postgres", Probe: db.Check(pool)},
	))

	// Domain routes go here. See CLAUDE.md §5.1 and §7.

	// --- admins only ----------------------------------------------------------
	r.Route("/admin", func(r chi.Router) {
		r.Use(httpx.RequireRole(httpx.RoleAdmin))

		r.Get("/dashboard", handlers.AdminDashboard)
		// The dashboard's breakdowns, on their own request so a slow
		// group-by cannot delay the headline tiles.
		r.Get("/analytics", handlers.AdminAnalytics)

		r.Route("/orders", func(r chi.Router) {
			r.Get("/", handlers.AdminListOrders)
			r.Get("/{id}", handlers.AdminGetOrder)
			// Bulk actions BEFORE the {id} patterns, or chi matches
			// "bulk-process" as an order id.
			r.Post("/bulk-process", handlers.AdminBulkProcess)
			r.Post("/bulk-dispatch", handlers.AdminBulkDispatch)
			r.Get("/export.csv", handlers.AdminExportOrders)
			r.Post("/{id}/process", handlers.AdminProcessOrder)
			r.Post("/{id}/dispatch", handlers.AdminDispatchOrder)
			r.Post("/{id}/record-payment", handlers.AdminRecordPayment)
			r.Post("/{id}/cancel", handlers.AdminCancelOrder)
		})

		// The prepaid wallet — CLAUDE.md §6.7. Refunds are audited.
		r.Route("/wallets", func(r chi.Router) {
			r.Get("/", handlers.AdminListWallets)
			r.Get("/{customer_id}", handlers.AdminGetWallet)
			r.Post("/{customer_id}/refund", handlers.AdminRefundWallet)
		})

		r.Route("/payouts", func(r chi.Router) {
			// Static paths before any {id} pattern.
			r.Get("/summary", handlers.PayoutSummary)
			r.Get("/export.csv", handlers.ExportPendingPayouts)
			r.Get("/", handlers.ListPayouts)
			r.Post("/mark-paid", handlers.MarkPayoutsPaid)
		})
	})

	// --- suppliers only -------------------------------------------------------
	//
	// Scoped to the calling supplier inside the handler as well as here: the
	// role guard says "a supplier", the handler decides WHICH supplier from
	// the verified actor, so one grower can never read another's sales.
	r.Route("/supplier", func(r chi.Router) {
		r.Use(httpx.RequireRole(httpx.RoleSupplier))
		r.Get("/sales", handlers.SupplierSales)
		// The trend screen. Separate from /sales because the two answer
		// different questions and want different default periods.
		r.Get("/analytics", handlers.SupplierAnalytics)
	})

	// --- public ---------------------------------------------------------------
	// No identity needed: the cutoff is the same for everyone, and a customer
	// browsing anonymously still needs to know how long they have.
	r.Get("/cutoff", handlers.Cutoff)

	// --- payment provider -----------------------------------------------------
	//
	// No role guard and no user identity: the caller is Razorpay. Its own
	// HMAC signature over the RAW body is the authentication (CLAUDE.md §6.4).
	// The gateway forwards this route's bytes verbatim — see vm-gateway-api's
	// webhook route — because re-serialising the JSON would break the
	// signature.
	r.Post("/webhooks/razorpay", handlers.RazorpayWebhook)

	// --- customers only ------------------------------------------------------
	// Handlers additionally scope every query to the customer id from the
	// verified token, so role alone is never the only guard.
	r.Group(func(r chi.Router) {
		r.Use(httpx.RequireRole(httpx.RoleCustomer))

		r.Route("/cart", func(r chi.Router) {
			r.Get("/", handlers.GetCart)
			r.Post("/items", handlers.AddCartItem)
			r.Patch("/items/{id}", handlers.UpdateCartItem)
			r.Delete("/items/{id}", handlers.RemoveCartItem)
		})

		r.Route("/orders", func(r chi.Router) {
			r.Get("/", handlers.ListOrders)
			// Idempotent via the Idempotency-Key header (CLAUDE.md rule 6).
			r.Post("/", handlers.PlaceOrder)
			r.Get("/{id}", handlers.GetOrder)
			// A UX hint only. It can never be the sole path to `paid` — that
			// is the webhook's job.
			r.Post("/{id}/verify-payment", handlers.VerifyPayment)
		})

		// Wallet and scheduled / repeat orders — CLAUDE.md §6.7.
		r.Route("/wallet", func(r chi.Router) {
			r.Get("/", handlers.GetWallet)
			r.Post("/topups", handlers.CreateTopup)
			r.Get("/topups/{id}", handlers.GetTopup)
			r.Post("/topups/{id}/verify", handlers.VerifyTopup)
		})

		r.Route("/schedules", func(r chi.Router) {
			r.Get("/", handlers.ListSchedules)
			r.Post("/", handlers.CreateSchedule)
			r.Get("/options", handlers.ScheduleOptions)
			r.Get("/{id}", handlers.GetSchedule)
			r.Patch("/{id}", handlers.UpdateSchedule)
			r.Post("/{id}/pause", handlers.PauseSchedule)
			r.Post("/{id}/resume", handlers.ResumeSchedule)
			r.Post("/{id}/cancel", handlers.CancelSchedule)
			r.Post("/{id}/skip", handlers.SkipDelivery)
			r.Delete("/{id}/skip/{date}", handlers.UnskipDelivery)
		})
	})

	return r
}
