package api

import (
	"errors"
	"fmt"
	"net/http"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vayal-mikrogreenz/vm-go-common/grams"
	"github.com/vayal-mikrogreenz/vm-go-common/httpx"

	"github.com/vayal-mikrogreenz/vm-catalog-api/internal/availability"
	"github.com/vayal-mikrogreenz/vm-catalog-api/internal/store"
)

// Hold settlement outcomes.
const (
	holdCommitted = "committed"
	holdReleased  = "released"
)

// maxImageProducts bounds an image lookup. A page of twenty orders with a few
// lines each sits well inside this; each id costs one presigned signature.
const maxImageProducts = 500

// reserveLine is one grade's worth of grams to hold. Keyed on the SIZE CODE,
// because that is what owns the gram pool.
type reserveLine struct {
	SizeCodeID string `json:"size_code_id"`
	Grams      int32  `json:"grams"`
}

type reserveRequest struct {
	// OrderRef is the orders-side order id these holds belong to.
	OrderRef   string        `json:"order_ref"`
	TTLSeconds int           `json:"ttl_seconds"`
	Lines      []reserveLine `json:"lines"`
}

type heldLine struct {
	HoldID     string `json:"hold_id"`
	ProductID  string `json:"product_id"`
	SizeCodeID string `json:"size_code_id"`
	Grams      int32  `json:"grams"`
	ExpiresAt  string `json:"expires_at"`
}

// Reserve holds stock for an order — CLAUDE.md §6.3 step 1.
//
// Service-to-service only. The whole reservation succeeds or none of it does:
// a customer must never end up paying for an order where only some lines were
// actually secured.
//
// This is the concurrency-critical path on the platform. Correctness rests on
// three things:
//
//   - SELECT ... FOR UPDATE on each availability row, so two checkouts for the
//     same product serialise rather than both reading the same "remaining".
//   - Locking in a deterministic order (sorted by size_code_id). Without it,
//     two carts holding {A,B} and {B,A} can deadlock.
//   - Re-checking remaining grams AFTER acquiring the lock, never before.
func (a *API) Reserve(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req reserveRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		a.fail(ctx, w, err)
		return
	}

	orderRef, err := uuid.Parse(req.OrderRef)
	if err != nil {
		a.fail(ctx, w, httpx.BadRequest("order_ref must be a UUID."))
		return
	}
	if len(req.Lines) == 0 {
		a.fail(ctx, w, httpx.BadRequest("At least one line is required."))
		return
	}
	ttl := time.Duration(req.TTLSeconds) * time.Second
	if ttl <= 0 || ttl > time.Hour {
		a.fail(ctx, w, httpx.BadRequest("ttl_seconds must be between 1 and 3600."))
		return
	}

	// Collapse duplicate GRADES and sort, so the lock order is deterministic
	// for every caller regardless of cart ordering.
	//
	// Grades, not products: the gram pool belongs to the size code now, so two
	// customers buying M and XL of the same produce take different locks and
	// no longer wait on each other.
	gramsBySizeCode := map[uuid.UUID]int32{}
	for _, line := range req.Lines {
		id, err := uuid.Parse(line.SizeCodeID)
		if err != nil {
			a.fail(ctx, w, httpx.BadRequest("size_code_id must be a UUID."))
			return
		}
		if line.Grams <= 0 {
			a.fail(ctx, w, httpx.BadRequest("grams must be greater than zero."))
			return
		}
		gramsBySizeCode[id] += line.Grams
	}

	sizeCodeIDs := make([]uuid.UUID, 0, len(gramsBySizeCode))
	for id := range gramsBySizeCode {
		sizeCodeIDs = append(sizeCodeIDs, id)
	}
	sort.Slice(sizeCodeIDs, func(i, j int) bool {
		return sizeCodeIDs[i].String() < sizeCodeIDs[j].String()
	})

	// The business day is computed HERE, server-side in IST. A client-supplied
	// date must never decide what stock is purchasable (CLAUDE.md rule 2).
	today := availability.Today()
	expiresAt := time.Now().Add(ttl)

	tx, err := a.pool.Begin(ctx)
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit
	q := a.queries.WithTx(tx)

	held := make([]heldLine, 0, len(sizeCodeIDs))

	for _, sizeCodeID := range sizeCodeIDs {
		// Named wantGrams, not grams: the package of the same name formats
		// the figure quoted back to the customer below.
		wantGrams := gramsBySizeCode[sizeCodeID]

		// Blocks until any competing checkout for this GRADE commits or rolls
		// back. Everything read after this point is current.
		row, err := q.LockAvailabilityForUpdate(ctx, store.LockAvailabilityForUpdateParams{
			SizeCodeID: sizeCodeID, AvailableOn: today,
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				a.fail(ctx, w, httpx.Conflict("NOT_AVAILABLE_TODAY",
					"One of these products is not available today."))
				return
			}
			a.fail(ctx, w, httpx.Internal(err))
			return
		}

		sheet := availability.Sheet{
			AvailableOn:   row.AvailableOn,
			TotalGrams:    row.TotalGrams,
			ReservedGrams: row.ReservedGrams,
			SoldGrams:     row.SoldGrams,
			Status:        row.Status,
		}

		// Re-checked under the lock, not before it. This is the check that
		// actually prevents overselling.
		if !sheet.SellableOn(today) {
			a.fail(ctx, w, httpx.Conflict("NOT_AVAILABLE_TODAY",
				"One of these products is no longer available today."))
			return
		}
		if sheet.RemainingGrams() < wantGrams {
			// Name the produce and quote the figure. "One of these items" was
			// true and useless: the customer is looking at a cart of five
			// things and has to guess which one to change, and by how much.
			// The name costs one extra query on a path that is already
			// failing.
			// Name the produce AND the grade: "Tomato has sold out" is wrong
			// when only the XL has, and a customer looking at a cart of five
			// things should not have to guess which line to change.
			name := "One of these items"
			if product, lookupErr := q.GetProduct(ctx, row.ProductID); lookupErr == nil {
				name = product.Name
			}
			sizeCode := ""
			if sc, lookupErr := q.GetSizeCode(ctx, sizeCodeID); lookupErr == nil {
				sizeCode = sc.Code
				// The implicit grade of an ungraded listing is left off — a
				// customer should never be shown a placeholder code.
				if sc.Code != defaultSizeCode {
					name = name + " (" + sc.Code + ")"
				}
			}
			remaining := sheet.RemainingGrams()
			message := fmt.Sprintf("%s has sold out for today.", name)
			if remaining > 0 {
				message = fmt.Sprintf("Only %s of %s is left today.",
					grams.Format(remaining), name)
			}
			a.fail(ctx, w, httpx.Conflict("INSUFFICIENT_STOCK", message).
				WithDetails(map[string]any{
					"product_id":      row.ProductID.String(),
					"product_name":    name,
					"size_code_id":    sizeCodeID.String(),
					"size_code":       sizeCode,
					"remaining_grams": remaining,
					"requested_grams": wantGrams,
				}))
			return
		}

		if _, err := q.IncrementReservedGrams(ctx, store.IncrementReservedGramsParams{
			SizeCodeID: sizeCodeID, AvailableOn: today, ReservedGrams: wantGrams,
		}); err != nil {
			a.fail(ctx, w, httpx.Internal(err))
			return
		}

		hold, err := q.CreateStockHold(ctx, store.CreateStockHoldParams{
			OrderRef:    orderRef,
			ProductID:   row.ProductID,
			SizeCodeID:  sizeCodeID,
			AvailableOn: today,
			Grams:       wantGrams,
			ExpiresAt:   expiresAt,
		})
		if err != nil {
			a.fail(ctx, w, httpx.Internal(err))
			return
		}

		held = append(held, heldLine{
			HoldID:     hold.ID.String(),
			ProductID:  hold.ProductID.String(),
			SizeCodeID: sizeCodeID.String(),
			Grams:      wantGrams,
			ExpiresAt:  hold.ExpiresAt.Format(time.RFC3339),
		})
	}

	if err := tx.Commit(ctx); err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	a.respond(ctx, w, http.StatusCreated, map[string]any{
		"order_ref":  orderRef.String(),
		"holds":      held,
		"expires_at": expiresAt.Format(time.RFC3339),
	})
}

type productIDsRequest struct {
	ProductIDs []string `json:"product_ids"`
}

type productImageLine struct {
	ProductID string `json:"product_id"`
	ImageURL  string `json:"image_url"`
}

// ProductImages returns a presigned image URL per product.
//
// Service-to-service. vm-orders-api calls it to put thumbnails on an order,
// which is the one thing an order line cannot snapshot: what products store is
// an object KEY, and the URL is minted at read time so moving bucket or host
// never breaks an old row (CLAUDE.md §5.2, §6.6).
//
// Products with no photograph are simply absent from the response, as are ids
// that no longer exist. The caller shows a placeholder, which is what it does
// for a product that never had a photo anyway.
func (a *API) ProductImages(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req productIDsRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		a.fail(ctx, w, err)
		return
	}
	if len(req.ProductIDs) == 0 {
		a.fail(ctx, w, httpx.BadRequest("At least one product_id is required."))
		return
	}
	if len(req.ProductIDs) > maxImageProducts {
		a.fail(ctx, w, httpx.BadRequest("Too many product_ids in one request."))
		return
	}

	ids := make([]uuid.UUID, 0, len(req.ProductIDs))
	for _, raw := range req.ProductIDs {
		id, err := uuid.Parse(raw)
		if err != nil {
			a.fail(ctx, w, httpx.BadRequest("product_ids must be UUIDs."))
			return
		}
		ids = append(ids, id)
	}

	rows, err := a.queries.ProductImagesForIDs(ctx, ids)
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	lines := make([]productImageLine, 0, len(rows))
	for _, row := range rows {
		// One signature per product, not per order line: a page of orders that
		// all contain the same tomatoes signs once. The query already returns
		// one cover row per product, and only for products that have one.
		signed, signErr := a.storage.PresignGet(ctx, row.ImageKey)
		if signErr != nil {
			// A key that cannot be signed is a placeholder on a card, not a
			// failed order history.
			continue
		}
		lines = append(lines, productImageLine{
			ProductID: row.ID.String(),
			ImageURL:  signed,
		})
	}

	a.respond(ctx, w, http.StatusOK, map[string]any{"products": lines})
}

type settleRequest struct {
	// Outcome is "committed" (payment succeeded) or "released" (cancelled,
	// failed or expired).
	Outcome string   `json:"outcome"`
	HoldIDs []string `json:"hold_ids"`
}

// SettleHolds commits or releases previously-held stock — §6.3 steps 2 and 3.
//
// Idempotent: settling an already-settled hold is a no-op, because the update
// is filtered on status='held'. Without that filter a retried release would
// return the same grams twice and inflate available stock.
func (a *API) SettleHolds(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req settleRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		a.fail(ctx, w, err)
		return
	}
	if req.Outcome != holdCommitted && req.Outcome != holdReleased {
		a.fail(ctx, w, httpx.BadRequest(`outcome must be "committed" or "released".`))
		return
	}

	ids := make([]uuid.UUID, 0, len(req.HoldIDs))
	for _, raw := range req.HoldIDs {
		id, err := uuid.Parse(raw)
		if err != nil {
			a.fail(ctx, w, httpx.BadRequest("hold_ids must be UUIDs."))
			return
		}
		ids = append(ids, id)
	}
	// Deterministic order again: settling touches the same availability rows a
	// concurrent reservation might be locking.
	sort.Slice(ids, func(i, j int) bool { return ids[i].String() < ids[j].String() })

	tx, err := a.pool.Begin(ctx)
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit
	q := a.queries.WithTx(tx)

	settled := 0
	for _, id := range ids {
		hold, err := q.SettleStockHold(ctx, store.SettleStockHoldParams{
			ID: id, Status: req.Outcome,
		})
		if err != nil {
			// Already settled, or never existed. Both mean "nothing to do".
			if errors.Is(err, pgx.ErrNoRows) {
				continue
			}
			a.fail(ctx, w, httpx.Internal(err))
			return
		}

		if req.Outcome == holdCommitted {
			// Reserved -> sold. The total is unchanged; the grams simply move
			// from held to committed.
			_, err = q.CommitReservedGrams(ctx, store.CommitReservedGramsParams{
				SizeCodeID: hold.SizeCodeID, AvailableOn: hold.AvailableOn, ReservedGrams: hold.Grams,
			})
		} else {
			_, err = q.DecrementReservedGrams(ctx, store.DecrementReservedGramsParams{
				SizeCodeID: hold.SizeCodeID, AvailableOn: hold.AvailableOn, ReservedGrams: hold.Grams,
			})
		}
		if err != nil {
			a.fail(ctx, w, httpx.Internal(err))
			return
		}
		settled++
	}

	if err := tx.Commit(ctx); err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	a.respond(ctx, w, http.StatusOK, map[string]any{
		"outcome": req.Outcome,
		"settled": settled,
		// Holds already settled by an earlier call or the sweeper.
		"skipped": len(ids) - settled,
	})
}
