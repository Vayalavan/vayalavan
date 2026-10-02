package analyticsevents_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vayal-mikrogreenz/vm-go-common/config"
	"github.com/vayal-mikrogreenz/vm-go-common/httpx"
	"github.com/vayal-mikrogreenz/vm-go-common/logging"

	"github.com/vayal-mikrogreenz/vm-profile-api/internal/analyticsevents"
	"github.com/vayal-mikrogreenz/vm-profile-api/internal/app"
	"github.com/vayal-mikrogreenz/vm-profile-api/internal/store"
)

func ptr[T any](v T) *T { return &v }

// TestSupplierEventCarriesNoPersonalData — the payload's keys are an
// allow-list, and none of the contact, tax, address or bank values in the
// supplier row reach it.
func TestSupplierEventCarriesNoPersonalData(t *testing.T) {
	row := store.Supplier{
		ID: uuid.New(), UserID: uuid.New(), BusinessName: "Kaveri Greens",
		ContactName: "Meena Raman", Phone: "9876543210", Email: "meena@example.com",
		Gstin: ptr("33AAAAA0000A1Z5"), Pan: ptr("AAAAA0000A"),
		AddressLine1: ptr("12 Temple Street"), AddressLine2: ptr("Near the tank"),
		City: ptr("Kumbakonam"), State: ptr("Tamil Nadu"), Pincode: ptr("612001"),
		BankAccountName: ptr("Meena R"), BankAccountNumber: ptr("001234567890"),
		BankIfsc: ptr("SBIN0001234"), Status: "rejected",
		RejectionReason: ptr("Meena's FSSAI licence expired"),
		CommissionBps:   ptr(int32(250)), CreatedAt: time.Now(),
	}
	raw, err := json.Marshal(analyticsevents.Build(row))
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"Meena", "9876543210", "example.com", "33AAAAA",
		"AAAAA0000A", "Temple", "tank", "001234567890", "SBIN", "FSSAI"} {
		if strings.Contains(string(raw), secret) {
			t.Errorf("payload leaks %q: %s", secret, raw)
		}
	}

	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		t.Fatal(err)
	}
	allow := map[string]bool{}
	for _, k := range []string{"supplier_id", "business_name", "status", "city", "state",
		"pincode", "gst_registered", "commission_bps", "approved_at", "created_at"} {
		allow[k] = true
	}
	var unexpected []string
	for k := range top {
		if !allow[k] {
			unexpected = append(unexpected, k)
		}
	}
	sort.Strings(unexpected)
	if len(unexpected) > 0 {
		t.Errorf("payload has keys outside the allow-list: %v — adding a field is a "+
			"decision about what the analytics role may see", unexpected)
	}
	if !strings.Contains(string(raw), `"gst_registered":true`) ||
		!strings.Contains(string(raw), `"commission_bps":250`) {
		t.Errorf("payload = %s", raw)
	}
}

// TestApprovalWritesOneEvent — approving a supplier writes exactly one
// supplier.approved event, in the approval's transaction, carrying the state
// after it; refusing an unknown supplier writes none.
func TestApprovalWritesOneEvent(t *testing.T) {
	if err := config.LoadRootDotEnv(); err != nil {
		t.Skipf("no root .env: %v", err)
	}
	url := os.Getenv("PROFILE_DATABASE_URL")
	if url == "" {
		t.Skip("PROFILE_DATABASE_URL is not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil || pool.Ping(ctx) != nil {
		t.Skip("database unreachable")
	}
	// Registered first so it runs LAST: a deferred Close would run before the
	// cleanups below and leave them talking to a closed pool.
	t.Cleanup(pool.Close)

	// The audit row the approval writes references a real admin user.
	var adminID, userID, supplierID uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO users (email, role, status)
		VALUES ($1, 'admin', 'active') RETURNING id`,
		"event-admin-"+uuid.NewString()[:8]+"@vayal.test").Scan(&adminID); err != nil {
		t.Fatalf("seeding admin: %v", err)
	}
	email := "supplier-event-" + uuid.NewString()[:8] + "@vayal.test"
	if err := pool.QueryRow(ctx, `INSERT INTO users (email, role, status)
		VALUES ($1, 'supplier', 'pending') RETURNING id`, email).Scan(&userID); err != nil {
		t.Fatalf("seeding user: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO suppliers
		(user_id, business_name, contact_name, phone, email, city, status)
		VALUES ($1, 'Event Probe Farms', 'Probe Person', '9000000000', $2, 'Madurai', 'pending')
		RETURNING id`, userID, email).Scan(&supplierID); err != nil {
		t.Fatalf("seeding supplier: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM supplier_events_outbox WHERE aggregate_id = $1`, supplierID)
		_, _ = pool.Exec(ctx, `DELETE FROM admin_audit_log WHERE entity_id = $1`, supplierID)
		_, _ = pool.Exec(ctx, `DELETE FROM suppliers WHERE id = $1`, supplierID)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id = ANY($1)`, []uuid.UUID{userID, adminID})
	})

	router := app.NewRouter(app.Config{InternalToken: "event-test-token"}, pool,
		logging.NewTo(io.Discard, app.ServiceName, "error"))
	approve := func(id uuid.UUID) int {
		req := httptest.NewRequest(http.MethodPost, "/admin/suppliers/"+id.String()+"/approve",
			strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(httpx.InternalTokenHeader, "event-test-token")
		req.Header.Set(httpx.UserRoleHeader, httpx.RoleAdmin)
		req.Header.Set(httpx.UserIDHeader, adminID.String())
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec.Code
	}
	if code := approve(supplierID); code != http.StatusOK {
		t.Fatalf("approve: got %d", code)
	}
	if code := approve(uuid.New()); code != http.StatusNotFound {
		t.Errorf("approving an unknown supplier: got %d, want 404", code)
	}

	rows, err := pool.Query(ctx, `SELECT event_type, actor_role, payload
		FROM supplier_events_outbox WHERE aggregate_id = $1`, supplierID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var count int
	for rows.Next() {
		count++
		var eventType, actor string
		var payload map[string]any
		if err := rows.Scan(&eventType, &actor, &payload); err != nil {
			t.Fatal(err)
		}
		if eventType != "supplier.approved" || actor != "admin" ||
			payload["status"] != "approved" || payload["approved_at"] == nil ||
			payload["business_name"] != "Event Probe Farms" {
			t.Errorf("event = %s by %s: %v", eventType, actor, payload)
		}
	}
	if count != 1 {
		t.Errorf("got %d events, want 1", count)
	}
}
