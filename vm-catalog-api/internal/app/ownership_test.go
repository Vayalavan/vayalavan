package app

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/vayal-mikrogreenz/vm-go-common/httpx"
	"github.com/vayal-mikrogreenz/vm-go-common/logging"
)

const testToken = "test-internal-token"

func testRouter() http.Handler {
	logger := logging.NewTo(io.Discard, ServiceName, "debug")
	// nil pool and nil storage: every case below is rejected by middleware
	// before a handler can touch either. If one ever is not, this panics —
	// which is the correct way to find out.
	return NewRouter(Config{InternalToken: testToken}, nil, nil, logger)
}

// request builds a call carrying the gateway's internal token plus whatever
// identity headers the case is testing.
func request(method, path, role, userID, supplierID string) *http.Request {
	req := httptest.NewRequest(method, path, nil)
	req.Header.Set(httpx.InternalTokenHeader, testToken)
	if role != "" {
		req.Header.Set(httpx.UserRoleHeader, role)
		req.Header.Set(httpx.UserIDHeader, userID)
	}
	if supplierID != "" {
		req.Header.Set(httpx.SupplierIDHeader, supplierID)
	}
	return req
}

// TestSupplierRoutesRejectOtherRoles is the service-layer half of CLAUDE.md
// §7. The gateway guards these routes too; this asserts that a routing
// mistake at the edge is not sufficient to reach supplier data.
func TestSupplierRoutesRejectOtherRoles(t *testing.T) {
	router := testRouter()
	userID := uuid.NewString()
	supplierID := uuid.NewString()

	supplierPaths := []struct{ method, path string }{
		{http.MethodGet, "/products/"},
		{http.MethodPost, "/products/"},
		{http.MethodGet, "/products/" + uuid.NewString()},
		{http.MethodPut, "/products/" + uuid.NewString()},
		{http.MethodPost, "/products/" + uuid.NewString() + "/archive"},
		{http.MethodPost, "/uploads/presign"},
		{http.MethodGet, "/imports/"},
		{http.MethodPost, "/imports/"},
		{http.MethodPost, "/imports/" + uuid.NewString() + "/commit"},
	}

	for _, route := range supplierPaths {
		for _, role := range []string{httpx.RoleCustomer, httpx.RoleAdmin} {
			t.Run(route.method+" "+route.path+" as "+role, func(t *testing.T) {
				rec := httptest.NewRecorder()
				router.ServeHTTP(rec, request(route.method, route.path, role, userID, supplierID))

				if rec.Code != http.StatusForbidden {
					t.Errorf("%s %s as %s = %d, want %d",
						route.method, route.path, role, rec.Code, http.StatusForbidden)
				}
			})
		}

		t.Run(route.method+" "+route.path+" unauthenticated", func(t *testing.T) {
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, request(route.method, route.path, "", "", ""))

			if rec.Code != http.StatusUnauthorized {
				t.Errorf("%s %s anonymous = %d, want %d",
					route.method, route.path, rec.Code, http.StatusUnauthorized)
			}
		})
	}
}

// TestAdminRoutesRejectOtherRoles guards the cross-supplier endpoints.
func TestAdminRoutesRejectOtherRoles(t *testing.T) {
	router := testRouter()
	userID := uuid.NewString()

	adminPaths := []struct{ method, path string }{
		{http.MethodGet, "/admin/products"},
		{http.MethodGet, "/admin/products/" + uuid.NewString()},
		{http.MethodPost, "/admin/products/" + uuid.NewString() + "/archive"},
	}

	for _, route := range adminPaths {
		for _, role := range []string{httpx.RoleCustomer, httpx.RoleSupplier} {
			t.Run(route.path+" as "+role, func(t *testing.T) {
				rec := httptest.NewRecorder()
				router.ServeHTTP(rec, request(route.method, route.path, role, userID, uuid.NewString()))

				if rec.Code != http.StatusForbidden {
					t.Errorf("%s %s as %s = %d, want %d",
						route.method, route.path, role, rec.Code, http.StatusForbidden)
				}
			})
		}
	}
}

// TestSupplierTokenWithoutSupplierIDFailsClosed covers the state where a
// supplier account exists but has no supplier row: the token carries the
// supplier role and no supplier_id.
//
// Every product query is scoped by supplier id. Without this guard the zero
// UUID would be used as a filter — matching nothing today, but silently
// matching whatever ends up with that id tomorrow. It must fail, not fall
// through.
func TestSupplierTokenWithoutSupplierIDFailsClosed(t *testing.T) {
	router := testRouter()

	rec := httptest.NewRecorder()
	// Supplier role, but no X-Supplier-Id header.
	router.ServeHTTP(rec, request(http.MethodGet, "/products/",
		httpx.RoleSupplier, uuid.NewString(), ""))

	if rec.Code != http.StatusForbidden {
		t.Errorf("supplier without a supplier id = %d, want %d (must fail closed)",
			rec.Code, http.StatusForbidden)
	}
}

// TestHealthzNeedsNoTokenOrRole keeps the probes reachable by an orchestrator
// that holds neither the internal token nor an identity.
func TestHealthzNeedsNoTokenOrRole(t *testing.T) {
	logger := logging.NewTo(io.Discard, ServiceName, "debug")
	router := NewRouter(Config{InternalToken: testToken}, nil, nil, logger)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, httpx.HealthzPath, nil))

	if rec.Code != http.StatusOK {
		t.Errorf("GET %s = %d, want %d", httpx.HealthzPath, rec.Code, http.StatusOK)
	}
}
