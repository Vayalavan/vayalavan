package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vayal-mikrogreenz/vm-go-common/httpx"

	"github.com/vayal-mikrogreenz/vm-profile-api/internal/analyticsevents"
	"github.com/vayal-mikrogreenz/vm-profile-api/internal/auth"
	"github.com/vayal-mikrogreenz/vm-profile-api/internal/mail"
	"github.com/vayal-mikrogreenz/vm-profile-api/internal/store"
)

// passwordSetTokenTTL bounds how long an admin-issued onboarding link works.
const passwordSetTokenTTL = 72 * time.Hour

type supplierResponse struct {
	ID           string  `json:"id"`
	UserID       string  `json:"user_id"`
	BusinessName string  `json:"business_name"`
	ContactName  string  `json:"contact_name"`
	Phone        string  `json:"phone"`
	Email        string  `json:"email"`
	GSTIN        *string `json:"gstin"`
	PAN          *string `json:"pan"`

	AddressLine1 *string `json:"address_line1"`
	AddressLine2 *string `json:"address_line2"`
	City         *string `json:"city"`
	State        *string `json:"state"`
	Pincode      *string `json:"pincode"`

	BankAccountName   *string `json:"bank_account_name"`
	BankAccountNumber *string `json:"bank_account_number"`
	BankIFSC          *string `json:"bank_ifsc"`

	Status string `json:"status"`
	// CommissionBPS is null when this supplier uses the platform default.
	// The effective rate is resolved by vm-orders-api, which owns the default.
	CommissionBPS   *int32  `json:"commission_bps"`
	RejectionReason *string `json:"rejection_reason"`
	ApprovedAt      *string `json:"approved_at"`
	CreatedAt       string  `json:"created_at"`
}

// maskAccountNumber reduces an account number to its last four digits.
//
// CLAUDE.md §5.1: bank details are masked everywhere except the admin payout
// screen. Masking at the serialisation boundary means a new endpoint cannot
// leak them by forgetting to.
func maskAccountNumber(number *string) *string {
	if number == nil {
		return nil
	}
	digits := strings.TrimSpace(*number)
	if len(digits) <= 4 {
		masked := strings.Repeat("•", len(digits))
		return &masked
	}
	masked := strings.Repeat("•", len(digits)-4) + digits[len(digits)-4:]
	return &masked
}

// toSupplierResponse serialises a supplier. revealBank must be true ONLY for
// the admin payout screen.
func toSupplierResponse(s store.Supplier, revealBank bool) supplierResponse {
	resp := supplierResponse{
		ID:                s.ID.String(),
		UserID:            s.UserID.String(),
		BusinessName:      s.BusinessName,
		ContactName:       s.ContactName,
		Phone:             s.Phone,
		Email:             s.Email,
		GSTIN:             s.Gstin,
		PAN:               s.Pan,
		AddressLine1:      s.AddressLine1,
		AddressLine2:      s.AddressLine2,
		City:              s.City,
		State:             s.State,
		Pincode:           s.Pincode,
		BankAccountName:   s.BankAccountName,
		BankAccountNumber: maskAccountNumber(s.BankAccountNumber),
		BankIFSC:          s.BankIfsc,
		Status:            s.Status,
		CommissionBPS:     s.CommissionBps,
		RejectionReason:   s.RejectionReason,
		CreatedAt:         s.CreatedAt.Format(time.RFC3339),
	}
	if revealBank {
		resp.BankAccountNumber = s.BankAccountNumber
	}
	if s.ApprovedAt != nil {
		formatted := s.ApprovedAt.Format(time.RFC3339)
		resp.ApprovedAt = &formatted
	}
	return resp
}

// ---------------------------------------------------------------------------
// POST /suppliers/apply — public self-service application
// ---------------------------------------------------------------------------

type supplierApplyRequest struct {
	BusinessName string `json:"business_name"`
	ContactName  string `json:"contact_name"`
	Phone        string `json:"phone"`
	Email        string `json:"email"`
	Password     string `json:"password"`
	GSTIN        string `json:"gstin"`
	PAN          string `json:"pan"`
	AddressLine1 string `json:"address_line1"`
	AddressLine2 string `json:"address_line2"`
	City         string `json:"city"`
	State        string `json:"state"`
	Pincode      string `json:"pincode"`
}

// ApplyAsSupplier creates a pending supplier and its pending user.
//
// No session is returned: the account cannot sign in until an admin approves
// it, so issuing tokens would only produce a login that immediately 403s.
func (a *API) ApplyAsSupplier(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req supplierApplyRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		a.fail(ctx, w, err)
		return
	}

	v := newValidation()
	businessName := v.require("business_name", req.BusinessName, 2, 200)
	contactName := v.require("contact_name", req.ContactName, 1, 120)
	email := v.email("email", req.Email)
	phone := v.phone("phone", req.Phone)
	v.password("password", req.Password)
	gstin := v.optional(req.GSTIN, 20)
	pan := v.optional(req.PAN, 10)
	line1 := v.optional(req.AddressLine1, 200)
	line2 := v.optional(req.AddressLine2, 200)
	city := v.optional(req.City, 80)
	state := v.optional(req.State, 80)

	var pincode *string
	if strings.TrimSpace(req.Pincode) != "" {
		p := v.pincode("pincode", req.Pincode)
		pincode = &p
	}
	if err := v.err(); err != nil {
		a.fail(ctx, w, err)
		return
	}

	hash, err := auth.HashPassword(req.Password, a.bcryptCost)
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	tx, err := a.pool.Begin(ctx)
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit
	q := a.queries.WithTx(tx)

	user, err := q.CreateUser(ctx, store.CreateUserParams{
		Email:        &email,
		Phone:        &phone,
		PasswordHash: &hash,
		Role:         auth.RoleSupplier,
		// Pending until an admin approves — the account exists but cannot
		// sign in, which is exactly the state an application should leave.
		Status:        statusPending,
		EmailVerified: false,
	})
	if err != nil {
		if isUniqueViolation(err) {
			a.fail(ctx, w, httpx.Conflict("ACCOUNT_EXISTS",
				"An account with this email or phone number already exists."))
			return
		}
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	supplier, err := q.CreateSupplier(ctx, store.CreateSupplierParams{
		UserID:       user.ID,
		BusinessName: businessName,
		ContactName:  contactName,
		Phone:        phone,
		Email:        email,
		Gstin:        gstin,
		Pan:          pan,
		AddressLine1: line1,
		AddressLine2: line2,
		City:         city,
		State:        state,
		Pincode:      pincode,
		Status:       statusPending,
	})
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}
	if err := analyticsevents.Emit(ctx, q, supplier.ID, analyticsevents.SupplierApplied,
		analyticsevents.ActorSupplier, time.Now()); err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}
	if err := tx.Commit(ctx); err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	a.respond(ctx, w, http.StatusCreated, map[string]any{
		"supplier": toSupplierResponse(supplier, false),
		"message":  "Application received. We'll email you once it has been reviewed.",
	})
}

// ---------------------------------------------------------------------------
// GET /internal/suppliers/approved-ids — service-to-service
// ---------------------------------------------------------------------------

// ApprovedSupplierIDs returns the ids of every supplier currently allowed to
// sell.
//
// vm-catalog-api uses this as the storefront allow-list. It lives here, not
// there, because supplier approval is profile's domain and CLAUDE.md §3
// forbids catalog reading the profile schema directly.
//
// Suspending a supplier therefore removes their produce from the storefront
// on the next refresh, with no catalog-side state to keep in sync.
func (a *API) ApprovedSupplierIDs(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	ids, err := a.queries.ListApprovedSupplierIDs(ctx)
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, id.String())
	}
	a.respond(ctx, w, http.StatusOK, map[string]any{"supplier_ids": out})
}

// ---------------------------------------------------------------------------
// GET /internal/suppliers/directory and /internal/suppliers/bank-details
// ---------------------------------------------------------------------------

type supplierIDsRequest struct {
	SupplierIDs []string `json:"supplier_ids"`
}

func parseSupplierIDs(raw []string) ([]uuid.UUID, error) {
	ids := make([]uuid.UUID, 0, len(raw))
	for _, value := range raw {
		id, err := uuid.Parse(value)
		if err != nil {
			return nil, httpx.BadRequest("supplier_ids must be UUIDs.")
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// SupplierDirectory returns business names for a set of supplier ids.
//
// Names only — no bank details, no contact details. Admin screens that just
// need to label a row use this, so the unmasked endpoint stays reserved for
// the one screen that genuinely needs it.
func (a *API) SupplierDirectory(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req supplierIDsRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		a.fail(ctx, w, err)
		return
	}
	ids, err := parseSupplierIDs(req.SupplierIDs)
	if err != nil {
		a.fail(ctx, w, err)
		return
	}

	rows, err := a.queries.ListSupplierNames(ctx, ids)
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	out := make([]map[string]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, map[string]string{
			"supplier_id": row.ID.String(), "business_name": row.BusinessName,
		})
	}
	a.respond(ctx, w, http.StatusOK, map[string]any{"suppliers": out})
}

// SupplierBankDetails returns UNMASKED bank details for the NEFT worksheet.
//
// CLAUDE.md §5.1 masks these everywhere except the admin payout screen. This
// endpoint is that exception, so every call is written to admin_audit_log HERE
// as well as on the calling side — the record must exist in the schema that
// owns the data, not only in the one that asked for it.
//
// The calling admin's id arrives in the forwarded identity headers; a call
// without one is refused rather than logged against nobody.
func (a *API) SupplierBankDetails(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	actor, err := httpx.RequireActor(ctx)
	if err != nil {
		a.fail(ctx, w, err)
		return
	}
	if !actor.IsAdmin() {
		a.fail(ctx, w, httpx.Forbidden("Only administrators may view bank details."))
		return
	}

	var req supplierIDsRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		a.fail(ctx, w, err)
		return
	}
	ids, err := parseSupplierIDs(req.SupplierIDs)
	if err != nil {
		a.fail(ctx, w, err)
		return
	}

	rows, err := a.queries.ListSupplierBankDetails(ctx, ids)
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	// Audited before the response is written.
	if err := a.writeAudit(ctx, a.queries, actor.UserID,
		"supplier.bank_details_released", "supplier", uuid.Nil, nil,
		map[string]any{
			"supplier_count": len(rows),
			"supplier_ids":   req.SupplierIDs,
			"reason":         "admin payout worksheet",
		}); err != nil {
		a.fail(ctx, w, err)
		return
	}

	a.logger.WarnContext(ctx, "unmasked supplier bank details released",
		slogAdmin(actor.UserID), slog.Int("suppliers", len(rows)))

	out := make([]map[string]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, map[string]string{
			"supplier_id":         row.ID.String(),
			"business_name":       row.BusinessName,
			"bank_account_name":   deref(row.BankAccountName),
			"bank_account_number": deref(row.BankAccountNumber),
			"bank_ifsc":           deref(row.BankIfsc),
		})
	}
	a.respond(ctx, w, http.StatusOK, map[string]any{"suppliers": out})
}

func deref(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

// PendingApplicationCount feeds the admin dashboard tile.
func (a *API) PendingApplicationCount(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	count, err := a.queries.CountPendingSupplierApplications(ctx)
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}
	a.respond(ctx, w, http.StatusOK, map[string]any{"pending_applications": count})
}

// ---------------------------------------------------------------------------
// GET /suppliers/{id} — own record, or any record for an admin
// ---------------------------------------------------------------------------

// GetSupplier returns a supplier record.
//
// A supplier may read only its own row (CLAUDE.md §7). Enforced here in the
// service layer, not only at the gateway.
func (a *API) GetSupplier(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	actor, err := httpx.RequireActor(ctx)
	if err != nil {
		a.fail(ctx, w, err)
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		a.fail(ctx, w, httpx.NotFound("Supplier not found."))
		return
	}

	supplier, err := a.queries.GetSupplierByID(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			a.fail(ctx, w, httpx.NotFound("Supplier not found."))
			return
		}
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	if !actor.IsAdmin() && supplier.UserID != actor.UserID {
		// 404 rather than 403: confirming the row exists would leak that a
		// competitor is registered on the platform.
		a.fail(ctx, w, httpx.NotFound("Supplier not found."))
		return
	}

	a.respond(ctx, w, http.StatusOK, toSupplierResponse(supplier, false))
}

// ---------------------------------------------------------------------------
// Admin: list, create, approve, reject, suspend
// ---------------------------------------------------------------------------

// ListSuppliers returns a page of suppliers, optionally filtered by status.
func (a *API) ListSuppliers(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	query := r.URL.Query()
	limit, offset := paginate(query)

	var status *string
	if s := strings.TrimSpace(query.Get("status")); s != "" {
		switch s {
		case statusPending, statusApproved, statusSuspended, statusRejected:
			status = &s
		default:
			a.fail(ctx, w, httpx.BadRequest(
				"status must be one of pending, approved, suspended, rejected."))
			return
		}
	}

	rows, err := a.queries.ListSuppliers(ctx, store.ListSuppliersParams{
		Status: status,
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}
	total, err := a.queries.CountSuppliers(ctx, status)
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	out := make([]supplierResponse, 0, len(rows))
	for _, row := range rows {
		// The approval queue does not need bank details; only the payout
		// screen does.
		out = append(out, toSupplierResponse(row, false))
	}

	a.respond(ctx, w, http.StatusOK, map[string]any{
		"suppliers": out,
		"total":     total,
		"limit":     limit,
		"offset":    offset,
	})
}

func paginate(query url.Values) (limit, offset int32) {
	limit, offset = 25, 0
	if v, err := strconv.Atoi(query.Get("limit")); err == nil && v > 0 && v <= 100 {
		limit = int32(v)
	}
	if v, err := strconv.Atoi(query.Get("offset")); err == nil && v >= 0 {
		offset = int32(v)
	}
	return limit, offset
}

type adminCreateSupplierRequest struct {
	BusinessName      string `json:"business_name"`
	ContactName       string `json:"contact_name"`
	Phone             string `json:"phone"`
	Email             string `json:"email"`
	GSTIN             string `json:"gstin"`
	PAN               string `json:"pan"`
	AddressLine1      string `json:"address_line1"`
	AddressLine2      string `json:"address_line2"`
	City              string `json:"city"`
	State             string `json:"state"`
	Pincode           string `json:"pincode"`
	BankAccountName   string `json:"bank_account_name"`
	BankAccountNumber string `json:"bank_account_number"`
	BankIFSC          string `json:"bank_ifsc"`
	// CommissionBPS overrides the platform commission for this supplier.
	//
	// A POINTER so "not sent" and "sent as 0" are distinguishable: 0 is a
	// legitimate rate (a grower we take nothing from), and a plain int64
	// would make it indistinguishable from an omitted field and silently
	// reset the rate on every edit that forgot to include it.
	CommissionBPS *int64 `json:"commission_bps"`
}

// validateCommission checks an optional per-supplier rate.
//
// Returns nil for "not provided", which means this supplier uses the platform
// default. The database enforces the same 0-10000 bounds, but rejecting here
// gives the admin a field-level message instead of a constraint violation.
//
// 0 is allowed on purpose: a grower we take no commission from is a real
// arrangement, and is why the request field is a pointer — otherwise it would
// be indistinguishable from an omitted one.
func validateCommission(v *validation, raw *int64) *int32 {
	if raw == nil {
		return nil
	}
	if *raw < 0 || *raw > 10000 {
		v.add("commission_bps", "must be between 0 and 10000 basis points (0-100%)")
		return nil
	}
	rate := int32(*raw)
	return &rate
}

// AdminCreateSupplier creates an already-approved supplier and emails a
// set-password link.
//
// No password is accepted or generated: an admin choosing a supplier's
// password means the admin knows it. The one-time link makes the supplier the
// only party who ever sets it.
func (a *API) AdminCreateSupplier(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	actor, err := httpx.RequireActor(ctx)
	if err != nil {
		a.fail(ctx, w, err)
		return
	}

	var req adminCreateSupplierRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		a.fail(ctx, w, err)
		return
	}

	v := newValidation()
	businessName := v.require("business_name", req.BusinessName, 2, 200)
	contactName := v.require("contact_name", req.ContactName, 1, 120)
	email := v.email("email", req.Email)
	phone := v.phone("phone", req.Phone)
	gstin := v.optional(req.GSTIN, 20)
	pan := v.optional(req.PAN, 10)
	line1 := v.optional(req.AddressLine1, 200)
	line2 := v.optional(req.AddressLine2, 200)
	city := v.optional(req.City, 80)
	state := v.optional(req.State, 80)
	bankName := v.optional(req.BankAccountName, 120)
	bankNumber := v.optional(req.BankAccountNumber, 32)
	commission := validateCommission(v, req.CommissionBPS)

	var pincode *string
	if strings.TrimSpace(req.Pincode) != "" {
		p := v.pincode("pincode", req.Pincode)
		pincode = &p
	}
	var ifsc *string
	if trimmed := strings.ToUpper(strings.TrimSpace(req.BankIFSC)); trimmed != "" {
		if !ifscPattern.MatchString(trimmed) {
			v.add("bank_ifsc", "is not a valid IFSC code")
		}
		ifsc = &trimmed
	}
	if err := v.err(); err != nil {
		a.fail(ctx, w, err)
		return
	}

	tx, err := a.pool.Begin(ctx)
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit
	q := a.queries.WithTx(tx)

	now := time.Now()

	user, err := q.CreateUser(ctx, store.CreateUserParams{
		Email: &email,
		Phone: &phone,
		// No password yet — the set-password link is the only way in.
		PasswordHash:  nil,
		Role:          auth.RoleSupplier,
		Status:        statusPending,
		EmailVerified: false,
	})
	if err != nil {
		if isUniqueViolation(err) {
			a.fail(ctx, w, httpx.Conflict("ACCOUNT_EXISTS",
				"An account with this email or phone number already exists."))
			return
		}
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	supplier, err := q.CreateSupplier(ctx, store.CreateSupplierParams{
		UserID:            user.ID,
		BusinessName:      businessName,
		ContactName:       contactName,
		Phone:             phone,
		Email:             email,
		Gstin:             gstin,
		Pan:               pan,
		AddressLine1:      line1,
		AddressLine2:      line2,
		City:              city,
		State:             state,
		Pincode:           pincode,
		CommissionBps:     commission,
		BankAccountName:   bankName,
		BankAccountNumber: bankNumber,
		BankIfsc:          ifsc,
		// Admin-created suppliers are trusted on creation.
		Status:     statusApproved,
		ApprovedBy: &actor.UserID,
		ApprovedAt: &now,
	})
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	plaintext, hash, err := auth.NewOpaqueToken()
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}
	if _, err := q.CreatePasswordSetToken(ctx, store.CreatePasswordSetTokenParams{
		UserID:    user.ID,
		TokenHash: hash,
		ExpiresAt: now.Add(passwordSetTokenTTL),
	}); err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	if err := a.writeAudit(ctx, q, actor.UserID, "supplier.created", "supplier",
		supplier.ID, nil, supplier); err != nil {
		a.fail(ctx, w, err)
		return
	}
	if err := analyticsevents.Emit(ctx, q, supplier.ID, analyticsevents.SupplierCreated,
		analyticsevents.ActorAdmin, time.Now()); err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	if err := tx.Commit(ctx); err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	// Sent after commit: a delivery failure must not roll back a supplier the
	// admin has already been told about. Failure is logged, not surfaced —
	// the admin can resend.
	a.sendPasswordSetLink(ctx, supplier, plaintext)

	a.respond(ctx, w, http.StatusCreated, toSupplierResponse(supplier, false))
}

func (a *API) sendPasswordSetLink(ctx context.Context, supplier store.Supplier, token string) {
	link := fmt.Sprintf("%s/set-password?token=%s", a.appBaseURL, url.QueryEscape(token))

	body := fmt.Sprintf(`Hello %s,

An account has been created for %s on Vayalavan.

Set your password to get started:

%s

This link is valid for %d hours and can be used once.

If you weren't expecting this, please ignore this email or write to us at
vayal.mikrogreenz@gmail.com.

— Vayalavan
`, supplier.ContactName, supplier.BusinessName, link, int(passwordSetTokenTTL.Hours()))

	if err := a.mailer.Send(ctx, mail.Message{
		To:      supplier.Email,
		Subject: "Set your Vayalavan password",
		Body:    body,
	}); err != nil {
		a.logger.ErrorContext(ctx, "failed to send supplier set-password email",
			slogErr(err), slogSupplier(supplier.ID))
	}
}

type supplierDecisionRequest struct {
	Reason string `json:"reason"`
}

// ApproveSupplier moves a supplier to approved and activates its user.
func (a *API) ApproveSupplier(w http.ResponseWriter, r *http.Request) {
	a.decideSupplier(w, r, statusApproved)
}

// RejectSupplier marks an application rejected.
func (a *API) RejectSupplier(w http.ResponseWriter, r *http.Request) {
	a.decideSupplier(w, r, statusRejected)
}

// SuspendSupplier suspends an approved supplier.
func (a *API) SuspendSupplier(w http.ResponseWriter, r *http.Request) {
	a.decideSupplier(w, r, statusSuspended)
}

// decideSupplier applies an admin decision, updating the supplier, its user's
// login status, and the audit log in one transaction.
func (a *API) decideSupplier(w http.ResponseWriter, r *http.Request, decision string) {
	ctx := r.Context()

	actor, err := httpx.RequireActor(ctx)
	if err != nil {
		a.fail(ctx, w, err)
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		a.fail(ctx, w, httpx.NotFound("Supplier not found."))
		return
	}

	var req supplierDecisionRequest
	if r.ContentLength > 0 {
		if err := httpx.DecodeJSON(w, r, &req); err != nil {
			a.fail(ctx, w, err)
			return
		}
	}
	if decision == statusRejected && strings.TrimSpace(req.Reason) == "" {
		a.fail(ctx, w, httpx.Validation("A reason is required when rejecting.",
			map[string]any{"reason": "is required"}))
		return
	}

	tx, err := a.pool.Begin(ctx)
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit
	q := a.queries.WithTx(tx)

	before, err := q.GetSupplierByID(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			a.fail(ctx, w, httpx.NotFound("Supplier not found."))
			return
		}
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	var after store.Supplier
	var userStatus, action string

	switch decision {
	case statusApproved:
		after, err = q.ApproveSupplier(ctx, store.ApproveSupplierParams{
			ID: id, ApprovedBy: &actor.UserID,
		})
		userStatus, action = statusActive, "supplier.approved"
	case statusRejected:
		reason := strings.TrimSpace(req.Reason)
		after, err = q.RejectSupplier(ctx, store.RejectSupplierParams{
			ID: id, RejectionReason: &reason,
		})
		userStatus, action = statusSuspended, "supplier.rejected"
	case statusSuspended:
		after, err = q.SuspendSupplier(ctx, id)
		userStatus, action = statusSuspended, "supplier.suspended"
	}
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	if _, err := q.SetUserStatus(ctx, store.SetUserStatusParams{
		ID: after.UserID, Status: userStatus,
	}); err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	// Suspension must bite immediately. Without this the supplier keeps a
	// valid refresh token and can mint fresh access tokens for up to 30 days.
	if userStatus == statusSuspended {
		if _, err := q.RevokeAllUserRefreshTokens(ctx, after.UserID); err != nil {
			a.fail(ctx, w, httpx.Internal(err))
			return
		}
	}

	// CLAUDE.md §5.3: supplier approval is one of the mandatory audit actions.
	if err := a.writeAudit(ctx, q, actor.UserID, action, "supplier", id, before, after); err != nil {
		a.fail(ctx, w, err)
		return
	}
	// The audit action names ARE the event types (supplier.approved, …).
	// The rejection reason stays in the audit log: admin free text.
	if err := analyticsevents.Emit(ctx, q, id, action,
		analyticsevents.ActorAdmin, time.Now()); err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	if err := tx.Commit(ctx); err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}
	a.respond(ctx, w, http.StatusOK, toSupplierResponse(after, false))
}

// writeAudit records an administrative action.
//
// Takes the transaction's queries so the audit row commits atomically with the
// change it describes — an audit log that can disagree with reality is worse
// than none.
func (a *API) writeAudit(
	ctx context.Context,
	q *store.Queries,
	adminID uuid.UUID,
	action, entityType string,
	entityID uuid.UUID,
	before, after any,
) error {
	beforeJSON, err := marshalAudit(before)
	if err != nil {
		return httpx.Internal(err)
	}
	afterJSON, err := marshalAudit(after)
	if err != nil {
		return httpx.Internal(err)
	}

	if _, err := q.InsertAuditLog(ctx, store.InsertAuditLogParams{
		AdminUserID: adminID,
		Action:      action,
		EntityType:  entityType,
		EntityID:    &entityID,
		Before:      beforeJSON,
		After:       afterJSON,
	}); err != nil {
		return httpx.Internal(err)
	}
	return nil
}

// marshalAudit serialises an audit snapshot, redacting the bank account
// number so the audit log does not become the one place it sits in the clear.
func marshalAudit(v any) ([]byte, error) {
	if v == nil {
		return nil, nil
	}
	if supplier, ok := v.(store.Supplier); ok {
		supplier.BankAccountNumber = maskAccountNumber(supplier.BankAccountNumber)
		return json.Marshal(supplier)
	}
	return json.Marshal(v)
}

// AdminUpdateSupplier edits a supplier's business, contact and bank details.
//
// Status is deliberately not editable here. Approval, rejection and suspension
// are separate audited transitions with their own meaning (and, for approval,
// their own side effects); folding them into a general-purpose edit would let
// a careless save silently approve a pending applicant.
//
// Audited like every other admin write on a supplier — bank details decide
// where money goes, so "who changed this account number, and when" has to be
// answerable. The before/after pair records the account number MASKED: the
// audit log needs to show that it changed, not to become a second, permanent
// copy of everyone's bank details.
func (a *API) AdminUpdateSupplier(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	actor, err := httpx.RequireActor(ctx)
	if err != nil {
		a.fail(ctx, w, err)
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		a.fail(ctx, w, httpx.NotFound("Supplier not found."))
		return
	}

	var req adminCreateSupplierRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		a.fail(ctx, w, err)
		return
	}

	// Same validation as creation: an edit must not be able to write a value
	// that creation would have rejected.
	v := newValidation()
	businessName := v.require("business_name", req.BusinessName, 2, 200)
	contactName := v.require("contact_name", req.ContactName, 1, 120)
	email := v.email("email", req.Email)
	phone := v.phone("phone", req.Phone)
	gstin := v.optional(req.GSTIN, 20)
	pan := v.optional(req.PAN, 10)
	line1 := v.optional(req.AddressLine1, 200)
	line2 := v.optional(req.AddressLine2, 200)
	city := v.optional(req.City, 80)
	state := v.optional(req.State, 80)
	bankName := v.optional(req.BankAccountName, 120)
	bankNumber := v.optional(req.BankAccountNumber, 32)
	commission := validateCommission(v, req.CommissionBPS)

	var pincode *string
	if strings.TrimSpace(req.Pincode) != "" {
		p := v.pincode("pincode", req.Pincode)
		pincode = &p
	}
	var ifsc *string
	if trimmed := strings.ToUpper(strings.TrimSpace(req.BankIFSC)); trimmed != "" {
		if !ifscPattern.MatchString(trimmed) {
			v.add("bank_ifsc", "is not a valid IFSC code")
		}
		ifsc = &trimmed
	}
	if err := v.err(); err != nil {
		a.fail(ctx, w, err)
		return
	}

	tx, err := a.pool.Begin(ctx)
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit
	q := a.queries.WithTx(tx)

	before, err := q.GetSupplierByID(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			a.fail(ctx, w, httpx.NotFound("Supplier not found."))
			return
		}
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	after, err := q.AdminUpdateSupplier(ctx, store.AdminUpdateSupplierParams{
		ID:                id,
		BusinessName:      businessName,
		ContactName:       contactName,
		Phone:             phone,
		Email:             email,
		Gstin:             gstin,
		Pan:               pan,
		AddressLine1:      line1,
		AddressLine2:      line2,
		City:              city,
		State:             state,
		Pincode:           pincode,
		BankAccountName:   bankName,
		BankAccountNumber: bankNumber,
		BankIfsc:          ifsc,
		CommissionBps:     commission,
	})
	if err != nil {
		if isUniqueViolation(err) {
			a.fail(ctx, w, httpx.Conflict("SUPPLIER_EXISTS",
				"Another supplier already uses this email or phone number."))
			return
		}
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	if err := a.writeAudit(ctx, q, actor.UserID, "supplier.updated", "supplier", id,
		auditableSupplier(before), auditableSupplier(after)); err != nil {
		a.fail(ctx, w, err)
		return
	}
	if err := analyticsevents.Emit(ctx, q, id, analyticsevents.SupplierUpdated,
		analyticsevents.ActorAdmin, time.Now()); err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	if err := tx.Commit(ctx); err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	a.respond(ctx, w, http.StatusOK, toSupplierResponse(after, false))
}

// auditableSupplier is the shape written to admin_audit_log.
//
// The account number is masked: the log must prove the value changed without
// becoming a permanent second store of every supplier's bank details, readable
// by anyone who can read the audit table.
func auditableSupplier(s store.Supplier) map[string]any {
	return map[string]any{
		"business_name":       s.BusinessName,
		"contact_name":        s.ContactName,
		"phone":               s.Phone,
		"email":               s.Email,
		"gstin":               s.Gstin,
		"pan":                 s.Pan,
		"address_line1":       s.AddressLine1,
		"address_line2":       s.AddressLine2,
		"city":                s.City,
		"state":               s.State,
		"pincode":             s.Pincode,
		"bank_account_name":   s.BankAccountName,
		"bank_account_number": maskAccountNumber(s.BankAccountNumber),
		"bank_ifsc":           s.BankIfsc,
		// Audited because it decides what a grower is paid. "null" here means
		// the platform default applied, which is itself a meaningful change to
		// record when someone switches an override off.
		"commission_bps": s.CommissionBps,
	}
}

// SupplierCommissions returns per-supplier commission rates for vm-orders-api.
//
// Rates only. No names, contact details, addresses or bank fields — orders-api
// needs to know what to deduct and nothing else, and a response that carries
// only integers cannot leak anything if the call is ever mis-scoped.
//
// A null rate means "this supplier uses the platform default". The default
// itself is NOT returned, because it lives in orders-api's configuration and
// having two services each hold a copy is how they end up disagreeing.
//
// Suppliers not found are simply absent from the response rather than an
// error: the caller falls back to the default for anything missing, which is
// the same thing it does for a null.
func (a *API) SupplierCommissions(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req supplierIDsRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		a.fail(ctx, w, err)
		return
	}
	ids, err := parseSupplierIDs(req.SupplierIDs)
	if err != nil {
		a.fail(ctx, w, err)
		return
	}

	rows, err := a.queries.ListSupplierCommissions(ctx, ids)
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		out = append(out, map[string]any{
			"supplier_id":    row.ID.String(),
			"commission_bps": row.CommissionBps,
		})
	}
	a.respond(ctx, w, http.StatusOK, map[string]any{"suppliers": out})
}

// customerIDsRequest is the body CustomerContacts takes.
type customerIDsRequest struct {
	CustomerIDs []string `json:"customer_ids"`
}

// CustomerContacts returns email addresses for a set of customers.
//
// Service-to-service only, for vm-orders-api's outbox dispatcher: an order
// confirmation has to go somewhere, and the address lives in this schema
// (CLAUDE.md §3 — orders may not read it).
//
// Deliberately narrow. It answers with an address and a name and nothing else:
// no phone, no addresses, no order history. A customer with no email is simply
// absent from the response, which the caller reads as "cannot email this one".
func (a *API) CustomerContacts(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req customerIDsRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		a.fail(ctx, w, err)
		return
	}
	ids := make([]uuid.UUID, 0, len(req.CustomerIDs))
	for _, value := range req.CustomerIDs {
		id, err := uuid.Parse(value)
		if err != nil {
			a.fail(ctx, w, httpx.BadRequest("customer_ids must be UUIDs."))
			return
		}
		ids = append(ids, id)
	}

	rows, err := a.queries.ListCustomerContacts(ctx, ids)
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	out := make([]map[string]string, 0, len(rows))
	for _, row := range rows {
		email := ""
		if row.Email != nil {
			email = *row.Email
		}
		out = append(out, map[string]string{
			"customer_id": row.ID.String(), "email": email, "name": row.Name,
		})
	}
	a.respond(ctx, w, http.StatusOK, map[string]any{"customers": out})
}
