package app

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vayal-mikrogreenz/vm-go-common/db"
	"github.com/vayal-mikrogreenz/vm-go-common/httpx"

	"github.com/vayal-mikrogreenz/vm-events-consumer-api/internal/cdc"
)

// NewRouter builds the HTTP surface: health, metrics and the stream's status.
// This service has no business endpoints — its work is the replication
// stream — so the router exists for probes and for an operator asking
// "how far behind is analytics?".
func NewRouter(cfg Config, pool *pgxpool.Pool, stream *cdc.Stream, logger *slog.Logger) http.Handler {
	r := chi.NewRouter()

	r.Use(httpx.RequestID)
	r.Use(httpx.LoggerContext(logger))
	r.Use(httpx.RequestLogger(logger))
	r.Use(httpx.Recoverer(logger))
	metrics := httpx.NewMetrics(ServiceName)
	r.Use(httpx.MetricsMiddleware(metrics))
	r.Use(httpx.InternalAuth(cfg.InternalToken, logger))

	r.Get(httpx.HealthzPath, httpx.Healthz(ServiceName))
	r.Get(httpx.MetricsPath, metrics.Handler())
	// Not ready while the stream is down: nothing is reaching analytics.
	r.Get(httpx.ReadyzPath, httpx.Readyz(ServiceName, logger,
		httpx.Check{Name: "postgres", Probe: db.Check(pool)},
		httpx.Check{Name: "replication", Probe: stream.Ready},
	))

	r.Get("/status", func(w http.ResponseWriter, r *http.Request) {
		httpx.WriteJSON(r.Context(), w, logger, http.StatusOK, stream.Status())
	})
	return r
}
