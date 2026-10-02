// Package logging provides the platform's structured JSON logger.
//
// CLAUDE.md rule 8: every service emits structured JSON logs carrying a
// request_id propagated from the gateway via X-Request-Id. Correlating a
// customer's failed checkout across the gateway, orders and catalog is only
// possible if that id rides along on every line, so this package attaches it
// automatically from the context rather than trusting each call site to
// remember.
package logging

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"log/slog"
	"os"
	"strings"
)

// RequestIDHeader is the header the request id travels in, between the
// gateway and the internal services.
const RequestIDHeader = "X-Request-Id"

// requestIDKey is an unexported context key type, so no other package can
// collide with or overwrite our value.
type requestIDKey struct{}

// WithRequestID returns a context carrying the given request id.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey{}, id)
}

// RequestIDFrom returns the request id on the context, or "" if absent.
func RequestIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

// NewRequestID returns a random 128-bit hex id.
//
// Generated with crypto/rand rather than a counter or timestamp so ids stay
// unique across replicas and restarts without coordination.
func NewRequestID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand failing means the OS entropy source is broken. A
		// degraded log correlation id beats taking the service down.
		return "unknown"
	}
	return hex.EncodeToString(b[:])
}

// ParseLevel maps a configured level name onto a slog.Level, defaulting to
// info for anything unrecognised.
func ParseLevel(level string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// contextHandler decorates every record with the context's request id.
//
// A handler wrapper rather than a logger method: it means logger.InfoContext
// from anywhere in the codebase is automatically correlated, and there is no
// way to accidentally log a line without the id.
type contextHandler struct {
	slog.Handler
}

func (h contextHandler) Handle(ctx context.Context, r slog.Record) error {
	if id := RequestIDFrom(ctx); id != "" {
		r.AddAttrs(slog.String("request_id", id))
	}
	return h.Handler.Handle(ctx, r)
}

// WithAttrs and WithGroup must re-wrap, or the request id injection is lost
// the first time a caller does logger.With(...).
func (h contextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return contextHandler{h.Handler.WithAttrs(attrs)}
}

func (h contextHandler) WithGroup(name string) slog.Handler {
	return contextHandler{h.Handler.WithGroup(name)}
}

// New returns a JSON logger tagged with the service name.
//
// JSON unconditionally, in every environment: a log format that differs
// between local and production is a log format whose parsing bugs are only
// discovered in production.
func New(serviceName, level string) *slog.Logger {
	return NewTo(os.Stdout, serviceName, level)
}

// NewTo is New with an explicit destination, for tests.
func NewTo(w io.Writer, serviceName, level string) *slog.Logger {
	handler := slog.NewJSONHandler(w, &slog.HandlerOptions{
		Level: ParseLevel(level),
	})
	return slog.New(contextHandler{handler}).With(slog.String("service", serviceName))
}
