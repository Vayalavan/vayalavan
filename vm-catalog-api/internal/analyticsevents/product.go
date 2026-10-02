// Package analyticsevents writes product events to product_events_outbox, the
// catalog half of the analytics pipeline (CLAUDE.md §5.4).
//
// Every event carries the whole product as analytics may see it AFTER the
// change — grades, packs, prices, markup — so the consumer upserts rather
// than replays deltas, and a missed or repeated event still converges.
//
// The payload is built from the types below and nothing else. There is no
// field for the description (free text a grower writes), media object keys,
// or anything about the grower beyond their id: the analytics role sees
// counts and prices, not content.
package analyticsevents

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/vayal-mikrogreenz/vm-go-common/logging"

	"github.com/vayal-mikrogreenz/vm-catalog-api/internal/pricing"
	"github.com/vayal-mikrogreenz/vm-catalog-api/internal/store"
)

// SchemaVersion is the payload shape below. Bump it on any change a consumer
// would have to handle differently, and teach the consumer first.
const SchemaVersion = 1

// Event types. Must match the CHECK on product_events_outbox.event_type.
const (
	ProductCreated       = "product.created"
	ProductUpdated       = "product.updated"
	ProductMarkupChanged = "product.markup_changed"
	ProductArchived      = "product.archived"
	ProductSnapshot      = "product.snapshot"
)

// Actors. Must match the CHECK on product_events_outbox.actor_role.
const (
	ActorSupplier = "supplier"
	ActorAdmin    = "admin"
	ActorSystem   = "system"
)

// Sources: how the change was made.
const (
	SourceForm      = "form"
	SourceCSVImport = "csv_import"
	SourceAdmin     = "admin"
	SourceBackfill  = "backfill"
	// Created by the development seed (mocks/).
	SourceSeed = "seed"
)

// Product is the analytics view of a product.
type Product struct {
	ProductID  uuid.UUID `json:"product_id"`
	SupplierID uuid.UUID `json:"supplier_id"`
	Name       string    `json:"name"`
	Type       string    `json:"type"`
	Grade      string    `json:"grade,omitempty"`
	Status     string    `json:"status"`
	// Our markup on the grower's price, in basis points.
	MarkupBPS  int32      `json:"markup_bps"`
	Source     string     `json:"source"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
	ImageCount int        `json:"image_count"`
	VideoCount int        `json:"video_count"`
	SizeCodes  []SizeCode `json:"size_codes"`
}

// SizeCode is one grade and its packs.
type SizeCode struct {
	SizeCodeID      uuid.UUID `json:"size_code_id"`
	Code            string    `json:"code"`
	Meta            string    `json:"meta,omitempty"`
	HarvestSharePct *int16    `json:"harvest_share_pct"`
	IsActive        bool      `json:"is_active"`
	Packs           []Pack    `json:"packs"`
}

// Pack is one pack option, priced.
type Pack struct {
	PackOptionID uuid.UUID `json:"pack_option_id"`
	Label        string    `json:"label"`
	WeightGrams  int32     `json:"weight_grams"`
	// What the grower charges.
	PricePaise int64 `json:"price_paise"`
	// What the customer pays: the grower's price plus our markup.
	CustomerPricePaise int64 `json:"customer_price_paise"`
	IsActive           bool  `json:"is_active"`
}

// Emit writes one product event in the caller's transaction.
//
// `q` must be bound to that transaction: the product is re-read through it, so
// the payload is the state being committed, and the event rolls back with the
// change it describes.
func Emit(
	ctx context.Context, q store.Querier, productID uuid.UUID,
	eventType, actor, source string, occurredAt time.Time,
) error {
	snapshot, err := Build(ctx, q, productID, source)
	if err != nil {
		return fmt.Errorf("building %s event: %w", eventType, err)
	}
	payload, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	var requestID *string
	if id := logging.RequestIDFrom(ctx); id != "" {
		requestID = &id
	}
	if _, err := q.InsertProductEvent(ctx, store.InsertProductEventParams{
		AggregateID:   productID,
		EventType:     eventType,
		SchemaVersion: SchemaVersion,
		OccurredAt:    occurredAt,
		ActorRole:     actor,
		RequestID:     requestID,
		Payload:       payload,
	}); err != nil {
		return fmt.Errorf("writing %s event: %w", eventType, err)
	}
	return nil
}

// Build assembles the analytics view of a product.
func Build(ctx context.Context, q store.Querier, productID uuid.UUID, source string) (Product, error) {
	product, err := q.GetProduct(ctx, productID)
	if err != nil {
		return Product{}, err
	}
	codes, err := q.ListSizeCodesForProduct(ctx, productID)
	if err != nil {
		return Product{}, err
	}
	packs, err := q.ListPackOptionsForProduct(ctx, productID)
	if err != nil {
		return Product{}, err
	}
	media, err := q.ListMediaForProducts(ctx, []uuid.UUID{productID})
	if err != nil {
		return Product{}, err
	}

	out := Product{
		ProductID:  product.ID,
		SupplierID: product.SupplierID,
		Name:       product.Name,
		Type:       product.Type,
		Status:     product.Status,
		MarkupBPS:  product.MarkupBps,
		Source:     source,
		CreatedAt:  product.CreatedAt.UTC(),
		UpdatedAt:  product.UpdatedAt.UTC(),
		SizeCodes:  make([]SizeCode, 0, len(codes)),
	}
	if product.Grade != nil {
		out.Grade = *product.Grade
	}
	for _, m := range media {
		switch m.Kind {
		case "image":
			out.ImageCount++
		case "video":
			out.VideoCount++
		}
	}

	bySize := make(map[uuid.UUID][]Pack, len(codes))
	for _, p := range packs {
		bySize[p.SizeCodeID] = append(bySize[p.SizeCodeID], Pack{
			PackOptionID:       p.ID,
			Label:              p.Label,
			WeightGrams:        p.WeightGrams,
			PricePaise:         p.PricePaise,
			CustomerPricePaise: pricing.CustomerPaise(p.PricePaise, product.MarkupBps),
			IsActive:           p.IsActive,
		})
	}
	for _, c := range codes {
		code := SizeCode{
			SizeCodeID:      c.ID,
			Code:            c.Code,
			HarvestSharePct: c.HarvestSharePct,
			IsActive:        c.IsActive,
			Packs:           bySize[c.ID],
		}
		if c.Meta != nil {
			code.Meta = *c.Meta
		}
		if code.Packs == nil {
			code.Packs = []Pack{}
		}
		out.SizeCodes = append(out.SizeCodes, code)
	}
	return out, nil
}
