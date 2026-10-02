package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/vayal-mikrogreenz/vm-go-common/httpx"

	"github.com/vayal-mikrogreenz/vm-catalog-api/internal/analyticsevents"
	"github.com/vayal-mikrogreenz/vm-catalog-api/internal/store"
)

const pgUniqueViolation = "23505"

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation
}

// errDuplicateProduct is returned when a supplier already lists the same
// produce at the same grade.
var errDuplicateProduct = httpx.Conflict("PRODUCT_EXISTS",
	"You already have a product with this name and grade.")

// notFound is used for both "no such product" and "not yours": distinguishing
// them would confirm the existence of a competitor's product.
var errProductNotFound = httpx.NotFound("Product not found.")

func productIDFromPath(r *http.Request) (uuid.UUID, error) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		return uuid.Nil, errProductNotFound
	}
	return id, nil
}

// ---------------------------------------------------------------------------
// GET /products — the supplier's own catalogue
// ---------------------------------------------------------------------------

func (a *API) ListProducts(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	supplierID, err := supplierFromRequest(r)
	if err != nil {
		a.fail(ctx, w, err)
		return
	}
	filters, err := parseListFilters(r.URL.Query())
	if err != nil {
		a.fail(ctx, w, err)
		return
	}

	a.listProducts(ctx, w, &supplierID, filters)
}

// listProducts serves both the supplier and admin list views. supplierID is
// nil for admins, which is the ONLY difference between them — so the two can
// never drift apart in filtering or shape.
func (a *API) listProducts(
	ctx context.Context, w http.ResponseWriter, supplierID *uuid.UUID, f listFilters,
) {
	products, err := a.queries.ListProducts(ctx, store.ListProductsParams{
		SupplierID: supplierID,
		Type:       f.Type,
		Status:     f.Status,
		Search:     f.Search,
		Limit:      f.Limit,
		Offset:     f.Offset,
	})
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	total, err := a.queries.CountProducts(ctx, store.CountProductsParams{
		SupplierID: supplierID,
		Type:       f.Type,
		Status:     f.Status,
		Search:     f.Search,
	})
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	trees, err := a.treesByProduct(ctx, products)
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	out := make([]productResponse, 0, len(products))
	for _, p := range products {
		out = append(out, a.toProductResponse(ctx, p, trees[p.ID]))
	}

	a.respond(ctx, w, http.StatusOK, map[string]any{
		"products": out,
		"total":    total,
		"limit":    f.Limit,
		"offset":   f.Offset,
	})
}

// ---------------------------------------------------------------------------
// GET /products/{id}
// ---------------------------------------------------------------------------

func (a *API) GetProduct(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	supplierID, err := supplierFromRequest(r)
	if err != nil {
		a.fail(ctx, w, err)
		return
	}
	id, err := productIDFromPath(r)
	if err != nil {
		a.fail(ctx, w, err)
		return
	}

	// Ownership is in the WHERE clause: another supplier's id simply returns
	// no rows.
	product, err := a.queries.GetProductForSupplier(ctx, store.GetProductForSupplierParams{
		ID: id, SupplierID: supplierID,
	})
	if err != nil {
		a.fail(ctx, w, a.lookupError(err))
		return
	}

	tree, err := a.treeForProduct(ctx, product)
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}
	a.respond(ctx, w, http.StatusOK, a.toProductResponse(ctx, product, tree))
}

func (a *API) lookupError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return errProductNotFound
	}
	return httpx.Internal(err)
}

// ---------------------------------------------------------------------------
// POST /products
// ---------------------------------------------------------------------------

func (a *API) CreateProduct(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	supplierID, err := supplierFromRequest(r)
	if err != nil {
		a.fail(ctx, w, err)
		return
	}

	var payload productPayload
	if err := httpx.DecodeJSON(w, r, &payload); err != nil {
		a.fail(ctx, w, err)
		return
	}
	parsed, err := validateProduct(payload, a.productLimits())
	if err != nil {
		a.fail(ctx, w, err)
		return
	}

	status := payload.Status
	if status == "" {
		status = statusDraft
	}

	// The product and its units must appear together — a product with no
	// units cannot be bought, so a partial write is never a valid state.
	tx, err := a.pool.Begin(ctx)
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit
	q := a.queries.WithTx(tx)

	product, err := q.CreateProduct(ctx, store.CreateProductParams{
		SupplierID:  supplierID,
		Name:        parsed.Name,
		Type:        parsed.Type,
		Grade:       parsed.Grade,
		Description: parsed.Description,
		Status:      status,
		// New produce starts on the platform's default rate
		// (PRODUCT_MARKUP_DEFAULT_BPS); an admin can change it afterwards.
		MarkupBps: a.rates.DefaultMarkupBPS,
	})
	if err != nil {
		if isUniqueViolation(err) {
			a.fail(ctx, w, errDuplicateProduct)
			return
		}
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	tree, err := a.replaceSizeCodes(ctx, q, product.ID, parsed.SizeCodes)
	if err != nil {
		a.fail(ctx, w, err)
		return
	}
	tree.CommonMedia, err = a.replaceCommonMedia(ctx, q, product.ID, parsed.Media)
	if err != nil {
		a.fail(ctx, w, err)
		return
	}
	if err := analyticsevents.Emit(ctx, q, product.ID, analyticsevents.ProductCreated,
		analyticsevents.ActorSupplier, analyticsevents.SourceForm, time.Now()); err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}
	if err := tx.Commit(ctx); err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	a.respond(ctx, w, http.StatusCreated, a.toProductResponse(ctx, product, tree))
}

// ---------------------------------------------------------------------------
// PUT /products/{id}
// ---------------------------------------------------------------------------

// UpdateProduct replaces a product and its ENTIRE grade tree.
//
// Replace rather than merge: the supplier UI edits units as a list, and a
// merge would need stable client-side ids for rows the supplier is still
// typing. Replacing inside one transaction means the unit set a supplier sees
// after saving is exactly the one they submitted.
func (a *API) UpdateProduct(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	supplierID, err := supplierFromRequest(r)
	if err != nil {
		a.fail(ctx, w, err)
		return
	}
	id, err := productIDFromPath(r)
	if err != nil {
		a.fail(ctx, w, err)
		return
	}

	var payload productPayload
	if err := httpx.DecodeJSON(w, r, &payload); err != nil {
		a.fail(ctx, w, err)
		return
	}
	parsed, err := validateProduct(payload, a.productLimits())
	if err != nil {
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

	existing, err := q.GetProductForSupplier(ctx, store.GetProductForSupplierParams{
		ID: id, SupplierID: supplierID,
	})
	if err != nil {
		a.fail(ctx, w, a.lookupError(err))
		return
	}

	status := payload.Status
	if status == "" {
		status = existing.Status
	}

	product, err := q.UpdateProduct(ctx, store.UpdateProductParams{
		ID:          id,
		SupplierID:  supplierID,
		Name:        parsed.Name,
		Type:        parsed.Type,
		Grade:       parsed.Grade,
		Description: parsed.Description,
		Status:      status,
	})
	if err != nil {
		if isUniqueViolation(err) {
			a.fail(ctx, w, errDuplicateProduct)
			return
		}
		a.fail(ctx, w, a.lookupError(err))
		return
	}

	tree, err := a.replaceSizeCodes(ctx, q, product.ID, parsed.SizeCodes)
	if err != nil {
		a.fail(ctx, w, err)
		return
	}
	tree.CommonMedia, err = a.replaceCommonMedia(ctx, q, product.ID, parsed.Media)
	if err != nil {
		a.fail(ctx, w, err)
		return
	}
	if err := analyticsevents.Emit(ctx, q, product.ID, analyticsevents.ProductUpdated,
		analyticsevents.ActorSupplier, analyticsevents.SourceForm, time.Now()); err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}
	if err := tx.Commit(ctx); err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	a.respond(ctx, w, http.StatusOK, a.toProductResponse(ctx, product, tree))
}

// replaceSizeCodes deletes and re-inserts a product's ENTIRE grade tree —
// every size code, and beneath each one its gallery and its pack options.
//
// Must be called inside a transaction: between the delete and the inserts the
// product has no grades at all, and that state must never be observable.
//
// Replace rather than merge, as the unit set always was: the supplier edits
// the tree as ordered lists, and a merge would need stable client-side ids for
// rows they are still typing. After a save the tree they see is exactly the
// one they submitted.
//
// The delete cascades to pack options and media through their composite
// foreign keys, so this issues one delete rather than three. Storage objects
// are NOT removed: a key dropped from a gallery may be re-added a moment
// later, and orphaned objects are a housekeeping problem, not a correctness
// one.
func (a *API) replaceSizeCodes(
	ctx context.Context, q *store.Queries, productID uuid.UUID, sizeCodes []parsedSizeCode,
) (productTree, error) {
	if _, err := q.DeleteSizeCodesForProduct(ctx, productID); err != nil {
		return productTree{}, httpx.Internal(err)
	}

	tree := productTree{
		SizeCodes: make([]store.ProductSizeCode, 0, len(sizeCodes)),
		Packs:     map[uuid.UUID][]store.ProductPackOption{},
		Media:     map[uuid.UUID][]store.ProductMedium{},
	}

	for _, sc := range sizeCodes {
		created, err := q.CreateSizeCode(ctx, store.CreateSizeCodeParams{
			ProductID:       productID,
			Code:            sc.Code,
			Meta:            sc.Meta,
			IsActive:        sc.IsActive,
			SortOrder:       sc.SortOrder,
			HarvestSharePct: sc.HarvestSharePct,
		})
		if err != nil {
			if isUniqueViolation(err) {
				// Validation already rejects a repeated code, so reaching here
				// means two requests raced.
				return productTree{}, httpx.Conflict("DUPLICATE_SIZE_CODE",
					"Two size codes cannot have the same code.")
			}
			return productTree{}, httpx.Internal(err)
		}
		tree.SizeCodes = append(tree.SizeCodes, created)

		for _, pack := range sc.Packs {
			row, err := q.CreatePackOption(ctx, store.CreatePackOptionParams{
				SizeCodeID:  created.ID,
				ProductID:   productID,
				Label:       pack.Label,
				Meta:        pack.Meta,
				WeightGrams: pack.WeightGrams,
				PricePaise:  pack.PricePaise,
				IsActive:    pack.IsActive,
				SortOrder:   pack.SortOrder,
			})
			if err != nil {
				if isUniqueViolation(err) {
					return productTree{}, httpx.Conflict("DUPLICATE_PACK_WEIGHT",
						"Two pack options in one size code cannot have the same weight.")
				}
				return productTree{}, httpx.Internal(err)
			}
			tree.Packs[created.ID] = append(tree.Packs[created.ID], row)
		}

		for _, m := range sc.Media {
			row, err := q.InsertProductMedia(ctx, store.InsertProductMediaParams{
				SizeCodeID:  &created.ID,
				ProductID:   productID,
				Kind:        m.Kind,
				ObjectKey:   m.ObjectKey,
				ContentType: m.ContentType,
				SortOrder:   m.SortOrder,
			})
			if err != nil {
				if isUniqueViolation(err) {
					return productTree{}, httpx.Conflict("DUPLICATE_MEDIA",
						"The same file cannot be attached to a size code twice.")
				}
				return productTree{}, httpx.Internal(err)
			}
			tree.Media[created.ID] = append(tree.Media[created.ID], row)
		}
	}
	return tree, nil
}

// replaceCommonMedia deletes and re-inserts a product's PRODUCT-LEVEL gallery
// — the pictures that are of the farm rather than of a grade.
//
// Separate from replaceSizeCodes because the two buckets are separate: a grade
// delete cascades its own media, and common media deliberately survives that
// (its size_code_id is NULL, so the composite foreign key does not reach it).
// Must be called inside the same transaction, for the same reason: between the
// delete and the inserts the product has no common gallery at all.
//
// Only the hand-written product paths call it. A CSV import creates products
// and has no column for this bucket, so there is nothing there to replace —
// and calling it would empty a gallery the file never mentioned.
func (a *API) replaceCommonMedia(
	ctx context.Context, q *store.Queries, productID uuid.UUID, media []parsedMedia,
) ([]store.ProductMedium, error) {
	if _, err := q.DeleteCommonMediaForProduct(ctx, productID); err != nil {
		return nil, httpx.Internal(err)
	}

	out := make([]store.ProductMedium, 0, len(media))
	for _, m := range media {
		row, err := q.InsertProductMedia(ctx, store.InsertProductMediaParams{
			// NULL: this row belongs to the product, not to any one grade.
			SizeCodeID:  nil,
			ProductID:   productID,
			Kind:        m.Kind,
			ObjectKey:   m.ObjectKey,
			ContentType: m.ContentType,
			SortOrder:   m.SortOrder,
		})
		if err != nil {
			if isUniqueViolation(err) {
				return nil, httpx.Conflict("DUPLICATE_MEDIA",
					"The same file cannot be attached to a product twice.")
			}
			return nil, httpx.Internal(err)
		}
		out = append(out, row)
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// POST /products/{id}/archive
// ---------------------------------------------------------------------------

// ArchiveProduct retires a product without deleting it.
//
// Archive, never delete: orders snapshot product details, but a supplier's own
// history and any in-flight order must still resolve the row.
func (a *API) ArchiveProduct(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	supplierID, err := supplierFromRequest(r)
	if err != nil {
		a.fail(ctx, w, err)
		return
	}
	id, err := productIDFromPath(r)
	if err != nil {
		a.fail(ctx, w, err)
		return
	}

	product, err := a.changeWithEvent(ctx, analyticsevents.ProductArchived,
		analyticsevents.ActorSupplier, analyticsevents.SourceForm,
		func(q *store.Queries) (store.Product, error) {
			return q.ArchiveProductForSupplier(ctx,
				store.ArchiveProductForSupplierParams{ID: id, SupplierID: supplierID})
		})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Either not theirs, or already archived. Idempotent from the
			// caller's point of view, so report the current state.
			a.fail(ctx, w, errProductNotFound)
			return
		}
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	tree, err := a.treeForProduct(ctx, product)
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}
	a.respond(ctx, w, http.StatusOK, a.toProductResponse(ctx, product, tree))
}

// changeWithEvent runs a one-statement product change and its analytics event
// in one transaction (CLAUDE.md §5.4), so the product can never change without
// analytics hearing of it. The change's own errors are returned untouched, so
// callers keep their pgx.ErrNoRows handling.
func (a *API) changeWithEvent(
	ctx context.Context, eventType, actor, source string,
	change func(*store.Queries) (store.Product, error),
) (store.Product, error) {
	tx, err := a.pool.Begin(ctx)
	if err != nil {
		return store.Product{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit
	q := a.queries.WithTx(tx)

	product, err := change(q)
	if err != nil {
		return store.Product{}, err
	}
	if err := analyticsevents.Emit(ctx, q, product.ID, eventType, actor, source,
		time.Now()); err != nil {
		return store.Product{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return store.Product{}, err
	}
	return product, nil
}

// ---------------------------------------------------------------------------
// Admin
// ---------------------------------------------------------------------------

// AdminArchiveProduct archives any product — the takedown lever for produce
// that should not be on sale.
func (a *API) AdminArchiveProduct(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	id, err := productIDFromPath(r)
	if err != nil {
		a.fail(ctx, w, err)
		return
	}

	product, err := a.changeWithEvent(ctx, analyticsevents.ProductArchived,
		analyticsevents.ActorAdmin, analyticsevents.SourceAdmin,
		func(q *store.Queries) (store.Product, error) {
			return q.ArchiveProductAsAdmin(ctx, id)
		})
	if err != nil {
		a.fail(ctx, w, a.lookupError(err))
		return
	}
	tree, err := a.treeForProduct(ctx, product)
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}
	a.respond(ctx, w, http.StatusOK, a.toProductResponse(ctx, product, tree))
}
