package api

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/vayal-mikrogreenz/vm-go-common/httpx"
	"github.com/vayal-mikrogreenz/vm-go-common/isttime"
	"github.com/vayal-mikrogreenz/vm-go-common/money"

	"github.com/vayal-mikrogreenz/vm-orders-api/internal/store"
)

// Audit actions recorded for the orders domain (CLAUDE.md §5.3).
const (
	actionPayoutMarkedPaid  = "payout.marked_paid"
	actionBankDetailsViewed = "supplier.bank_details_viewed"
	actionOrderDispatched   = "order.dispatched"
	actionOrderCancelled    = "order.cancelled"
	actionOrderPaidManually = "order.payment_recorded"
	actionOrderProcessed    = "order.processed"
)

// maxPayoutBatch bounds one mark-paid call. A settlement run is tens of rows,
// not thousands, and the whole batch is locked in one transaction.
const maxPayoutBatch = 200

// adminFromRequest returns the authenticated administrator.
func adminFromRequest(r *http.Request) (uuid.UUID, error) {
	actor, err := httpx.RequireActor(r.Context())
	if err != nil {
		return uuid.Nil, err
	}
	if !actor.IsAdmin() {
		return uuid.Nil, httpx.Forbidden("You do not have access to this resource.")
	}
	return actor.UserID, nil
}

// ---------------------------------------------------------------------------
// GET /admin/payouts/summary
// ---------------------------------------------------------------------------

type payoutSummaryRow struct {
	SupplierID      string `json:"supplier_id"`
	BusinessName    string `json:"business_name"`
	PendingPaise    int64  `json:"pending_paise"`
	Pending         string `json:"pending_display"`
	PendingOrders   int64  `json:"pending_orders"`
	OldestPendingAt string `json:"oldest_pending_at"`
	// AgeDays makes "this supplier has been waiting a fortnight" visible at a
	// glance, which is the whole point of the settlement screen.
	AgeDays int `json:"age_days"`
}

// PayoutSummary lists what each supplier is owed.
func (a *API) PayoutSummary(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if _, err := adminFromRequest(r); err != nil {
		a.fail(ctx, w, err)
		return
	}

	rows, err := a.queries.PayoutSummary(ctx)
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	// Business names live in the profile schema, so they come over HTTP.
	// Masked details only — this screen shows no bank information.
	names := a.supplierNames(ctx, supplierIDsFromSummary(rows))

	now := time.Now()
	out := make([]payoutSummaryRow, 0, len(rows))
	var totalPaise int64

	for _, row := range rows {
		totalPaise += row.PendingPaise
		out = append(out, payoutSummaryRow{
			SupplierID:      row.SupplierID.String(),
			BusinessName:    names[row.SupplierID],
			PendingPaise:    row.PendingPaise,
			Pending:         money.FormatRupees(money.Paise(row.PendingPaise)),
			PendingOrders:   row.PendingOrders,
			OldestPendingAt: row.OldestPendingAt.Format(time.RFC3339),
			AgeDays:         int(now.Sub(row.OldestPendingAt).Hours() / 24),
		})
	}

	a.respond(ctx, w, http.StatusOK, map[string]any{
		"suppliers":           out,
		"total_pending_paise": totalPaise,
		"total_pending":       money.FormatRupees(money.Paise(totalPaise)),
	})
}

func supplierIDsFromSummary(rows []store.PayoutSummaryRow) []uuid.UUID {
	ids := make([]uuid.UUID, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.SupplierID)
	}
	return ids
}

// ---------------------------------------------------------------------------
// GET /admin/payouts
// ---------------------------------------------------------------------------

type payoutRow struct {
	ID           string  `json:"id"`
	SupplierID   string  `json:"supplier_id"`
	BusinessName string  `json:"business_name"`
	OrderID      string  `json:"order_id"`
	OrderNumber  string  `json:"order_number"`
	PlacedAt     string  `json:"placed_at"`
	AmountPaise  int64   `json:"amount_paise"`
	Amount       string  `json:"amount_display"`
	Status       string  `json:"status"`
	ReferenceNo  *string `json:"reference_no"`
	MarkedPaidAt *string `json:"marked_paid_at"`
}

// ListPayouts returns line-level payouts, filtered by supplier and status.
func (a *API) ListPayouts(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if _, err := adminFromRequest(r); err != nil {
		a.fail(ctx, w, err)
		return
	}

	params := store.ListPayoutsParams{Limit: 100}
	if raw := strings.TrimSpace(r.URL.Query().Get("supplier_id")); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			a.fail(ctx, w, httpx.BadRequest("supplier_id must be a UUID."))
			return
		}
		params.SupplierID = &id
	}
	if status := strings.TrimSpace(r.URL.Query().Get("status")); status != "" {
		if status != "pending" && status != "paid" {
			a.fail(ctx, w, httpx.BadRequest("status must be pending or paid."))
			return
		}
		params.Status = &status
	}
	if v, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && v > 0 && v <= 500 {
		params.Limit = int32(v)
	}
	if v, err := strconv.Atoi(r.URL.Query().Get("offset")); err == nil && v >= 0 {
		params.Offset = int32(v)
	}

	rows, err := a.queries.ListPayouts(ctx, params)
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	ids := make([]uuid.UUID, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.SupplierID)
	}
	names := a.supplierNames(ctx, ids)

	out := make([]payoutRow, 0, len(rows))
	var total int64
	for _, row := range rows {
		total += row.AmountPaise
		entry := payoutRow{
			ID:           row.ID.String(),
			SupplierID:   row.SupplierID.String(),
			BusinessName: names[row.SupplierID],
			OrderID:      row.OrderID.String(),
			OrderNumber:  row.OrderNumber,
			PlacedAt:     row.PlacedAt.Format(time.RFC3339),
			AmountPaise:  row.AmountPaise,
			Amount:       money.FormatRupees(money.Paise(row.AmountPaise)),
			Status:       row.Status,
			ReferenceNo:  row.ReferenceNo,
		}
		if row.MarkedPaidAt != nil {
			formatted := row.MarkedPaidAt.Format(time.RFC3339)
			entry.MarkedPaidAt = &formatted
		}
		out = append(out, entry)
	}

	a.respond(ctx, w, http.StatusOK, map[string]any{
		"payouts":       out,
		"total_paise":   total,
		"total_display": money.FormatRupees(money.Paise(total)),
	})
}

// ---------------------------------------------------------------------------
// POST /admin/payouts/mark-paid
// ---------------------------------------------------------------------------

type markPaidRequest struct {
	PayoutIDs []string `json:"payout_ids"`
	// ReferenceNo is the NEFT UTR. Required: a payout marked paid without one
	// cannot be reconciled against a bank statement.
	ReferenceNo string `json:"reference_no"`
	Notes       string `json:"notes"`
}

// MarkPayoutsPaid settles a set of payouts atomically.
//
// All-or-nothing, and it REJECTS the whole batch if any row is already paid
// (CLAUDE.md §5.3 requires an audit trail, and silently skipping would leave
// an admin believing they had just paid money they had paid last week).
//
// The combination of SELECT ... FOR UPDATE and the status='pending' filter on
// the update makes double payment impossible even under two admins clicking
// at the same moment: the second transaction blocks, then sees the rows are
// no longer pending.
func (a *API) MarkPayoutsPaid(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	adminID, err := adminFromRequest(r)
	if err != nil {
		a.fail(ctx, w, err)
		return
	}

	var req markPaidRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		a.fail(ctx, w, err)
		return
	}

	v := newValidation()
	reference := strings.TrimSpace(req.ReferenceNo)
	switch {
	case reference == "":
		v.add("reference_no", "is required — record the NEFT UTR")
	case len(reference) > 64:
		v.add("reference_no", "is too long")
	}
	if len(req.PayoutIDs) == 0 {
		v.add("payout_ids", "at least one payout is required")
	}
	if len(req.PayoutIDs) > maxPayoutBatch {
		v.add("payout_ids", "too many payouts in one batch")
	}

	ids := make([]uuid.UUID, 0, len(req.PayoutIDs))
	seen := map[uuid.UUID]bool{}
	for _, raw := range req.PayoutIDs {
		id, parseErr := uuid.Parse(raw)
		if parseErr != nil {
			v.add("payout_ids", "contains a value that is not a UUID")
			break
		}
		// A duplicate id in one batch would be counted twice in the total the
		// admin is reconciling against their bank transfer.
		if seen[id] {
			v.add("payout_ids", "contains a duplicate")
			break
		}
		seen[id] = true
		ids = append(ids, id)
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

	// Locked in a deterministic order, so two concurrent settlement runs
	// serialise rather than deadlock.
	locked, err := q.LockPayoutsForUpdate(ctx, ids)
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	if len(locked) != len(ids) {
		a.fail(ctx, w, httpx.NotFound("One or more payouts were not found."))
		return
	}

	// Reject the batch if ANY row is already settled, naming them so the admin
	// can see exactly what happened.
	var alreadyPaid []map[string]any
	for _, payout := range locked {
		if payout.Status == "paid" {
			entry := map[string]any{
				"payout_id":    payout.ID.String(),
				"amount_paise": payout.AmountPaise,
			}
			if payout.ReferenceNo != nil {
				entry["reference_no"] = *payout.ReferenceNo
			}
			if payout.MarkedPaidAt != nil {
				entry["marked_paid_at"] = payout.MarkedPaidAt.Format(time.RFC3339)
			}
			alreadyPaid = append(alreadyPaid, entry)
		}
	}
	if len(alreadyPaid) > 0 {
		a.logger.WarnContext(ctx, "mark-paid rejected: batch contains settled payouts",
			slog.String("admin_user_id", adminID.String()),
			slog.Int("already_paid", len(alreadyPaid)))
		a.fail(ctx, w, httpx.Conflict("PAYOUT_ALREADY_PAID",
			fmt.Sprintf("%d of these payouts have already been paid. "+
				"Nothing was changed.", len(alreadyPaid))).
			WithDetails(map[string]any{"already_paid": alreadyPaid}))
		return
	}

	notes := strings.TrimSpace(req.Notes)
	var notesPtr *string
	if notes != "" {
		notesPtr = &notes
	}

	settled := make([]payoutRow, 0, len(locked))
	var totalPaise int64

	for _, payout := range locked {
		updated, err := q.MarkPayoutPaid(ctx, store.MarkPayoutPaidParams{
			ID: payout.ID, MarkedPaidBy: &adminID,
			ReferenceNo: &reference, Notes: notesPtr,
		})
		if err != nil {
			a.fail(ctx, w, httpx.Internal(err))
			return
		}
		totalPaise += updated.AmountPaise

		markedAt := updated.MarkedPaidAt.Format(time.RFC3339)
		settled = append(settled, payoutRow{
			ID:           updated.ID.String(),
			SupplierID:   updated.SupplierID.String(),
			OrderID:      updated.OrderID.String(),
			AmountPaise:  updated.AmountPaise,
			Amount:       money.FormatRupees(money.Paise(updated.AmountPaise)),
			Status:       updated.Status,
			ReferenceNo:  updated.ReferenceNo,
			MarkedPaidAt: &markedAt,
		})
	}

	// CLAUDE.md §5.3: marking a payout paid is a mandatory audit action. In
	// the same transaction, so the log can never disagree with reality.
	if err := a.writeAudit(ctx, q, adminID, actionPayoutMarkedPaid, "supplier_payout",
		uuid.Nil, nil, map[string]any{
			"payout_ids":   req.PayoutIDs,
			"reference_no": reference,
			"notes":        notes,
			"total_paise":  totalPaise,
			"count":        len(settled),
		}); err != nil {
		a.fail(ctx, w, err)
		return
	}

	if err := tx.Commit(ctx); err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	a.logger.InfoContext(ctx, "payouts settled",
		slog.String("admin_user_id", adminID.String()),
		slog.String("reference_no", reference),
		slog.Int("count", len(settled)),
		slog.Int64("total_paise", totalPaise))

	a.respond(ctx, w, http.StatusOK, map[string]any{
		"settled":       len(settled),
		"total_paise":   totalPaise,
		"total_display": money.FormatRupees(money.Paise(totalPaise)),
		"reference_no":  reference,
		"payouts":       settled,
	})
}

// ---------------------------------------------------------------------------
// GET /admin/payouts/export.csv
// ---------------------------------------------------------------------------

// ExportPendingPayouts writes the NEFT worksheet.
//
// This is the ONE place bank account numbers appear unmasked (CLAUDE.md
// §5.1), because an admin cannot make a transfer without them. Every export
// is written to admin_audit_log — an unmasked view of bank details is exactly
// the kind of access that needs a name against it afterwards.
func (a *API) ExportPendingPayouts(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	adminID, err := adminFromRequest(r)
	if err != nil {
		a.fail(ctx, w, err)
		return
	}

	params := store.ListPayoutsParams{Limit: 5000}
	pending := "pending"
	params.Status = &pending

	var supplierFilter *uuid.UUID
	if raw := strings.TrimSpace(r.URL.Query().Get("supplier_id")); raw != "" {
		id, parseErr := uuid.Parse(raw)
		if parseErr != nil {
			a.fail(ctx, w, httpx.BadRequest("supplier_id must be a UUID."))
			return
		}
		params.SupplierID = &id
		supplierFilter = &id
	}

	rows, err := a.queries.ListPayouts(ctx, params)
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	ids := make([]uuid.UUID, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.SupplierID)
	}
	// Unmasked, and audited on the profile side too.
	banks, err := a.supplierBankDetails(ctx, adminID, ids)
	if err != nil {
		a.fail(ctx, w, err)
		return
	}

	// Audited BEFORE the bytes go out, so a connection that drops mid-download
	// still leaves a record that the data was released.
	if err := a.writeAudit(ctx, a.queries, adminID, actionBankDetailsViewed, "supplier_payout",
		uuid.Nil, nil, map[string]any{
			"reason":       "pending payout CSV export",
			"supplier_ids": supplierIDStrings(ids),
			"row_count":    len(rows),
			"filtered_to":  supplierIDString(supplierFilter),
		}); err != nil {
		a.fail(ctx, w, err)
		return
	}

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition",
		fmt.Sprintf(`attachment; filename="vayal-pending-payouts-%s.csv"`,
			isttime.Today().Format("2006-01-02")))
	w.WriteHeader(http.StatusOK)

	writer := csv.NewWriter(w)
	defer writer.Flush()

	_ = writer.Write([]string{
		"payout_id", "supplier_id", "business_name", "order_number", "placed_at",
		"amount_rupees", "bank_account_name", "bank_account_number", "bank_ifsc",
	})

	for _, row := range rows {
		bank := banks[row.SupplierID]
		_ = writer.Write([]string{
			row.ID.String(),
			row.SupplierID.String(),
			bank.BusinessName,
			row.OrderNumber,
			row.PlacedAt.In(isttime.Location()).Format("2006-01-02"),
			// Rupees with two decimals: this column is read by a human keying
			// a transfer into a banking portal, not by our own code.
			fmt.Sprintf("%d.%02d", row.AmountPaise/100, row.AmountPaise%100),
			bank.AccountName,
			bank.AccountNumber,
			bank.IFSC,
		})
	}
}

func supplierIDStrings(ids []uuid.UUID) []string {
	out := make([]string, 0, len(ids))
	seen := map[uuid.UUID]bool{}
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			out = append(out, id.String())
		}
	}
	return out
}

func supplierIDString(id *uuid.UUID) string {
	if id == nil {
		return "all"
	}
	return id.String()
}

// writeAudit records an administrative action.
//
// Takes the transaction's queries so the audit row commits atomically with the
// change it describes — an audit log that can disagree with reality is worse
// than none.
func (a *API) writeAudit(
	ctx context.Context, q *store.Queries, adminID uuid.UUID,
	action, entityType string, entityID uuid.UUID, before, after any,
) error {
	beforeJSON, err := marshalOrNil(before)
	if err != nil {
		return httpx.Internal(err)
	}
	afterJSON, err := marshalOrNil(after)
	if err != nil {
		return httpx.Internal(err)
	}

	var entityPtr *uuid.UUID
	if entityID != uuid.Nil {
		entityPtr = &entityID
	}

	if _, err := q.InsertAuditLog(ctx, store.InsertAuditLogParams{
		AdminUserID: adminID, Action: action, EntityType: entityType,
		EntityID: entityPtr, Before: beforeJSON, After: afterJSON,
	}); err != nil {
		return httpx.Internal(err)
	}
	return nil
}

func marshalOrNil(v any) ([]byte, error) {
	if v == nil {
		return nil, nil
	}
	return json.Marshal(v)
}
