package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vayal-mikrogreenz/vm-go-common/httpx"

	"github.com/vayal-mikrogreenz/vm-catalog-api/internal/analyticsevents"
	"github.com/vayal-mikrogreenz/vm-catalog-api/internal/csvimport"
	"github.com/vayal-mikrogreenz/vm-catalog-api/internal/store"
)

// imageFetchTimeout bounds fetching one image_url from a CSV row.
const imageFetchTimeout = 15 * time.Second

// DownloadTemplate serves the starter CSV (CLAUDE.md §6.5).
func (a *API) DownloadTemplate(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="vayal-products-template.csv"`)
	w.WriteHeader(http.StatusOK)
	if _, err := io.WriteString(w, csvimport.TemplateCSV); err != nil {
		a.logger.ErrorContext(r.Context(), "writing CSV template failed", slog2(err))
	}
}

// ---------------------------------------------------------------------------
// POST /imports — step one: validate and preview. Writes NOTHING to products.
// ---------------------------------------------------------------------------

// UploadImport parses an uploaded CSV and stores the per-row report.
//
// Deliberately does not touch the products table: CLAUDE.md §6.5 makes this a
// two-step flow so a supplier sees exactly what will happen before it does.
func (a *API) UploadImport(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	supplierID, err := supplierFromRequest(r)
	if err != nil {
		a.fail(ctx, w, err)
		return
	}

	// Cap the multipart body before reading it, so an oversized upload is
	// refused rather than buffered.
	r.Body = http.MaxBytesReader(w, r.Body, a.limits.MaxCSVBytes+1024)
	if err := r.ParseMultipartForm(a.limits.MaxCSVBytes); err != nil {
		a.fail(ctx, w, httpx.BadRequest(
			"Upload must be multipart/form-data with a 'file' field, within the size limit."))
		return
	}
	defer func() { _ = r.MultipartForm.RemoveAll() }()

	file, header, err := r.FormFile("file")
	if err != nil {
		a.fail(ctx, w, httpx.BadRequest("No file was uploaded under the 'file' field."))
		return
	}
	defer file.Close()

	filename := path.Base(header.Filename)
	if ext := strings.ToLower(path.Ext(filename)); ext != ".csv" && ext != ".txt" && ext != "" {
		a.fail(ctx, w, httpx.Validation("That file type is not supported.",
			map[string]any{"file": "must be a .csv file"}))
		return
	}

	report, err := csvimport.Parse(file, a.csvLimits())
	if err != nil {
		// Parse failures are the supplier's to fix, and every message is
		// written for them, so pass it straight through as a 422.
		a.fail(ctx, w, httpx.Validation("That file could not be imported.",
			map[string]any{"file": err.Error()}))
		return
	}

	encoded, err := json.Marshal(report)
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	status := "preview"
	if report.ValidRows == 0 {
		// Nothing importable — recorded as failed so it cannot be committed.
		status = "failed"
	}

	record, err := a.queries.CreateCSVImport(ctx, store.CreateCSVImportParams{
		SupplierID: supplierID,
		Filename:   filename,
		Status:     status,
		TotalRows:  int32(report.TotalRows),
		ValidRows:  int32(report.ValidRows),
		ErrorRows:  int32(report.ErrorRows),
		Report:     encoded,
	})
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	a.respond(ctx, w, http.StatusCreated, map[string]any{
		"import_id":  record.ID.String(),
		"filename":   record.Filename,
		"status":     record.Status,
		"total_rows": report.TotalRows,
		"valid_rows": report.ValidRows,
		"error_rows": report.ErrorRows,
		"rows":       report.Rows,
		"products":   report.Products,
	})
}

// ---------------------------------------------------------------------------
// GET /imports/{id}
// ---------------------------------------------------------------------------

// GetImport returns a previously-uploaded preview.
func (a *API) GetImport(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	supplierID, err := supplierFromRequest(r)
	if err != nil {
		a.fail(ctx, w, err)
		return
	}
	record, report, err := a.loadImport(ctx, r, supplierID)
	if err != nil {
		a.fail(ctx, w, err)
		return
	}

	a.respond(ctx, w, http.StatusOK, map[string]any{
		"import_id":  record.ID.String(),
		"filename":   record.Filename,
		"status":     record.Status,
		"total_rows": record.TotalRows,
		"valid_rows": record.ValidRows,
		"error_rows": record.ErrorRows,
		"rows":       report.Rows,
		"products":   report.Products,
	})
}

// loadImport fetches a supplier's own import and decodes its stored report.
func (a *API) loadImport(
	ctx context.Context, r *http.Request, supplierID uuid.UUID,
) (store.CsvImport, *csvimport.Report, error) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		return store.CsvImport{}, nil, httpx.NotFound("Import not found.")
	}

	// Supplier-scoped: one supplier cannot read or commit another's import.
	record, err := a.queries.GetCSVImport(ctx, store.GetCSVImportParams{
		ID: id, SupplierID: supplierID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return store.CsvImport{}, nil, httpx.NotFound("Import not found.")
		}
		return store.CsvImport{}, nil, httpx.Internal(err)
	}

	var report csvimport.Report
	if len(record.Report) > 0 {
		if err := json.Unmarshal(record.Report, &report); err != nil {
			return store.CsvImport{}, nil, httpx.Internal(err)
		}
	}
	return record, &report, nil
}

// ---------------------------------------------------------------------------
// POST /imports/{id}/commit — step two: write the valid rows
// ---------------------------------------------------------------------------

// CommitImport writes every valid row from a previewed import.
//
// Re-reads the stored report rather than re-parsing an upload: the supplier
// approved a specific preview, and committing anything else would break that
// promise. All rows land in ONE transaction (CLAUDE.md §6.5), so a failure
// half way through leaves the catalogue untouched.
func (a *API) CommitImport(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	supplierID, err := supplierFromRequest(r)
	if err != nil {
		a.fail(ctx, w, err)
		return
	}
	record, report, err := a.loadImport(ctx, r, supplierID)
	if err != nil {
		a.fail(ctx, w, err)
		return
	}

	switch record.Status {
	case "committed":
		a.fail(ctx, w, httpx.Conflict("IMPORT_ALREADY_COMMITTED",
			"This import has already been applied."))
		return
	case "failed":
		a.fail(ctx, w, httpx.Conflict("IMPORT_HAS_NO_VALID_ROWS",
			"This import has no valid rows to apply."))
		return
	}
	if len(report.Products) == 0 {
		a.fail(ctx, w, httpx.Conflict("IMPORT_HAS_NO_VALID_ROWS",
			"This import has no valid rows to apply."))
		return
	}

	// Images referenced by URL are fetched and re-hosted BEFORE the
	// transaction opens: a slow remote server must not hold a database
	// transaction open for the duration.
	mediaKeys := a.fetchRowMedia(ctx, supplierID, report.Products)

	tx, err := a.pool.Begin(ctx)
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit
	q := a.queries.WithTx(tx)

	// Claim the import first, filtered on status='preview'. Two concurrent
	// commits therefore resolve to exactly one winner.
	if _, err := q.MarkCSVImportCommitted(ctx, store.MarkCSVImportCommittedParams{
		ID: record.ID, SupplierID: supplierID,
	}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			a.fail(ctx, w, httpx.Conflict("IMPORT_ALREADY_COMMITTED",
				"This import has already been applied."))
			return
		}
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	created := 0
	for _, draft := range report.Products {
		product, err := q.CreateProduct(ctx, store.CreateProductParams{
			SupplierID:  supplierID,
			Name:        draft.Name,
			Type:        draft.Type,
			Grade:       optional(draft.Grade, 40),
			Description: optional(draft.Description, 2000),
			// Imported products start as drafts: a bulk upload should not put
			// produce on sale before the supplier has looked at it.
			Status: statusDraft,
			// New produce starts on the platform's default rate
			// (PRODUCT_MARKUP_DEFAULT_BPS); an admin can change it afterwards.
			MarkupBps: a.rates.DefaultMarkupBPS,
		})
		if err != nil {
			if isUniqueViolation(err) {
				a.fail(ctx, w, httpx.Conflict("PRODUCT_EXISTS", fmt.Sprintf(
					"You already have a product named %q at grade %q. "+
						"Remove it from the file or archive the existing one.",
					draft.Name, draft.Grade)))
				return
			}
			a.fail(ctx, w, httpx.Internal(err))
			return
		}

		// The whole grade tree for this draft, built the same way a hand
		// written product is, so both paths land on replaceSizeCodes and
		// cannot drift.
		sizeCodes := make([]parsedSizeCode, 0, len(draft.SizeCodes))
		for _, size := range draft.SizeCodes {
			packs := make([]parsedUnit, 0, len(size.Units))
			for _, u := range size.Units {
				packs = append(packs, parsedUnit{
					Label:       u.Label,
					WeightGrams: u.WeightGrams,
					PricePaise:  u.PricePaise,
					IsActive:    true,
					SortOrder:   u.SortOrder,
				})
			}

			// Only the media that actually downloaded. A dead link costs the
			// grade that one picture, not the import (see fetchRowMedia), and
			// the gallery is capped the same way a hand-built one is.
			media := make([]parsedMedia, 0, len(size.Media))
			for _, item := range size.Media {
				stored, ok := mediaKeys[item.URL]
				if !ok {
					continue
				}
				if len(media) >= a.limits.MaxMediaPerSizeCode {
					break
				}
				contentType := stored.contentType
				media = append(media, parsedMedia{
					Kind:        stored.kind,
					ObjectKey:   stored.key,
					ContentType: &contentType,
					SortOrder:   int32(len(media)),
				})
			}

			sizeCodes = append(sizeCodes, parsedSizeCode{
				Code:      size.Code,
				Meta:      optional(size.Meta, 80),
				IsActive:  true,
				SortOrder: int32(len(sizeCodes)),
				Media:     media,
				Packs:     packs,
			})
		}

		if _, err := a.replaceSizeCodes(ctx, q, product.ID, sizeCodes); err != nil {
			a.fail(ctx, w, err)
			return
		}
		if err := analyticsevents.Emit(ctx, q, product.ID, analyticsevents.ProductCreated,
			analyticsevents.ActorSupplier, analyticsevents.SourceCSVImport, time.Now()); err != nil {
			a.fail(ctx, w, httpx.Internal(err))
			return
		}
		created++
	}

	if err := tx.Commit(ctx); err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	a.respond(ctx, w, http.StatusOK, map[string]any{
		"import_id":        record.ID.String(),
		"status":           "committed",
		"products_created": created,
		"rows_imported":    record.ValidRows,
		"rows_skipped":     record.ErrorRows,
		"media_downloaded": len(mediaKeys),
	})
}

// fetchRowImages downloads each distinct image_url and re-hosts it in our own
// storage, returning url -> object key.
//
// Re-hosting rather than storing the remote URL (CLAUDE.md §6.5): a supplier's
// link will rot, and serving customer-facing images from a third party we do
// not control is both a privacy and an availability problem.
//
// Best-effort: a failed download costs the product that one picture, not the
// import.
func (a *API) fetchRowMedia(
	ctx context.Context, supplierID uuid.UUID, products []csvimport.DraftProduct,
) map[string]storedMedia {
	keys := map[string]storedMedia{}

	for _, product := range products {
		for _, item := range allDraftMedia(product) {
			url := strings.TrimSpace(item.URL)
			if url == "" {
				continue
			}
			// The same URL across two products is downloaded once and shared.
			if _, done := keys[url]; done {
				continue
			}

			stored, err := a.fetchAndStoreMedia(ctx, supplierID, url)
			if err != nil {
				a.logger.WarnContext(ctx, "could not import product media from URL",
					slog2(err), slogStr("url", url))
				continue
			}
			// A column saying "video" that serves a JPEG is a supplier
			// mistake, not ours to correct silently — but the file is what it
			// is, so the fetched content type decides the kind, and the
			// column only decides where we looked.
			keys[url] = stored
		}
	}
	return keys
}

// allDraftMedia flattens a draft's media across every grade, so one pass
// downloads each distinct URL once however many grades reference it.
func allDraftMedia(product csvimport.DraftProduct) []csvimport.DraftMedia {
	var out []csvimport.DraftMedia
	for _, size := range product.SizeCodes {
		out = append(out, size.Media...)
	}
	return out
}

// storedMedia is one successfully re-hosted file.
type storedMedia struct {
	kind        string
	key         string
	contentType string
}

func (a *API) fetchAndStoreMedia(
	ctx context.Context, supplierID uuid.UUID, url string,
) (storedMedia, error) {
	ctx, cancel := context.WithTimeout(ctx, imageFetchTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return storedMedia{}, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return storedMedia{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return storedMedia{}, fmt.Errorf("remote returned %d", resp.StatusCode)
	}

	contentType := strings.ToLower(resp.Header.Get("Content-Type"))
	if i := strings.IndexByte(contentType, ';'); i >= 0 {
		contentType = strings.TrimSpace(contentType[:i])
	}
	// The SERVED content type decides what this is, not the column it came
	// from and not the extension on the URL: the allow-list is the same one
	// the presigned upload path enforces, so a CSV cannot smuggle in a file
	// type a direct upload would refuse.
	media, allowed := allowedMediaTypes[contentType]
	if !allowed {
		return storedMedia{}, fmt.Errorf("unsupported content type %q", contentType)
	}

	maxBytes := a.limits.MaxImageBytes
	if media.kind == mediaKindVideo {
		maxBytes = a.limits.MaxVideoBytes
	}

	// Read one byte past the limit so an oversized file is detected without
	// trusting the remote server's Content-Length.
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return storedMedia{}, err
	}
	if int64(len(body)) > maxBytes {
		return storedMedia{}, fmt.Errorf("%s exceeds %d bytes", media.kind, maxBytes)
	}

	key := fmt.Sprintf("products/%s/%s%s", supplierID, uuid.NewString(), media.extension)
	if err := a.storage.PutObject(ctx, key, contentType, body); err != nil {
		return storedMedia{}, err
	}
	return storedMedia{kind: media.kind, key: key, contentType: contentType}, nil
}

// ---------------------------------------------------------------------------
// GET /imports
// ---------------------------------------------------------------------------

// ListImports returns the supplier's recent imports.
func (a *API) ListImports(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	supplierID, err := supplierFromRequest(r)
	if err != nil {
		a.fail(ctx, w, err)
		return
	}

	records, err := a.queries.ListCSVImports(ctx, store.ListCSVImportsParams{
		SupplierID: supplierID, Limit: 20,
	})
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	out := make([]map[string]any, 0, len(records))
	for _, rec := range records {
		out = append(out, map[string]any{
			"import_id":  rec.ID.String(),
			"filename":   rec.Filename,
			"status":     rec.Status,
			"total_rows": rec.TotalRows,
			"valid_rows": rec.ValidRows,
			"error_rows": rec.ErrorRows,
			"created_at": rec.CreatedAt.Format(timeFormat),
		})
	}
	a.respond(ctx, w, http.StatusOK, map[string]any{"imports": out})
}
