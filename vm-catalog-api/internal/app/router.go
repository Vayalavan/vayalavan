package app

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vayal-mikrogreenz/vm-go-common/db"
	"github.com/vayal-mikrogreenz/vm-go-common/httpx"

	"github.com/vayal-mikrogreenz/vm-catalog-api/internal/api"
	"github.com/vayal-mikrogreenz/vm-catalog-api/internal/storage"
	"github.com/vayal-mikrogreenz/vm-catalog-api/internal/suppliers"
)

// NewRouter builds the service's HTTP handler.
//
// Route grouping IS the authorisation model (CLAUDE.md §7): every route sits
// inside a group carrying a RequireRole guard, so adding a handler to the
// wrong block shows up in the diff. Handlers additionally scope every query to
// the supplier id from the verified token, so role alone is never the only
// thing standing between one supplier and another's catalogue.
//
// store may be nil in tests that only exercise liveness.
func NewRouter(
	cfg Config, pool *pgxpool.Pool, store *storage.Client, logger *slog.Logger,
) http.Handler {
	supplierClient := suppliers.NewClient(cfg.ProfileAPIURL, cfg.InternalToken, logger)

	handlers := api.New(pool, store, supplierClient, api.Limits{
		MaxImageBytes:          cfg.Limits.MaxImageBytes,
		MaxVideoBytes:          cfg.Limits.MaxVideoBytes,
		MaxMediaPerSizeCode:    cfg.Limits.MaxMediaPerSizeCode,
		MaxSizeCodesPerProduct: cfg.Limits.MaxSizeCodesPerProduct,
		MaxCSVBytes:            cfg.Limits.MaxCSVBytes,
		MaxCSVRows:             cfg.Limits.MaxCSVRows,
	}, api.Rates{
		DefaultMarkupBPS: cfg.DefaultMarkupBPS,
	}, logger)

	r := chi.NewRouter()

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

	checks := []httpx.Check{{Name: "postgres", Probe: db.Check(pool)}}
	if store != nil {
		checks = append(checks, httpx.Check{Name: "object_storage", Probe: store.Check()})
	}
	r.Get(httpx.HealthzPath, httpx.Healthz(ServiceName))
	// Internal only: the gateway does not route to /metrics, so it is reachable
	// from inside the network exactly like the rest of this service.
	r.Get(httpx.MetricsPath, metrics.Handler())
	r.Get(httpx.ReadyzPath, httpx.Readyz(ServiceName, logger, checks...))

	// --- suppliers -----------------------------------------------------------
	r.Group(func(r chi.Router) {
		r.Use(httpx.RequireRole(httpx.RoleSupplier))

		r.Route("/products", func(r chi.Router) {
			r.Get("/", handlers.ListProducts)
			r.Post("/", handlers.CreateProduct)
			r.Get("/{id}", handlers.GetProduct)
			// PUT replaces the product AND its whole unit set atomically.
			r.Put("/{id}", handlers.UpdateProduct)
			r.Post("/{id}/archive", handlers.ArchiveProduct)
		})

		r.Post("/uploads/presign", handlers.PresignUpload)

		// Daily availability — the supplier's morning routine.
		r.Route("/supplier/availability", func(r chi.Router) {
			r.Get("/", handlers.AvailabilitySheet)
			r.Put("/", handlers.SaveAvailability)
			r.Post("/copy-from-yesterday", handlers.CopyFromYesterday)
			// Static path is registered before the {id} pattern above it in
			// chi's tree, so "copy-from-yesterday" is never read as an id.
			r.Post("/{id}/close", handlers.CloseAvailability)
			r.Post("/{id}/reopen", handlers.ReopenAvailability)
		})

		r.Route("/imports", func(r chi.Router) {
			// Static path before the {id} pattern, or "template" would be
			// parsed as an import id.
			r.Get("/template", handlers.DownloadTemplate)
			r.Get("/", handlers.ListImports)
			// Step one: validate and preview. Writes nothing to products.
			r.Post("/", handlers.UploadImport)
			r.Get("/{id}", handlers.GetImport)
			// Step two: apply the previewed rows.
			r.Post("/{id}/commit", handlers.CommitImport)
		})
	})

	// --- service-to-service ---------------------------------------------------
	// No role guard: the caller is vm-orders-api, already proven by
	// InternalAuth. There is no end user behind these.
	r.Post("/internal/reservations", handlers.Reserve)
	r.Post("/internal/reservations/settle", handlers.SettleHolds)
	// Today's packs with BOTH prices and the exact stock behind them. The one
	// source vm-orders-api prices a cart from — see the handler for why it no
	// longer reads the customer-facing /catalog.
	r.Get("/internal/units/today", handlers.InternalUnits)
	// Presigned product photographs for orders-api, which has product ids on
	// its order lines but no access to the catalog schema.
	r.Post("/internal/products/images", handlers.ProductImages)

	// --- customers ------------------------------------------------------------
	// No role guard: browsing the storefront needs no account. The gateway
	// exposes this without authentication; InternalAuth still applies, so it
	// is reachable only through the gateway.
	r.Get("/catalog", handlers.Catalog)
	r.Get("/catalog/{id}", handlers.CatalogProduct)

	// --- admins --------------------------------------------------------------
	r.Route("/admin", func(r chi.Router) {
		r.Use(httpx.RequireRole(httpx.RoleAdmin))

		r.Get("/products", handlers.AdminListProducts)
		r.Get("/products/{id}", handlers.AdminGetProduct)
		r.Post("/products/{id}/archive", handlers.AdminArchiveProduct)
		// What we add to a grower's price. Admin only, and absent from every
		// supplier-facing response.
		r.Put("/products/{id}/markup", handlers.SetProductMarkup)
	})

	return r
}
