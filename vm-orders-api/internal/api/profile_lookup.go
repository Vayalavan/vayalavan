package api

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/vayal-mikrogreenz/vm-go-common/httpx"

	"github.com/vayal-mikrogreenz/vm-go-common/logging"
	"github.com/vayal-mikrogreenz/vm-orders-api/internal/store"
)

// bankDetails is one supplier's settlement information.
type bankDetails struct {
	BusinessName  string `json:"business_name"`
	AccountName   string `json:"bank_account_name"`
	AccountNumber string `json:"bank_account_number"`
	IFSC          string `json:"bank_ifsc"`
}

// supplierNames resolves business names for admin screens.
//
// Names only. Best-effort: a failed lookup leaves rows labelled by id rather
// than failing the whole settlement screen, because the amounts — which is
// what the admin is actually reconciling — are ours and already correct.
func (a *API) supplierNames(ctx context.Context, ids []uuid.UUID) map[uuid.UUID]string {
	names := map[uuid.UUID]string{}
	if len(ids) == 0 {
		return names
	}

	var body struct {
		Suppliers []struct {
			SupplierID   string `json:"supplier_id"`
			BusinessName string `json:"business_name"`
		} `json:"suppliers"`
	}
	if err := a.callProfile(ctx, "/internal/suppliers/directory", uuid.Nil, ids, &body); err != nil {
		a.logger.WarnContext(ctx, "could not resolve supplier names", slog.Any("error", err))
		return names
	}
	for _, entry := range body.Suppliers {
		if id, err := uuid.Parse(entry.SupplierID); err == nil {
			names[id] = entry.BusinessName
		}
	}
	return names
}

// CustomerContact is where an order confirmation can be sent.
type CustomerContact struct {
	Email string
	Name  string
}

// customerContacts resolves email addresses for the outbox dispatcher.
//
// Best-effort in shape but NOT in consequence: a customer missing from the
// response has no address on file (phone-only signup), and the dispatcher
// treats that as "nothing to send" rather than as a failure to retry forever.
// A transport error, by contrast, comes back as an error so the row is
// released and tried again.
func (a *API) customerContacts(
	ctx context.Context, ids []uuid.UUID,
) (map[uuid.UUID]CustomerContact, error) {
	contacts := map[uuid.UUID]CustomerContact{}
	if len(ids) == 0 {
		return contacts, nil
	}

	var body struct {
		Customers []struct {
			CustomerID string `json:"customer_id"`
			Email      string `json:"email"`
			Name       string `json:"name"`
		} `json:"customers"`
	}
	if err := a.callProfileCustomers(ctx, "/internal/customers/contacts", ids, &body); err != nil {
		return nil, err
	}
	for _, entry := range body.Customers {
		if id, err := uuid.Parse(entry.CustomerID); err == nil && entry.Email != "" {
			contacts[id] = CustomerContact{Email: entry.Email, Name: entry.Name}
		}
	}
	return contacts, nil
}

// callProfileCustomers POSTs a customer id set to vm-profile-api.
//
// A near-twin of callProfile, which posts `supplier_ids`. Kept separate rather
// than generalised over the JSON key: one string parameter deciding what a
// request means is exactly the kind of call site that gets passed the wrong
// literal and asks profile for suppliers when it wanted customers.
func (a *API) callProfileCustomers(
	ctx context.Context, path string, ids []uuid.UUID, out any,
) error {
	unique := make([]string, 0, len(ids))
	seen := map[uuid.UUID]bool{}
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			unique = append(unique, id.String())
		}
	}

	payload, err := json.Marshal(map[string]any{"customer_ids": unique})
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		a.profileURL+path, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(httpx.InternalTokenHeader, a.internalTok)
	if id := logging.RequestIDFrom(ctx); id != "" {
		req.Header.Set(logging.RequestIDHeader, id)
	}

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return httpx.Unavailable("vm-profile-api returned an unexpected status.")
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// supplierBankDetails fetches UNMASKED bank details for the NEFT worksheet.
//
// Unlike supplierNames this is NOT best-effort: an export with blank account
// numbers would be silently useless, and the admin would only find out at the
// bank. The admin's id is forwarded so profile can audit the release on its
// own side too (CLAUDE.md §5.1).
func (a *API) supplierBankDetails(
	ctx context.Context, adminID uuid.UUID, ids []uuid.UUID,
) (map[uuid.UUID]bankDetails, error) {
	details := map[uuid.UUID]bankDetails{}
	if len(ids) == 0 {
		return details, nil
	}

	var body struct {
		Suppliers []struct {
			SupplierID string `json:"supplier_id"`
			bankDetails
		} `json:"suppliers"`
	}
	if err := a.callProfile(ctx, "/internal/suppliers/bank-details", adminID, ids, &body); err != nil {
		return nil, httpx.Unavailable(
			"Could not fetch supplier bank details. The export was not produced.")
	}
	for _, entry := range body.Suppliers {
		if id, err := uuid.Parse(entry.SupplierID); err == nil {
			details[id] = entry.bankDetails
		}
	}
	return details, nil
}

// callProfile POSTs a supplier id set to vm-profile-api.
func (a *API) callProfile(
	ctx context.Context, path string, adminID uuid.UUID, ids []uuid.UUID, out any,
) error {
	unique := make([]string, 0, len(ids))
	seen := map[uuid.UUID]bool{}
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			unique = append(unique, id.String())
		}
	}

	payload, err := json.Marshal(map[string]any{"supplier_ids": unique})
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		a.profileURL+path, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(httpx.InternalTokenHeader, a.internalTok)
	// Forwarded so profile can apply its own admin guard and audit the access
	// against a real person.
	if adminID != uuid.Nil {
		req.Header.Set(httpx.UserIDHeader, adminID.String())
		req.Header.Set(httpx.UserRoleHeader, httpx.RoleAdmin)
	}
	if id := logging.RequestIDFrom(ctx); id != "" {
		req.Header.Set(logging.RequestIDHeader, id)
	}

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return httpx.Unavailable("vm-profile-api returned an unexpected status.")
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// pendingSupplierApplications reads the dashboard tile from vm-profile-api.
//
// Best-effort: a zero here is visibly a zero, and the rest of the dashboard is
// still worth showing if profile is briefly unreachable.
func (a *API) pendingSupplierApplications(ctx context.Context) int64 {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		a.profileURL+"/internal/suppliers/pending-count", nil)
	if err != nil {
		return 0
	}
	req.Header.Set(httpx.InternalTokenHeader, a.internalTok)
	if id := logging.RequestIDFrom(ctx); id != "" {
		req.Header.Set(logging.RequestIDHeader, id)
	}

	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		a.logger.WarnContext(ctx, "could not read pending supplier applications",
			slog.Any("error", err))
		return 0
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0
	}

	var body struct {
		PendingApplications int64 `json:"pending_applications"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return 0
	}
	return body.PendingApplications
}

// supplierCommissions resolves each supplier's commission rate in basis points.
//
// MUST be called BEFORE opening the payout transaction, never inside it. This
// is an HTTP round trip to another service; making it from inside a database
// transaction would hold a pooled connection open for the duration of a
// network call, and a slow profile-api would drain the pool while Postgres sat
// idle holding locks on the orders being paid.
//
// Suppliers absent from the response, or carrying a null rate, fall back to
// the platform default — which is also what happens if profile-api is
// unreachable. That fallback is deliberate and safe in one direction only:
// paying a supplier the standard rate when a negotiated one existed is a
// correctable accounting error, whereas failing the payment because a lookup
// timed out would leave stock committed against an order that never completes.
// The fallback is logged loudly so it can be reconciled.
func (a *API) supplierCommissions(
	ctx context.Context, ids []uuid.UUID,
) map[uuid.UUID]int64 {
	rates := map[uuid.UUID]int64{}
	if len(ids) == 0 {
		return rates
	}

	var body struct {
		Suppliers []struct {
			SupplierID    string `json:"supplier_id"`
			CommissionBPS *int64 `json:"commission_bps"`
		} `json:"suppliers"`
	}
	if err := a.callProfile(ctx, "/internal/suppliers/commissions", uuid.Nil, ids, &body); err != nil {
		a.logger.ErrorContext(ctx,
			"could not resolve supplier commission rates — falling back to the platform default",
			slog.Any("error", err), slog.Int("suppliers", len(ids)),
			slog.String("alert", "commission_lookup_failed"))
		return rates
	}

	for _, entry := range body.Suppliers {
		id, err := uuid.Parse(entry.SupplierID)
		if err != nil {
			continue
		}
		// A null rate is not an override; leaving it out of the map is how the
		// caller learns to use the default.
		if entry.CommissionBPS != nil {
			rates[id] = *entry.CommissionBPS
		}
	}
	return rates
}

// supplierIDsOnOrder lists the distinct suppliers with lines on an order.
//
// Read outside the payout transaction so the commission lookup can happen
// before it opens. Reading it twice — once here, once inside — is cheap and
// keeps the network call off the transaction entirely.
func supplierIDsOnOrder(
	ctx context.Context, q *store.Queries, orderID uuid.UUID,
) []uuid.UUID {
	shares, err := q.SumOrderItemsBySupplier(ctx, orderID)
	if err != nil {
		// The caller falls back to the platform default for anything it
		// cannot resolve, so an empty list is safe rather than fatal.
		return nil
	}
	ids := make([]uuid.UUID, 0, len(shares))
	for _, share := range shares {
		ids = append(ids, share.SupplierID)
	}
	return ids
}
