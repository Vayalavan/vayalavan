package app

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/vayal-mikrogreenz/vm-go-common/httpx"
	"github.com/vayal-mikrogreenz/vm-go-common/logging"
)

// TestHealthzServesWithoutDatabase asserts that liveness is genuinely
// dependency-free.
//
// The pool is deliberately nil. If /healthz ever grew a database call, this
// test would panic rather than pass — which is the point: liveness answers
// "should this process be restarted?", and a Postgres outage must not make
// every API instance report itself dead and trigger a restart storm.
//
// It also lets /healthz be verified without a running database, which is how
// the foundation is checked before the stack is up.
func TestHealthzServesWithoutDatabase(t *testing.T) {
	logger := logging.NewTo(io.Discard, ServiceName, "debug")
	router := NewRouter(Config{InternalToken: "test-token"}, nil, nil, logger)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, httpx.HealthzPath, nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s = %d, want %d (body %q)",
			httpx.HealthzPath, rec.Code, http.StatusOK, rec.Body.String())
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
	if body.Service != ServiceName {
		t.Errorf("service = %q, want %q", body.Service, ServiceName)
	}
}

// TestHealthzNeedsNoInternalToken pins the exemption that lets an
// orchestrator probe this service without holding the shared secret.
func TestHealthzNeedsNoInternalToken(t *testing.T) {
	logger := logging.NewTo(io.Discard, ServiceName, "debug")
	router := NewRouter(Config{InternalToken: "the-real-token"}, nil, nil, logger)

	req := httptest.NewRequest(http.MethodGet, httpx.HealthzPath, nil)
	// No X-Internal-Token header at all.
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("GET %s without a token = %d, want %d",
			httpx.HealthzPath, rec.Code, http.StatusOK)
	}
}

// TestDomainRoutesRequireInternalToken guards CLAUDE.md rule 5: these
// services are not publicly callable. There are no domain routes yet, so an
// unauthenticated request must be rejected by the auth middleware (401)
// rather than reaching the router's 404.
func TestDomainRoutesRequireInternalToken(t *testing.T) {
	logger := logging.NewTo(io.Discard, ServiceName, "debug")
	router := NewRouter(Config{InternalToken: "the-real-token"}, nil, nil, logger)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/anything", nil))

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("unauthenticated request = %d, want %d",
			rec.Code, http.StatusUnauthorized)
	}
}
