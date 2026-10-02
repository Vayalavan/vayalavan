// Package httpx holds the HTTP conventions shared by every service: the
// single error shape, the JSON writers, and the standard middleware chain.
package httpx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
)

// Error is the platform's single error type.
//
// CLAUDE.md rule 8 fixes one wire shape for every service:
//
//	{"error": {"code": "SNAKE_CASE_CODE", "message": "...", "details": {}}}
//
// One shape means the three UIs share one error handler, and a client never
// has to guess whether a failure came from the gateway or a Go service.
type Error struct {
	// Status is the HTTP status code to respond with.
	Status int
	// Code is a stable SNAKE_CASE identifier. Clients branch on this; the
	// message is for humans and may be reworded freely.
	Code string
	// Message is a human-readable explanation, safe to show a user.
	Message string
	// Details carries structured context, e.g. per-field validation errors.
	Details map[string]any
	// cause is the underlying error. Logged, never serialised — it can carry
	// SQL fragments, connection strings, or internal hostnames.
	cause error
}

func (e *Error) Error() string {
	if e.cause != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.cause)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// Unwrap exposes the cause to errors.Is and errors.As.
func (e *Error) Unwrap() error { return e.cause }

// WithCause attaches an underlying error for logging.
func (e *Error) WithCause(err error) *Error {
	e.cause = err
	return e
}

// WithDetails attaches structured context for the client.
func (e *Error) WithDetails(details map[string]any) *Error {
	e.Details = details
	return e
}

// Standard error codes. Services define their own domain codes alongside
// these; these cover the cases every service hits.
const (
	CodeBadRequest      = "BAD_REQUEST"
	CodeValidation      = "VALIDATION_FAILED"
	CodeUnauthorized    = "UNAUTHORIZED"
	CodeForbidden       = "FORBIDDEN"
	CodeNotFound        = "NOT_FOUND"
	CodeConflict        = "CONFLICT"
	CodeTooManyRequests = "TOO_MANY_REQUESTS"
	CodeInternal        = "INTERNAL_ERROR"
	CodeUnavailable     = "SERVICE_UNAVAILABLE"
)

// NewError builds an Error with an explicit status and code.
func NewError(status int, code, message string) *Error {
	return &Error{Status: status, Code: code, Message: message}
}

// BadRequest reports malformed input, e.g. unparseable JSON.
func BadRequest(message string) *Error {
	return NewError(http.StatusBadRequest, CodeBadRequest, message)
}

// Validation reports well-formed input that violates a business rule.
func Validation(message string, details map[string]any) *Error {
	return NewError(http.StatusUnprocessableEntity, CodeValidation, message).
		WithDetails(details)
}

// Unauthorized reports a missing or invalid credential.
func Unauthorized(message string) *Error {
	return NewError(http.StatusUnauthorized, CodeUnauthorized, message)
}

// Forbidden reports a valid credential lacking permission. This is the
// response for the ownership checks in CLAUDE.md §7 — one supplier reaching
// for another supplier's data.
func Forbidden(message string) *Error {
	return NewError(http.StatusForbidden, CodeForbidden, message)
}

// NotFound reports an absent resource.
func NotFound(message string) *Error {
	return NewError(http.StatusNotFound, CodeNotFound, message)
}

// Conflict reports a state clash, e.g. stock exhausted between browsing and
// checkout.
func Conflict(code, message string) *Error {
	return NewError(http.StatusConflict, code, message)
}

// TooManyRequests reports rate limiting.
func TooManyRequests(message string) *Error {
	return NewError(http.StatusTooManyRequests, CodeTooManyRequests, message)
}

// Internal reports an unexpected server-side failure. The message is
// deliberately generic; the cause is logged, not returned.
func Internal(err error) *Error {
	return NewError(http.StatusInternalServerError, CodeInternal,
		"Something went wrong on our end. Please try again.").WithCause(err)
}

// Unavailable reports a dependency being down, e.g. a failed readiness check.
func Unavailable(message string) *Error {
	return NewError(http.StatusServiceUnavailable, CodeUnavailable, message)
}

// errorBody is the wire envelope. Details is omitted when empty rather than
// serialised as null, so clients can rely on `details` being an object when
// present.
type errorBody struct {
	Error errorPayload `json:"error"`
}

type errorPayload struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

// WriteJSON writes v as a JSON response with the given status.
func WriteJSON(ctx context.Context, w http.ResponseWriter, logger *slog.Logger, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)

	if v == nil {
		return
	}
	if err := json.NewEncoder(w).Encode(v); err != nil {
		// The status line is already sent, so the response cannot be
		// corrected. Log it: a client is receiving truncated JSON.
		logger.ErrorContext(ctx, "encoding response body failed", slog.Any("error", err))
	}
}

// WriteError renders any error in the platform's standard shape.
//
// Unrecognised errors become a generic 500 with the original logged rather
// than returned, so an accidental fmt.Errorf carrying a connection string
// cannot leak to a customer.
func WriteError(ctx context.Context, w http.ResponseWriter, logger *slog.Logger, err error) {
	var appErr *Error
	if !errors.As(err, &appErr) {
		appErr = Internal(err)
	}

	attrs := []any{
		slog.String("error_code", appErr.Code),
		slog.Int("status", appErr.Status),
		slog.Any("error", err),
	}

	// 5xx is our bug and needs attention; 4xx is the client's and is
	// expected traffic. Logging both at error level makes the signal useless.
	if appErr.Status >= http.StatusInternalServerError {
		logger.ErrorContext(ctx, "request failed", attrs...)
	} else {
		logger.InfoContext(ctx, "request rejected", attrs...)
	}

	WriteJSON(ctx, w, logger, appErr.Status, errorBody{
		Error: errorPayload{
			Code:    appErr.Code,
			Message: appErr.Message,
			Details: appErr.Details,
		},
	})
}
