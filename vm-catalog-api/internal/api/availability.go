package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vayal-mikrogreenz/vm-go-common/httpx"
	"github.com/vayal-mikrogreenz/vm-go-common/isttime"

	"github.com/vayal-mikrogreenz/vm-catalog-api/internal/availability"
	"github.com/vayal-mikrogreenz/vm-catalog-api/internal/store"
)

// dateFormat is the wire format for a business day.
const dateFormat = "2006-01-02"

// maxSheetEntries bounds a bulk declaration. A supplier with more active
// products than this is a data problem worth surfacing, not a request to
// process.
const maxSheetEntries = 500

// parseBusinessDate reads a ?date=YYYY-MM-DD parameter as an IST calendar day.
//
// Used ONLY for viewing a supplier's own sheet. It must never feed a
// purchasability decision — see catalog.go, where the day is always computed
// server-side (requirement 4).
func parseBusinessDate(raw string) (time.Time, error) {
	if raw == "" {
		return availability.Today(), nil
	}
	parsed, err := time.ParseInLocation(dateFormat, raw, isttime.Location())
	if err != nil {
		return time.Time{}, httpx.BadRequest("date must be in YYYY-MM-DD format.")
	}
	return parsed, nil
}

// sheetRow is one SIZE CODE's declaration for the day — the unit a grower
// actually declares, because the grades are separate crates with separate
// gram pools. A product with three grades contributes three rows, and the
// clients group them under the product name.
type sheetRow struct {
	ProductID string  `json:"product_id"`
	Name      string  `json:"name"`
	Type      string  `json:"type"`
	Grade     *string `json:"grade"`
	// The grade this row declares stock for.
	SizeCodeID string  `json:"size_code_id"`
	SizeCode   string  `json:"size_code"`
	SizeMeta   *string `json:"size_meta"`
	ImageURL   *string `json:"image_url"`
	// AvailabilityID is null when nothing has been declared for this date —
	// the "not declared today" state the supplier screen leads with.
	AvailabilityID *string `json:"availability_id"`
	Declared       bool    `json:"declared"`
	TotalGrams     int32   `json:"total_grams"`
	// Reserved and sold ARE shown here: this is the supplier's own stock, and
	// they need to know what they are committed to before reducing it.
	ReservedGrams int32 `json:"reserved_grams"`
	SoldGrams     int32 `json:"sold_grams"`
	// RemainingGrams is status-blind: what would be left if selling resumed.
	// SellableGrams is what a customer can buy right now, and is zero once the
	// product is closed. The screen must show the second — the first made a
	// closed product still read as "50 kg left".
	RemainingGrams int32  `json:"remaining_grams"`
	SellableGrams  int32  `json:"sellable_grams"`
	Status         string `json:"status"`
}

// ---------------------------------------------------------------------------
// GET /supplier/availability?date=YYYY-MM-DD
// ---------------------------------------------------------------------------

// AvailabilitySheet returns one row per active product for a date.
//
// Every active product appears, declared or not, because the screen's job is
// to make undeclared produce obvious — a supplier who forgets has nothing on
// sale, and needs to see that at a glance.
func (a *API) AvailabilitySheet(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	supplierID, err := supplierFromRequest(r)
	if err != nil {
		a.fail(ctx, w, err)
		return
	}
	day, err := parseBusinessDate(r.URL.Query().Get("date"))
	if err != nil {
		a.fail(ctx, w, err)
		return
	}

	rows, err := a.queries.ListAvailabilitySheet(ctx, store.ListAvailabilitySheetParams{
		SupplierID:  supplierID,
		AvailableOn: day,
	})
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	out := make([]sheetRow, 0, len(rows))
	var declaredCount int

	for _, row := range rows {
		entry := sheetRow{
			ProductID:  row.ProductID.String(),
			Name:       row.ProductName,
			Type:       row.ProductType,
			Grade:      row.ProductGrade,
			SizeCodeID: row.SizeCodeID.String(),
			SizeCode:   row.SizeCode,
			SizeMeta:   row.SizeMeta,
			Status:     availability.StatusOpen,
		}

		// The query returns THIS grade's gallery cover, or '' when the grade
		// has no image at all — which includes one carrying only video.
		entry.ImageURL = a.presignMedia(ctx, row.ProductID, row.ProductImageKey)

		// A LEFT JOIN miss means "not declared for this date".
		if row.AvailabilityID != nil {
			declaredCount++
			id := row.AvailabilityID.String()
			sheet := availability.Sheet{
				TotalGrams:    derefInt32(row.TotalGrams),
				ReservedGrams: derefInt32(row.ReservedGrams),
				SoldGrams:     derefInt32(row.SoldGrams),
				Status:        derefString(row.AvailabilityStatus, availability.StatusOpen),
			}
			entry.AvailabilityID = &id
			entry.Declared = true
			entry.TotalGrams = sheet.TotalGrams
			entry.ReservedGrams = sheet.ReservedGrams
			entry.SoldGrams = sheet.SoldGrams
			entry.RemainingGrams = sheet.RemainingGrams()
			entry.SellableGrams = sheet.SellableGrams()
			entry.Status = sheet.Status
		}

		out = append(out, entry)
	}

	a.respond(ctx, w, http.StatusOK, map[string]any{
		"date": day.Format(dateFormat),
		// The client cannot compute "is this today" safely — its clock and
		// timezone are its own. The server says so.
		"is_today":         isttime.SameDayIST(day, availability.Today()),
		"products":         out,
		"declared_count":   declaredCount,
		"undeclared_count": len(out) - declaredCount,
	})
}

func derefInt32(v *int32) int32 {
	if v == nil {
		return 0
	}
	return *v
}

func derefString(v *string, fallback string) string {
	if v == nil {
		return fallback
	}
	return *v
}

// ---------------------------------------------------------------------------
// PUT /supplier/availability — bulk upsert for a date
// ---------------------------------------------------------------------------

// availabilityEntry declares one GRADE's stock for the day. A product with
// three grades is three entries — the gram pool belongs to the size code, and
// a product-level number would have to be split by guesswork.
type availabilityEntry struct {
	SizeCodeID string `json:"size_code_id"`
	// Grams, not kilograms: the UI collects kg and converts, because grams is
	// what the stock pool is denominated in.
	TotalGrams int32 `json:"total_grams"`
}

type saveAvailabilityRequest struct {
	Date    string              `json:"date"`
	Entries []availabilityEntry `json:"entries"`
}

// SaveAvailability declares stock for many products at once.
//
// All-or-nothing: the whole sheet commits in one transaction, so a supplier
// pressing "Save all" never ends up with half their produce declared and no
// indication of which half.
func (a *API) SaveAvailability(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	supplierID, err := supplierFromRequest(r)
	if err != nil {
		a.fail(ctx, w, err)
		return
	}

	var req saveAvailabilityRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		a.fail(ctx, w, err)
		return
	}

	day, err := parseBusinessDate(req.Date)
	if err != nil {
		a.fail(ctx, w, err)
		return
	}
	// Declaring stock for a day that has already passed cannot sell anything
	// and would quietly corrupt the historical record.
	if day.Before(availability.Today()) {
		a.fail(ctx, w, httpx.BadRequest("Availability cannot be declared for a past date."))
		return
	}

	v := newValidation()
	if len(req.Entries) == 0 {
		v.add("entries", "at least one product is required")
	}
	if len(req.Entries) > maxSheetEntries {
		v.add("entries", "too many products in one request")
	}

	type parsedEntry struct {
		sizeCodeID uuid.UUID
		totalGrams int32
	}
	parsed := make([]parsedEntry, 0, len(req.Entries))
	problems := map[string]any{}
	seen := map[uuid.UUID]bool{}

	for _, entry := range req.Entries {
		id, parseErr := uuid.Parse(entry.SizeCodeID)
		switch {
		case parseErr != nil:
			problems[entry.SizeCodeID] = "is not a valid size code id"
			continue
		case seen[id]:
			// Two rows for one grade would make the last write silently win.
			problems[entry.SizeCodeID] = "appears more than once"
			continue
		case entry.TotalGrams < 0:
			problems[entry.SizeCodeID] = "cannot be negative"
			continue
		case entry.TotalGrams > 100_000_000:
			problems[entry.SizeCodeID] = "is implausibly large"
			continue
		}
		seen[id] = true
		parsed = append(parsed, parsedEntry{sizeCodeID: id, totalGrams: entry.TotalGrams})
	}
	if len(problems) > 0 {
		v.add("entries", problems)
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

	saved := make([]sheetRow, 0, len(parsed))

	for _, entry := range parsed {
		// Ownership: the SIZE CODE must belong to a product of this supplier.
		// Checked before any write, so a foreign size code id cannot create an
		// availability row — and it resolves the product id at the same time,
		// which the denormalised column needs.
		sizeCode, err := q.GetSizeCodeForSupplier(ctx, store.GetSizeCodeForSupplierParams{
			ID: entry.sizeCodeID, SupplierID: supplierID,
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				a.fail(ctx, w, httpx.NotFound(
					"One of those size codes does not belong to you: "+entry.sizeCodeID.String()))
				return
			}
			a.fail(ctx, w, httpx.Internal(err))
			return
		}

		// If a row already exists, the new total must not fall below what
		// customers already hold. The database enforces this too; checking
		// here is what turns a constraint violation into a sentence the
		// supplier can act on.
		existing, err := q.GetAvailabilityForSizeCode(ctx, store.GetAvailabilityForSizeCodeParams{
			SizeCodeID: entry.sizeCodeID, AvailableOn: day,
		})
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			a.fail(ctx, w, httpx.Internal(err))
			return
		}
		if err == nil {
			current := availability.Sheet{
				TotalGrams:    existing.TotalGrams,
				ReservedGrams: existing.ReservedGrams,
				SoldGrams:     existing.SoldGrams,
				Status:        existing.Status,
			}
			var reduction availability.ReductionError
			if validationErr := availability.ValidateNewTotal(current, entry.totalGrams); validationErr != nil {
				if errors.As(validationErr, &reduction) {
					// Named by grade as well as product: "Tomato" alone does
					// not tell a grower which crate they are over-committing.
					// The implicit grade of an ungraded listing is left off —
					// a grower who never created 'STD' should not be shown it.
					label := sizeCode.ProductName
					if sizeCode.Code != defaultSizeCode {
						label += " (" + sizeCode.Code + ")"
					}
					a.fail(ctx, w, httpx.Conflict("STOCK_BELOW_COMMITTED",
						formatReduction(label, reduction)).WithDetails(map[string]any{
						"product_id":      sizeCode.ProductID.String(),
						"product_name":    sizeCode.ProductName,
						"size_code_id":    entry.sizeCodeID.String(),
						"size_code":       sizeCode.Code,
						"requested_grams": reduction.RequestedGrams,
						"committed_grams": reduction.CommittedGrams,
					}))
					return
				}
				a.fail(ctx, w, httpx.Internal(validationErr))
				return
			}
		}

		row, err := q.UpsertAvailability(ctx, store.UpsertAvailabilityParams{
			SupplierID:  supplierID,
			ProductID:   sizeCode.ProductID,
			SizeCodeID:  entry.sizeCodeID,
			AvailableOn: day,
			TotalGrams:  entry.totalGrams,
		})
		if err != nil {
			a.fail(ctx, w, httpx.Internal(err))
			return
		}

		id := row.ID.String()
		sheet := availability.Sheet{
			TotalGrams:    row.TotalGrams,
			ReservedGrams: row.ReservedGrams,
			SoldGrams:     row.SoldGrams,
			Status:        row.Status,
		}
		saved = append(saved, sheetRow{
			ProductID:      row.ProductID.String(),
			Name:           sizeCode.ProductName,
			Type:           sizeCode.ProductType,
			Grade:          sizeCode.ProductGrade,
			SizeCodeID:     sizeCode.ID.String(),
			SizeCode:       sizeCode.Code,
			SizeMeta:       sizeCode.Meta,
			AvailabilityID: &id,
			Declared:       true,
			TotalGrams:     sheet.TotalGrams,
			ReservedGrams:  sheet.ReservedGrams,
			SoldGrams:      sheet.SoldGrams,
			RemainingGrams: sheet.RemainingGrams(),
			SellableGrams:  sheet.SellableGrams(),
			Status:         sheet.Status,
		})
	}

	if err := tx.Commit(ctx); err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	a.respond(ctx, w, http.StatusOK, map[string]any{
		"date":     day.Format(dateFormat),
		"saved":    len(saved),
		"products": saved,
	})
}

func formatReduction(productName string, e availability.ReductionError) string {
	return "You cannot declare less " + productName +
		" than customers have already reserved or bought. " +
		"At least " + gramsToText(e.CommittedGrams) + " is already committed."
}

// gramsToText renders grams the way a supplier thinks about them.
func gramsToText(grams int32) string {
	if grams >= 1000 && grams%1000 == 0 {
		return itoa(grams/1000) + " kg"
	}
	if grams >= 1000 {
		whole := grams / 1000
		rest := (grams % 1000) / 100
		return itoa(whole) + "." + itoa(rest) + " kg"
	}
	return itoa(grams) + " g"
}

func itoa(v int32) string {
	if v == 0 {
		return "0"
	}
	negative := v < 0
	if negative {
		v = -v
	}
	var buf [12]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if negative {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// ---------------------------------------------------------------------------
// POST /supplier/availability/{id}/close
// ---------------------------------------------------------------------------

// CloseAvailability stops selling a product before its stock runs out.
//
// Closing does not touch reserved or sold grams: orders already placed stand.
// It only stops new ones.
func (a *API) CloseAvailability(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	supplierID, err := supplierFromRequest(r)
	if err != nil {
		a.fail(ctx, w, err)
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		a.fail(ctx, w, httpx.NotFound("Availability not found."))
		return
	}

	row, err := a.queries.CloseAvailability(ctx, store.CloseAvailabilityParams{
		ID: id, SupplierID: supplierID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Not theirs, or already closed. Both are "nothing to do here".
			a.fail(ctx, w, httpx.NotFound("Availability not found, or already closed."))
			return
		}
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	sheet := availability.Sheet{
		TotalGrams:    row.TotalGrams,
		ReservedGrams: row.ReservedGrams,
		SoldGrams:     row.SoldGrams,
		Status:        row.Status,
	}
	a.respond(ctx, w, http.StatusOK, map[string]any{
		"availability_id": row.ID.String(),
		"product_id":      row.ProductID.String(),
		"status":          row.Status,
		"remaining_grams": sheet.RemainingGrams(),
		"sellable_grams":  sheet.SellableGrams(),
	})
}

// ---------------------------------------------------------------------------
// POST /supplier/availability/{id}/reopen
// ---------------------------------------------------------------------------

// ReopenAvailability puts a closed product back on sale for the same day.
//
// Its own endpoint rather than a side effect of saving a quantity. Reopening
// used to happen implicitly on any quantity update, which meant a supplier
// editing one product put every product they had closed that day back on sale
// without being asked. Returning to sale is a decision, so it takes a click.
//
// The declared total is untouched — it was never zeroed by closing, so the
// supplier gets back exactly what they had.
func (a *API) ReopenAvailability(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	supplierID, err := supplierFromRequest(r)
	if err != nil {
		a.fail(ctx, w, err)
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		a.fail(ctx, w, httpx.NotFound("Availability not found."))
		return
	}

	row, err := a.queries.ReopenAvailability(ctx, store.ReopenAvailabilityParams{
		ID: id, SupplierID: supplierID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Not theirs, or already open. Both mean there is nothing to do.
			a.fail(ctx, w, httpx.NotFound("Availability not found, or already open."))
			return
		}
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	sheet := availability.Sheet{
		TotalGrams:    row.TotalGrams,
		ReservedGrams: row.ReservedGrams,
		SoldGrams:     row.SoldGrams,
		Status:        row.Status,
	}
	a.respond(ctx, w, http.StatusOK, map[string]any{
		"availability_id": row.ID.String(),
		"product_id":      row.ProductID.String(),
		"status":          row.Status,
		"remaining_grams": sheet.RemainingGrams(),
		"sellable_grams":  sheet.SellableGrams(),
	})
}

// ---------------------------------------------------------------------------
// POST /supplier/availability/copy-from-yesterday
// ---------------------------------------------------------------------------

// CopyFromYesterday seeds today's sheet from yesterday's declaration.
//
// The daily-driver convenience: most suppliers list roughly the same produce
// each morning, and retyping twenty numbers is where mistakes come from.
//
// Copies total_grams only. Reserved and sold start at zero because they belong
// to yesterday's orders — carrying them would immediately look oversold.
func (a *API) CopyFromYesterday(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	supplierID, err := supplierFromRequest(r)
	if err != nil {
		a.fail(ctx, w, err)
		return
	}

	var req struct {
		Date string `json:"date"`
	}
	if r.ContentLength > 0 {
		if err := httpx.DecodeJSON(w, r, &req); err != nil {
			a.fail(ctx, w, err)
			return
		}
	}

	target, err := parseBusinessDate(req.Date)
	if err != nil {
		a.fail(ctx, w, err)
		return
	}
	if target.Before(availability.Today()) {
		a.fail(ctx, w, httpx.BadRequest("Availability cannot be declared for a past date."))
		return
	}
	source := isttime.AddDays(target, -1)

	previous, err := a.queries.ListAvailabilityForDate(ctx, store.ListAvailabilityForDateParams{
		SupplierID: supplierID, AvailableOn: source,
	})
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}
	if len(previous) == 0 {
		a.fail(ctx, w, httpx.Conflict("NOTHING_TO_COPY",
			"You did not declare any availability on "+source.Format(dateFormat)+"."))
		return
	}

	tx, err := a.pool.Begin(ctx)
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit
	q := a.queries.WithTx(tx)

	copied, skipped := 0, 0

	for _, row := range previous {
		// If today already has a declaration with committed stock, copying
		// yesterday's number could drop below it. Skip rather than fail: the
		// supplier asked for a convenience, not a destructive overwrite.
		existing, err := q.GetAvailabilityForSizeCode(ctx, store.GetAvailabilityForSizeCodeParams{
			SizeCodeID: row.SizeCodeID, AvailableOn: target,
		})
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			a.fail(ctx, w, httpx.Internal(err))
			return
		}
		if err == nil {
			current := availability.Sheet{
				TotalGrams:    existing.TotalGrams,
				ReservedGrams: existing.ReservedGrams,
				SoldGrams:     existing.SoldGrams,
				Status:        existing.Status,
			}
			if availability.ValidateNewTotal(current, row.TotalGrams) != nil {
				skipped++
				continue
			}
		}

		if _, err := q.UpsertAvailability(ctx, store.UpsertAvailabilityParams{
			SupplierID:  supplierID,
			ProductID:   row.ProductID,
			SizeCodeID:  row.SizeCodeID,
			AvailableOn: target,
			TotalGrams:  row.TotalGrams,
		}); err != nil {
			a.fail(ctx, w, httpx.Internal(err))
			return
		}
		copied++
	}

	if err := tx.Commit(ctx); err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	a.respond(ctx, w, http.StatusOK, map[string]any{
		"date":        target.Format(dateFormat),
		"copied_from": source.Format(dateFormat),
		"copied":      copied,
		// Non-zero means some products already had orders today at a higher
		// declaration; the UI tells the supplier to review those by hand.
		"skipped": skipped,
	})
}
