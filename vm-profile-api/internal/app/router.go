package app

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vayal-mikrogreenz/vm-go-common/db"
	"github.com/vayal-mikrogreenz/vm-go-common/httpx"

	"github.com/vayal-mikrogreenz/vm-profile-api/internal/api"
	"github.com/vayal-mikrogreenz/vm-profile-api/internal/auth"
	"github.com/vayal-mikrogreenz/vm-profile-api/internal/mail"
	"github.com/vayal-mikrogreenz/vm-profile-api/internal/store"
)

// NewRouter builds the service's HTTP handler.
//
// Route grouping IS the authorisation model (CLAUDE.md §7): every non-public
// route sits inside a group carrying a RequireRole guard, so adding a handler
// to the wrong block is visible in the diff rather than discovered later.
//
// The gateway guards these routes too. This layer is the one that must be
// right — a routing mistake at the edge should not be able to expose an admin
// endpoint.
func NewRouter(cfg Config, pool *pgxpool.Pool, logger *slog.Logger) http.Handler {
	minter := auth.NewTokenMinter(cfg.JWTSecret, cfg.AccessTokenTTL)
	queries := store.New(pool)
	sessions := api.NewSessionService(pool, queries, minter, cfg.RefreshTokenTTL, logger)

	mailer := mail.NewSMTPSender(mail.Config{
		Host:     cfg.SMTP.Host,
		Port:     cfg.SMTP.Port,
		Username: cfg.SMTP.User,
		Password: cfg.SMTP.Password,
		From:     cfg.SMTP.From,
	}, logger)

	handlers := api.New(pool, sessions, cfg.BcryptCost, mailer, cfg.SupplierAppURL, logger)

	r := chi.NewRouter()

	// RequestID first so every later log line is correlated. RequestLogger
	// wraps Recoverer so a recovered panic is still logged as a completed 500.
	r.Use(httpx.RequestID)
	r.Use(httpx.LoggerContext(logger))
	r.Use(httpx.RequestLogger(logger))
	r.Use(httpx.Recoverer(logger))
	// Counters for the scrape endpoint below. After Recoverer, so a panicked
	// request is still counted as the 500 the client received.
	metrics := httpx.NewMetrics(ServiceName)
	r.Use(httpx.MetricsMiddleware(metrics))
	// Proves the caller is the gateway. Everything below trusts the identity
	// headers only because this has already run.
	r.Use(httpx.InternalAuth(cfg.InternalToken, logger))
	r.Use(httpx.ActorContext)

	r.Get(httpx.HealthzPath, httpx.Healthz(ServiceName))
	// Internal only: the gateway does not route to /metrics, so it is reachable
	// from inside the network exactly like the rest of this service.
	r.Get(httpx.MetricsPath, metrics.Handler())
	r.Get(httpx.ReadyzPath, httpx.Readyz(ServiceName, logger,
		httpx.Check{Name: "postgres", Probe: db.Check(pool)},
	))

	// --- unauthenticated -----------------------------------------------------
	// Reachable without a user identity. Rate limiting for these lives at the
	// gateway, which is where the client IP is actually known.
	r.Post("/auth/register", handlers.Register)
	r.Post("/auth/login", handlers.Login)
	r.Post("/auth/refresh", handlers.Refresh)
	r.Post("/auth/logout", handlers.Logout)
	r.Post("/auth/set-password", handlers.SetPassword)
	r.Post("/suppliers/apply", handlers.ApplyAsSupplier)

	// --- service-to-service --------------------------------------------------
	// No role guard: the caller is another service, already proven by
	// InternalAuth. There is no end user behind this request.
	r.Get("/internal/suppliers/approved-ids", handlers.ApprovedSupplierIDs)
	r.Post("/internal/suppliers/directory", handlers.SupplierDirectory)
	r.Post("/internal/suppliers/commissions", handlers.SupplierCommissions)
	// Addresses for transactional email, for vm-orders-api's outbox worker.
	r.Post("/internal/customers/contacts", handlers.CustomerContacts)
	r.Get("/internal/suppliers/pending-count", handlers.PendingApplicationCount)
	// Unmasked bank details. Guarded by the forwarded admin identity AND
	// audited on this side (CLAUDE.md §5.1).
	r.Post("/internal/suppliers/bank-details", handlers.SupplierBankDetails)

	// --- any signed-in user --------------------------------------------------
	r.Group(func(r chi.Router) {
		r.Use(httpx.RequireRole(auth.RoleCustomer, auth.RoleSupplier, auth.RoleAdmin, auth.RoleAnalyst))
		r.Get("/me", handlers.Me)
	})

	// --- customers only ------------------------------------------------------
	r.Route("/addresses", func(r chi.Router) {
		r.Use(httpx.RequireRole(auth.RoleCustomer))
		r.Get("/", handlers.ListAddresses)
		r.Post("/", handlers.CreateAddress)
		r.Get("/{id}", handlers.GetAddress)
		r.Put("/{id}", handlers.UpdateAddress)
		r.Delete("/{id}", handlers.DeleteAddress)
		r.Post("/{id}/default", handlers.SetDefaultAddress)
	})

	// --- suppliers and admins ------------------------------------------------
	// The handler additionally enforces that a supplier sees only its own row.
	r.Group(func(r chi.Router) {
		r.Use(httpx.RequireRole(auth.RoleSupplier, auth.RoleAdmin))
		r.Get("/suppliers/{id}", handlers.GetSupplier)
	})

	// --- admins only ---------------------------------------------------------
	r.Route("/admin", func(r chi.Router) {
		r.Use(httpx.RequireRole(auth.RoleAdmin))

		r.Get("/suppliers", handlers.ListSuppliers)
		r.Post("/suppliers", handlers.AdminCreateSupplier)
		r.Patch("/suppliers/{id}", handlers.AdminUpdateSupplier)
		r.Post("/suppliers/{id}/approve", handlers.ApproveSupplier)
		r.Post("/suppliers/{id}/reject", handlers.RejectSupplier)
		r.Post("/suppliers/{id}/suspend", handlers.SuspendSupplier)
	})

	return r
}
