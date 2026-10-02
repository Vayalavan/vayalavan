package httpx

import (
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/vayal-mikrogreenz/vm-go-common/logging"
)

// statusRecorder captures the status code and byte count for access logging,
// which net/http otherwise discards once the response is written.
type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	// A handler that writes without calling WriteHeader implies 200.
	if r.status == 0 {
		r.status = http.StatusOK
	}
	n, err := r.ResponseWriter.Write(b)
	r.bytes += n
	return n, err
}

// RequestID adopts the inbound X-Request-Id or mints a new one, places it on
// the request context, and echoes it on the response.
//
// The gateway is the normal origin of the id (CLAUDE.md rule 8); internal
// services adopt whatever it sends so a single customer action is one
// traceable id across all four services. Echoing it back lets a user paste
// the id from a failed request straight into a support ticket.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(logging.RequestIDHeader)
		if id == "" {
			id = logging.NewRequestID()
		}
		w.Header().Set(logging.RequestIDHeader, id)
		next.ServeHTTP(w, r.WithContext(logging.WithRequestID(r.Context(), id)))
	})
}

// RequestLogger emits one structured line per completed request.
//
// Health checks are logged at debug level: an orchestrator polling /healthz
// every few seconds would otherwise bury real traffic in noise.
func RequestLogger(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w}

			next.ServeHTTP(rec, r)

			if rec.status == 0 {
				rec.status = http.StatusOK
			}

			level := slog.LevelInfo
			switch {
			case isHealthPath(r.URL.Path):
				level = slog.LevelDebug
			case rec.status >= http.StatusInternalServerError:
				level = slog.LevelError
			case rec.status >= http.StatusBadRequest:
				level = slog.LevelWarn
			}

			logger.Log(r.Context(), level, "request completed",
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", rec.status),
				slog.Int("bytes", rec.bytes),
				slog.Int64("duration_ms", time.Since(start).Milliseconds()),
				slog.String("remote_addr", r.RemoteAddr),
			)
		})
	}
}

func isHealthPath(path string) bool {
	return path == HealthzPath || path == ReadyzPath
}

// Recoverer converts a panic into a logged 500 rather than a dropped
// connection, so one bad request cannot take the process down and leave a
// customer staring at a browser error with nothing in the logs.
func Recoverer(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				rec := recover()
				if rec == nil {
					return
				}
				// A client disconnecting mid-write is normal, not a bug, and
				// the connection is already gone — nothing to respond to.
				if rec == http.ErrAbortHandler {
					panic(rec)
				}

				logger.ErrorContext(r.Context(), "panic recovered",
					slog.Any("panic", rec),
					slog.String("method", r.Method),
					slog.String("path", r.URL.Path),
					slog.String("stack", string(debug.Stack())),
				)

				WriteError(r.Context(), w, logger,
					Internal(nil))
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// InternalAuth rejects any request not carrying the shared service token.
//
// CLAUDE.md rule 5: only the gateway is publicly reachable, and the Go
// services require INTERNAL_SERVICE_TOKEN. This is defence in depth — the
// services should also be unroutable from outside — because a network
// misconfiguration should not be the only thing standing between the public
// internet and an unauthenticated orders API.
//
// The health endpoints are exempt so an orchestrator can probe them without
// holding the secret.
func InternalAuth(token string, logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if isHealthPath(r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}
			if !secureCompare(r.Header.Get(InternalTokenHeader), token) {
				logger.WarnContext(r.Context(), "internal token rejected",
					slog.String("path", r.URL.Path),
					slog.String("remote_addr", r.RemoteAddr),
				)
				WriteError(r.Context(), w, logger,
					Unauthorized("This endpoint is not publicly accessible."))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// InternalTokenHeader carries the shared service-to-service secret.
const InternalTokenHeader = "X-Internal-Token"

// MetricsMiddleware records every completed request into m.
//
// Separate from RequestLogger rather than folded into it: logging is always
// on, metrics are a scrape target, and a service that wants one without the
// other should not have to take both. Mounted after the router so chi's route
// pattern is available — recording raw paths would put every order id into a
// label and blow up cardinality.
func MetricsMiddleware(m *Metrics) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// The scrape endpoint measuring itself is noise.
			if r.URL.Path == MetricsPath {
				next.ServeHTTP(w, r)
				return
			}

			defer m.TrackInFlight()()

			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w}
			next.ServeHTTP(rec, r)
			if rec.status == 0 {
				rec.status = http.StatusOK
			}

			m.Observe(RoutePattern(r), rec.status, time.Since(start))
		})
	}
}
