package projector

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"github.com/vayal-mikrogreenz/vm-events-consumer-api/internal/store"
)

// productPayload mirrors vm-catalog-api/internal/analyticsevents.Product,
// schema version 1.
type productPayload struct {
	ProductID  uuid.UUID `json:"product_id"`
	SupplierID uuid.UUID `json:"supplier_id"`
	Name       string    `json:"name"`
	Type       string    `json:"type"`
	Grade      string    `json:"grade"`
	Status     string    `json:"status"`
	MarkupBPS  int32     `json:"markup_bps"`
	Source     string    `json:"source"`
	CreatedAt  time.Time `json:"created_at"`
	ImageCount int32     `json:"image_count"`
	VideoCount int32     `json:"video_count"`
	SizeCodes  []struct {
		SizeCodeID      uuid.UUID `json:"size_code_id"`
		Code            string    `json:"code"`
		Meta            string    `json:"meta"`
		HarvestSharePct *int16    `json:"harvest_share_pct"`
		IsActive        bool      `json:"is_active"`
		Packs           []struct {
			PackOptionID       uuid.UUID `json:"pack_option_id"`
			Label              string    `json:"label"`
			WeightGrams        int32     `json:"weight_grams"`
			PricePaise         int64     `json:"price_paise"`
			CustomerPricePaise int64     `json:"customer_price_paise"`
			IsActive           bool      `json:"is_active"`
		} `json:"packs"`
	} `json:"size_codes"`
}

// packVersion is what a pack version records; a change to any of it opens a
// new version.
type packVersion struct {
	SizeCodeID         uuid.UUID
	SizeCode           string
	SizeMeta           *string
	HarvestSharePct    *int16
	SizeCodeActive     bool
	Label              string
	WeightGrams        int32
	PricePaise         int64
	MarkupBPS          int32
	CustomerPricePaise int64
	IsActive           bool
}

func (v packVersion) equal(row store.ProductPack) bool {
	return v.SizeCodeID == row.SizeCodeID &&
		v.SizeCode == row.SizeCode &&
		eqPtr(v.SizeMeta, row.SizeMeta) &&
		eqPtr(v.HarvestSharePct, row.HarvestSharePct) &&
		v.SizeCodeActive == row.SizeCodeActive &&
		v.Label == row.Label &&
		v.WeightGrams == row.WeightGrams &&
		v.PricePaise == row.PricePaise &&
		v.MarkupBPS == row.MarkupBps &&
		v.CustomerPricePaise == row.CustomerPricePaise &&
		v.IsActive == row.IsActive
}

func eqPtr[T comparable](a, b *T) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func projectProduct(ctx context.Context, q *store.Queries, ev Event) error {
	if ev.SchemaVersion != 1 {
		return permanent("product event schema_version %d is not supported", ev.SchemaVersion)
	}
	var p productPayload
	if err := json.Unmarshal(ev.Payload, &p); err != nil {
		return permanent("product payload: %w", err)
	}
	if p.ProductID == uuid.Nil || p.ProductID != ev.AggregateID {
		return permanent("product payload is for %s, event aggregate is %s", p.ProductID, ev.AggregateID)
	}

	// Every pack in the payload, as the version it should be now.
	wanted := map[uuid.UUID]packVersion{}
	var packCount, activeCount int32
	var minPrice, maxPrice *int64
	for _, code := range p.SizeCodes {
		for _, pack := range code.Packs {
			packCount++
			wanted[pack.PackOptionID] = packVersion{
				SizeCodeID: code.SizeCodeID, SizeCode: code.Code, SizeMeta: nonEmpty(code.Meta),
				HarvestSharePct: code.HarvestSharePct, SizeCodeActive: code.IsActive,
				Label: pack.Label, WeightGrams: pack.WeightGrams, PricePaise: pack.PricePaise,
				MarkupBPS: p.MarkupBPS, CustomerPricePaise: pack.CustomerPricePaise,
				IsActive: pack.IsActive,
			}
			// The price range a customer can actually buy at.
			if pack.IsActive && code.IsActive {
				activeCount++
				price := pack.CustomerPricePaise
				if minPrice == nil || price < *minPrice {
					minPrice = &price
				}
				if maxPrice == nil || price > *maxPrice {
					maxPrice = &price
				}
			}
		}
	}

	at := ev.OccurredAt
	var firstActive, archived *time.Time
	switch p.Status {
	case "active":
		firstActive = &at
	case "archived":
		archived = &at
	}

	rows, err := q.UpsertProduct(ctx, store.UpsertProductParams{
		ProductID:       p.ProductID,
		SupplierID:      p.SupplierID,
		Name:            p.Name,
		Type:            p.Type,
		Grade:           nonEmpty(p.Grade),
		Status:          p.Status,
		MarkupBps:       p.MarkupBPS,
		Source:          p.Source,
		SizeCodeCount:   int32(len(p.SizeCodes)),
		PackCount:       packCount,
		ActivePackCount: activeCount,
		MinPricePaise:   minPrice,
		MaxPricePaise:   maxPrice,
		ImageCount:      p.ImageCount,
		VideoCount:      p.VideoCount,
		CreatedAt:       p.CreatedAt,
		FirstActiveAt:   firstActive,
		ArchivedAt:      archived,
		LastEventID:     ev.ID,
		LastEventType:   ev.EventType,
		LastEventAt:     ev.OccurredAt,
	})
	if err != nil {
		return err
	}
	if rows == 0 {
		return nil // stale
	}
	return syncPacks(ctx, q, p.ProductID, wanted, at)
}

// syncPacks makes the product's CURRENT pack versions match `wanted` as of
// `at`: unchanged packs are left alone, changed ones are closed and reopened,
// and packs that are gone are closed. A product save replaces every pack id
// (vm-catalog-api deletes and re-creates the grade tree), so in practice an
// edit closes the old set and opens a new one — which is exactly the history
// an order line's pack id needs to resolve against later.
func syncPacks(
	ctx context.Context, q *store.Queries, productID uuid.UUID,
	wanted map[uuid.UUID]packVersion, at time.Time,
) error {
	current, err := q.CurrentPacksForProduct(ctx, productID)
	if err != nil {
		return err
	}
	open := map[uuid.UUID]store.ProductPack{}
	for _, row := range current {
		open[row.PackOptionID] = row
	}

	for id, row := range open {
		if v, keep := wanted[id]; keep && v.equal(row) {
			continue
		}
		if err := q.ClosePack(ctx, store.ClosePackParams{
			PackOptionID: id, ValidFrom: row.ValidFrom, ValidTo: &at,
		}); err != nil {
			return err
		}
	}
	for id, v := range wanted {
		if row, ok := open[id]; ok && v.equal(row) {
			continue
		}
		if err := q.OpenPack(ctx, store.OpenPackParams{
			PackOptionID: id, ValidFrom: at, ProductID: productID,
			SizeCodeID: v.SizeCodeID, SizeCode: v.SizeCode, SizeMeta: v.SizeMeta,
			HarvestSharePct: v.HarvestSharePct, SizeCodeActive: v.SizeCodeActive,
			Label: v.Label, WeightGrams: v.WeightGrams, PricePaise: v.PricePaise,
			MarkupBps: v.MarkupBPS, CustomerPricePaise: v.CustomerPricePaise,
			IsActive: v.IsActive,
		}); err != nil {
			return err
		}
	}
	return nil
}
