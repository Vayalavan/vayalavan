package httpx

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vayal-mikrogreenz/vm-go-common/logging"
)

// discardLogger keeps test output readable while still exercising every
// logging path in the handlers under test.
func discardLogger() *slog.Logger {
	return logging.NewTo(io.Discard, "test-service", "debug")
}

func TestHealthz(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, HealthzPath, nil)
	rec := httptest.NewRecorder()

	Healthz("vm-test-api")(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var body struct {
		Status  string `json:"status"`
		Service string `json:"service"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not valid JSON: %v (body %q)", err, rec.Body.String())
	}
	if body.Status != "ok" {
		t.Errorf("status = %q, want %q", body.Status, "ok")
	}
	if body.Service != "vm-test-api" {
		t.Errorf("service = %q, want %q", body.Service, "vm-test-api")
	}
}

func TestReadyz(t *testing.T) {
	healthy := func(context.Context) error { return nil }
	broken := func(context.Context) error { return errors.New("connection refused") }

	tests := []struct {
		name       string
		checks     []Check
		wantStatus int
		wantLabel  string
		wantChecks map[string]string
	}{
		{
			name:       "no dependencies is ready",
			checks:     nil,
			wantStatus: http.StatusOK,
			wantLabel:  "ok",
			wantChecks: map[string]string{},
		},
		{
			name:       "all healthy",
			checks:     []Check{{Name: "postgres", Probe: healthy}},
			wantStatus: http.StatusOK,
			wantLabel:  "ok",
			wantChecks: map[string]string{"postgres": "ok"},
		},
		{
			name:       "one failure makes the service unready",
			checks:     []Check{{Name: "postgres", Probe: broken}},
			wantStatus: http.StatusServiceUnavailable,
			wantLabel:  "unavailable",
			wantChecks: map[string]string{"postgres": "failed"},
		},
		{
			name: "healthy checks still report alongside a failure",
			checks: []Check{
				{Name: "postgres", Probe: healthy},
				{Name: "storage", Probe: broken},
			},
			wantStatus: http.StatusServiceUnavailable,
			wantLabel:  "unavailable",
			wantChecks: map[string]string{"postgres": "ok", "storage": "failed"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, ReadyzPath, nil)
			rec := httptest.NewRecorder()

			Readyz("vm-test-api", discardLogger(), tc.checks...)(rec, req)

			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tc.wantStatus)
			}

			var body struct {
				Status string            `json:"status"`
				Checks map[string]string `json:"checks"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("response is not valid JSON: %v", err)
			}
			if body.Status != tc.wantLabel {
				t.Errorf("status = %q, want %q", body.Status, tc.wantLabel)
			}
			for name, want := range tc.wantChecks {
				if got := body.Checks[name]; got != want {
					t.Errorf("checks[%q] = %q, want %q", name, got, want)
				}
			}
		})
	}
}

// TestReadyzDoesNotLeakErrorDetail pins that a failing probe reports only
// "failed". The underlying error can name internal hosts and credentials,
// and /readyz is reachable without the internal token.
func TestReadyzDoesNotLeakErrorDetail(t *testing.T) {
	secret := "postgres://vm_orders:hunter2@db.internal:5432/vayal"
	probe := func(context.Context) error { return errors.New("dial failed: " + secret) }

	req := httptest.NewRequest(http.MethodGet, ReadyzPath, nil)
	rec := httptest.NewRecorder()

	Readyz("vm-test-api", discardLogger(), Check{Name: "postgres", Probe: probe})(rec, req)

	if body := rec.Body.String(); strings.Contains(body, "hunter2") ||
		strings.Contains(body, "db.internal") {
		t.Errorf("readiness response leaked connection detail: %s", body)
	}
}

func TestWriteErrorRendersStandardShape(t *testing.T) {
	tests := []struct {
		name        string
		err         error
		wantStatus  int
		wantCode    string
		wantMessage string
		wantDetails bool
	}{
		{
			name:        "bad request",
			err:         BadRequest("Request body is not valid JSON."),
			wantStatus:  http.StatusBadRequest,
			wantCode:    CodeBadRequest,
			wantMessage: "Request body is not valid JSON.",
		},
		{
			name:        "not found",
			err:         NotFound("No such product."),
			wantStatus:  http.StatusNotFound,
			wantCode:    CodeNotFound,
			wantMessage: "No such product.",
		},
		{
			name:        "forbidden",
			err:         Forbidden("You do not own this product."),
			wantStatus:  http.StatusForbidden,
			wantCode:    CodeForbidden,
			wantMessage: "You do not own this product.",
		},
		{
			name: "validation carries details",
			err: Validation("Some fields are invalid.", map[string]any{
				"price_rupees": "must have at most 2 decimal places",
			}),
			wantStatus:  http.StatusUnprocessableEntity,
			wantCode:    CodeValidation,
			wantMessage: "Some fields are invalid.",
			wantDetails: true,
		},
		{
			name:       "conflict uses a domain-specific code",
			err:        Conflict("STOCK_EXHAUSTED", "This product just sold out."),
			wantStatus: http.StatusConflict,
			wantCode:   "STOCK_EXHAUSTED",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			WriteError(context.Background(), rec, discardLogger(), tc.err)

			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tc.wantStatus)
			}
			if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
				t.Errorf("Content-Type = %q, want application/json", ct)
			}

			var body struct {
				Error struct {
					Code    string         `json:"code"`
					Message string         `json:"message"`
					Details map[string]any `json:"details"`
				} `json:"error"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("response is not valid JSON: %v (body %q)", err, rec.Body.String())
			}

			if body.Error.Code != tc.wantCode {
				t.Errorf("error.code = %q, want %q", body.Error.Code, tc.wantCode)
			}
			if tc.wantMessage != "" && body.Error.Message != tc.wantMessage {
				t.Errorf("error.message = %q, want %q", body.Error.Message, tc.wantMessage)
			}
			if tc.wantDetails && len(body.Error.Details) == 0 {
				t.Error("error.details is empty, want populated")
			}
			if !tc.wantDetails && body.Error.Details != nil {
				t.Errorf("error.details = %v, want omitted", body.Error.Details)
			}
		})
	}
}

// TestWriteErrorDoesNotLeakInternalDetail is the important one: an
// unrecognised error must never reach the customer as-is. CLAUDE.md rule 8
// fixes the wire shape, and a raw fmt.Errorf carrying a connection string
// would satisfy the shape while leaking the credential.
func TestWriteErrorDoesNotLeakInternalDetail(t *testing.T) {
	secret := "pq: password authentication failed for user \"vm_orders\" at db.internal"
	rec := httptest.NewRecorder()

	WriteError(context.Background(), rec, discardLogger(), errors.New(secret))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}

	body := rec.Body.String()
	if strings.Contains(body, "db.internal") || strings.Contains(body, "vm_orders") {
		t.Errorf("500 response leaked internal detail: %s", body)
	}

	var parsed struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(body), &parsed); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
	if parsed.Error.Code != CodeInternal {
		t.Errorf("error.code = %q, want %q", parsed.Error.Code, CodeInternal)
	}
}

func TestErrorUnwrap(t *testing.T) {
	sentinel := errors.New("underlying failure")
	err := Internal(sentinel)

	if !errors.Is(err, sentinel) {
		t.Error("errors.Is could not find the wrapped cause")
	}

	var appErr *Error
	if !errors.As(error(err), &appErr) {
		t.Fatal("errors.As could not extract *Error")
	}
	if appErr.Status != http.StatusInternalServerError {
		t.Errorf("Status = %d, want 500", appErr.Status)
	}
}

func TestRequestIDMiddleware(t *testing.T) {
	t.Run("mints an id when the header is absent", func(t *testing.T) {
		var seen string
		handler := RequestID(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			seen = logging.RequestIDFrom(r.Context())
		}))

		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

		if seen == "" {
			t.Error("no request id was placed on the context")
		}
		if got := rec.Header().Get(logging.RequestIDHeader); got != seen {
			t.Errorf("echoed header = %q, want %q", got, seen)
		}
	})

	t.Run("adopts the inbound id so traces span services", func(t *testing.T) {
		const inbound = "abc123-from-the-gateway"
		var seen string
		handler := RequestID(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			seen = logging.RequestIDFrom(r.Context())
		}))

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set(logging.RequestIDHeader, inbound)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if seen != inbound {
			t.Errorf("context id = %q, want the inbound %q", seen, inbound)
		}
		if got := rec.Header().Get(logging.RequestIDHeader); got != inbound {
			t.Errorf("echoed header = %q, want %q", got, inbound)
		}
	})
}

func TestRecovererConvertsPanicToStandardError(t *testing.T) {
	handler := Recoverer(discardLogger())(http.HandlerFunc(
		func(http.ResponseWriter, *http.Request) {
			panic("something went badly wrong in a handler")
		}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}

	body := rec.Body.String()
	// The panic message may name internals; it belongs in the log only.
	if strings.Contains(body, "something went badly wrong") {
		t.Errorf("panic message leaked into the response: %s", body)
	}

	var parsed struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(body), &parsed); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
	if parsed.Error.Code != CodeInternal {
		t.Errorf("error.code = %q, want %q", parsed.Error.Code, CodeInternal)
	}
}

func TestInternalAuth(t *testing.T) {
	const token = "the-shared-internal-token"

	newHandler := func() http.Handler {
		return InternalAuth(token, discardLogger())(http.HandlerFunc(
			func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))
	}

	tests := []struct {
		name       string
		path       string
		header     string
		wantStatus int
	}{
		{"correct token passes", "/v1/orders", token, http.StatusOK},
		{"wrong token is rejected", "/v1/orders", "guessed-token", http.StatusUnauthorized},
		{"missing token is rejected", "/v1/orders", "", http.StatusUnauthorized},
		// Probes must work without the secret, or an orchestrator cannot
		// tell a healthy service from a misconfigured one.
		{"healthz is exempt", HealthzPath, "", http.StatusOK},
		{"readyz is exempt", ReadyzPath, "", http.StatusOK},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			if tc.header != "" {
				req.Header.Set(InternalTokenHeader, tc.header)
			}
			rec := httptest.NewRecorder()
			newHandler().ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tc.wantStatus)
			}
		})
	}
}
