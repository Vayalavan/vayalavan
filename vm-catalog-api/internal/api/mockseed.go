package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/vayal-mikrogreenz/vm-catalog-api/internal/analyticsevents"
	"github.com/vayal-mikrogreenz/vm-catalog-api/internal/store"
)

// The development seed's catalogues: mocks/suppliers/<slug>/catalog.json,
// with the photos and videos it names alongside in media/.
//
// The supplier ROW is created by infra/seed/02_identity.sql (profile schema,
// which this service may not touch); this seeds what that supplier sells.
// It goes through the same functions a product save does — replaceSizeCodes,
// replaceCommonMedia, the analytics event — so a seeded product is exactly
// what a supplier would have created by hand.
//
// Idempotent. A product the supplier already has (same name and grade, not
// archived) is left alone, edits and all; only missing ones are created.

type mockCatalog struct {
	Supplier struct {
		ID           uuid.UUID `json:"id"`
		BusinessName string    `json:"business_name"`
	} `json:"supplier"`
	Products []mockProduct `json:"products"`
}

type mockProduct struct {
	Name        string         `json:"name"`
	Type        string         `json:"type"`
	Grade       *string        `json:"grade"`
	Description *string        `json:"description"`
	Status      string         `json:"status"`
	MarkupBPS   int32          `json:"markup_bps"`
	Media       []mockMedia    `json:"media"`
	SizeCodes   []mockSizeCode `json:"size_codes"`
}

type mockSizeCode struct {
	Code            string      `json:"code"`
	Meta            *string     `json:"meta"`
	IsActive        bool        `json:"is_active"`
	HarvestSharePct *int16      `json:"harvest_share_pct"`
	Media           []mockMedia `json:"media"`
	Packs           []mockPack  `json:"packs"`
}

type mockPack struct {
	Label       string  `json:"label"`
	Meta        *string `json:"meta"`
	WeightGrams int32   `json:"weight_grams"`
	PricePaise  int64   `json:"price_paise"`
	IsActive    bool    `json:"is_active"`
}

type mockMedia struct {
	File string `json:"file"`
	Kind string `json:"kind"`
}

// MockSeedResult counts what one run did.
type MockSeedResult struct {
	Created, Skipped, Files int
}

// mockContentTypes are the types the seed recognises — a subset of what a
// direct upload accepts (allowedMediaTypes).
var mockContentTypes = map[string]string{
	".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".webp": "image/webp",
	".mp4": "video/mp4", ".mov": "video/quicktime", ".webm": "video/webm",
}

// SeedMocks seeds every catalogue under dir (the repository's mocks/).
func (a *API) SeedMocks(ctx context.Context, dir string) (MockSeedResult, error) {
	var total MockSeedResult
	files, err := filepath.Glob(filepath.Join(dir, "suppliers", "*", "catalog.json"))
	if err != nil {
		return total, err
	}
	if len(files) == 0 {
		return total, fmt.Errorf("no catalogues found under %s/suppliers/*/catalog.json", dir)
	}
	for _, file := range files {
		res, err := a.seedCatalog(ctx, file)
		total.Created += res.Created
		total.Skipped += res.Skipped
		total.Files += res.Files
		if err != nil {
			return total, fmt.Errorf("%s: %w", file, err)
		}
	}
	return total, nil
}

func (a *API) seedCatalog(ctx context.Context, path string) (MockSeedResult, error) {
	var res MockSeedResult
	raw, err := os.ReadFile(path)
	if err != nil {
		return res, err
	}
	var cat mockCatalog
	if err := json.Unmarshal(raw, &cat); err != nil {
		return res, fmt.Errorf("parsing: %w", err)
	}
	if cat.Supplier.ID == uuid.Nil {
		return res, fmt.Errorf("supplier.id is required")
	}
	base := filepath.Dir(path)

	for _, p := range cat.Products {
		exists, err := a.queries.ProductExistsForSupplier(ctx, store.ProductExistsForSupplierParams{
			SupplierID: cat.Supplier.ID, Name: p.Name, Grade: p.Grade,
		})
		if err != nil {
			return res, err
		}
		if exists {
			res.Skipped++
			continue
		}
		uploaded, err := a.seedProduct(ctx, cat.Supplier.ID, base, p)
		res.Files += uploaded
		if err != nil {
			return res, fmt.Errorf("product %q: %w", p.Name, err)
		}
		res.Created++
		a.logger.InfoContext(ctx, "seeded product",
			slog.String("supplier", cat.Supplier.BusinessName), slog.String("product", p.Name))
	}
	return res, nil
}

// seedProduct uploads a product's media, then writes the product, its grade
// tree and its analytics event in one transaction.
func (a *API) seedProduct(ctx context.Context, supplierID uuid.UUID, base string, p mockProduct) (int, error) {
	files := 0
	// Deterministic keys (supplier + file name): re-running the seed rewrites
	// the same objects rather than piling up copies.
	upload := func(items []mockMedia) ([]parsedMedia, error) {
		out := make([]parsedMedia, 0, len(items))
		for i, m := range items {
			ext := strings.ToLower(filepath.Ext(m.File))
			contentType, ok := mockContentTypes[ext]
			if !ok {
				return nil, fmt.Errorf("%s: unsupported file type %q", m.File, ext)
			}
			body, err := os.ReadFile(filepath.Join(base, m.File))
			if err != nil {
				return nil, err
			}
			key := fmt.Sprintf("products/%s/%s", supplierID, filepath.Base(m.File))
			if err := a.storage.PutObject(ctx, key, contentType, body); err != nil {
				return nil, err
			}
			files++
			kind := m.Kind
			if kind == "" {
				kind = strings.SplitN(contentType, "/", 2)[0]
			}
			ct := contentType
			out = append(out, parsedMedia{Kind: kind, ObjectKey: key, ContentType: &ct, SortOrder: int32(i)})
		}
		return out, nil
	}

	common, err := upload(p.Media)
	if err != nil {
		return files, err
	}
	sizeCodes := make([]parsedSizeCode, 0, len(p.SizeCodes))
	for i, sc := range p.SizeCodes {
		media, err := upload(sc.Media)
		if err != nil {
			return files, err
		}
		packs := make([]parsedUnit, 0, len(sc.Packs))
		for j, pk := range sc.Packs {
			packs = append(packs, parsedUnit{
				Label: pk.Label, Meta: pk.Meta, WeightGrams: pk.WeightGrams,
				PricePaise: pk.PricePaise, IsActive: pk.IsActive, SortOrder: int32(j),
			})
		}
		sizeCodes = append(sizeCodes, parsedSizeCode{
			Code: sc.Code, Meta: sc.Meta, IsActive: sc.IsActive, SortOrder: int32(i),
			HarvestSharePct: sc.HarvestSharePct, Media: media, Packs: packs,
		})
	}

	status := p.Status
	if status == "" {
		status = statusActive
	}

	tx, err := a.pool.Begin(ctx)
	if err != nil {
		return files, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit
	q := a.queries.WithTx(tx)

	product, err := q.CreateProduct(ctx, store.CreateProductParams{
		SupplierID: supplierID, Name: p.Name, Type: p.Type, Grade: p.Grade,
		Description: p.Description, Status: status, MarkupBps: p.MarkupBPS,
	})
	if err != nil {
		return files, err
	}
	if _, err := a.replaceSizeCodes(ctx, q, product.ID, sizeCodes); err != nil {
		return files, err
	}
	if _, err := a.replaceCommonMedia(ctx, q, product.ID, common); err != nil {
		return files, err
	}
	if err := analyticsevents.Emit(ctx, q, product.ID, analyticsevents.ProductCreated,
		analyticsevents.ActorSystem, analyticsevents.SourceSeed, time.Now()); err != nil {
		return files, err
	}
	return files, tx.Commit(ctx)
}
