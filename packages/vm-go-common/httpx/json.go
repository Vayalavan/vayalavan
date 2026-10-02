package httpx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
)

// maxRequestBodyBytes caps a decoded JSON body. The gateway enforces its own
// limit; this is the backstop for anything that reaches a service directly.
const maxRequestBodyBytes = 1 << 20 // 1 MiB

type loggerKey struct{}

// WithLogger puts a request-scoped logger on the context.
func WithLogger(ctx context.Context, logger *slog.Logger) context.Context {
	return context.WithValue(ctx, loggerKey{}, logger)
}

// LoggerFrom returns the request-scoped logger, falling back to the default
// so a missing logger never panics inside an error path.
func LoggerFrom(ctx context.Context) *slog.Logger {
	if logger, ok := ctx.Value(loggerKey{}).(*slog.Logger); ok {
		return logger
	}
	return slog.Default()
}

// LoggerContext attaches a logger to every request context, so helpers like
// RequireRole can report failures without being handed one.
func LoggerContext(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(WithLogger(r.Context(), logger)))
		})
	}
}

// DecodeJSON reads a JSON request body into dst.
//
// Strict on purpose: unknown fields are rejected so a client typo
// ("phon_number") fails loudly instead of being silently dropped and
// producing a record missing the field the caller thought they set.
func DecodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	if ct := r.Header.Get("Content-Type"); ct != "" &&
		!containsMediaType(ct, "application/json") {
		return BadRequest("Content-Type must be application/json.")
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)

	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(dst); err != nil {
		var syntaxErr *json.SyntaxError
		var typeErr *json.UnmarshalTypeError
		var maxBytesErr *http.MaxBytesError

		switch {
		case errors.As(err, &syntaxErr):
			return BadRequest(fmt.Sprintf(
				"Request body is not valid JSON (at position %d).", syntaxErr.Offset))
		case errors.As(err, &typeErr):
			return BadRequest(fmt.Sprintf(
				"Field %q has the wrong type.", typeErr.Field))
		case errors.As(err, &maxBytesErr):
			return BadRequest("Request body is too large.")
		case errors.Is(err, io.EOF):
			return BadRequest("Request body is empty.")
		default:
			return BadRequest("Request body could not be parsed.")
		}
	}

	// A second value would mean the client sent two JSON documents; ignoring
	// the tail silently would be a parsing ambiguity.
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return BadRequest("Request body must contain exactly one JSON object.")
	}
	return nil
}

func containsMediaType(header, want string) bool {
	for i := 0; i+len(want) <= len(header); i++ {
		if header[i:i+len(want)] == want {
			return true
		}
	}
	return false
}
