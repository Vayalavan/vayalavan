// Package api implements vm-catalog-api's HTTP handlers.
//
// Ownership rule: every supplier-facing query takes supplier_id from the
// VERIFIED identity (never from the request body or path) and filters on it in
// SQL. A supplier therefore cannot read or mutate another supplier's row even
// if a handler forgets a check — the wrong row is never fetched.
package api

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vayal-mikrogreenz/vm-go-common/httpx"
	"github.com/vayal-mikrogreenz/vm-go-common/money"

	"github.com/vayal-mikrogreenz/vm-catalog-api/internal/csvimport"
	"github.com/vayal-mikrogreenz/vm-catalog-api/internal/storage"
	"github.com/vayal-mikrogreenz/vm-catalog-api/internal/store"
	"github.com/vayal-mikrogreenz/vm-catalog-api/internal/suppliers"
)

// Product lifecycle states (mirrors the CHECK constraint).
const (
	statusDraft    = "draft"
	statusActive   = "active"
	statusArchived = "archived"
)

// defaultSizeCode is the implicit grade every pre-grade listing was migrated
// under, and the one the CSV importer assigns when a row names none. It is a
// placeholder, not a grade the grower chose, so it is never shown to anyone.
const defaultSizeCode = "STD"

// Media kinds (mirrors the CHECK constraint on product_media.kind).
const (
	mediaKindImage = "image"
	mediaKindVideo = "video"
)

// productTypes mirrors the CHECK constraint on products.type.
var productTypes = map[string]struct{}{
	"fruit": {}, "vegetable": {}, "microgreen": {}, "other": {},
}

// maxPacksPerSizeCode bounds one grade of a single payload. A supplier
// legitimately has a handful of pack sizes per grade; hundreds means a runaway
// client, and each one is a row we delete and re-insert on every save.
const maxPacksPerSizeCode = 20

// API carries the dependencies every handler needs.
type API struct {
	pool    *pgxpool.Pool
	queries *store.Queries
	storage *storage.Client
	// suppliers resolves which suppliers may appear on the storefront. Owned
	// by vm-profile-api, so it is an HTTP call, not a join (CLAUDE.md §3).
	suppliers *suppliers.Client
	limits    Limits
	rates     Rates
	logger    *slog.Logger
}

// Limits caps the untrusted-input paths into this service.
type Limits struct {
	MaxImageBytes          int64
	MaxVideoBytes          int64
	MaxMediaPerSizeCode    int
	MaxSizeCodesPerProduct int
	MaxCSVBytes            int64
	MaxCSVRows             int
}

// Rates is the commercial configuration this service applies.
type Rates struct {
	// DefaultMarkupBPS is what a NEW product starts on, in basis points.
	// Existing products keep whatever an admin set.
	DefaultMarkupBPS int32
}

// New builds the API.
func New(
	pool *pgxpool.Pool,
	store *storage.Client,
	supplierClient *suppliers.Client,
	limits Limits,
	rates Rates,
	logger *slog.Logger,
) *API {
	return &API{
		pool:      pool,
		queries:   newQueries(pool),
		storage:   store,
		suppliers: supplierClient,
		limits:    limits,
		rates:     rates,
		logger:    logger,
	}
}

func newQueries(pool *pgxpool.Pool) *store.Queries { return store.New(pool) }

func (a *API) respond(ctx context.Context, w http.ResponseWriter, status int, body any) {
	httpx.WriteJSON(ctx, w, a.logger, status, body)
}

func (a *API) fail(ctx context.Context, w http.ResponseWriter, err error) {
	httpx.WriteError(ctx, w, a.logger, err)
}

// supplierFromRequest returns the caller's own supplier id.
//
// Taken from the verified token, never from the request, so there is no path
// by which a supplier can act as another.
func supplierFromRequest(r *http.Request) (uuid.UUID, error) {
	actor, err := httpx.RequireActor(r.Context())
	if err != nil {
		return uuid.Nil, err
	}
	return actor.RequireSupplierID()
}

// ---------------------------------------------------------------------------
// Wire types
// ---------------------------------------------------------------------------

type unitPayload struct {
	Label string `json:"label"`
	// Optional customer-facing detail for this pack — "6-8 fruit",
	// "ventilated carton". The label is a chip in a selector and has to stay
	// short; this is where the rest goes.
	Meta        string `json:"meta"`
	WeightGrams int32  `json:"weight_grams"`
	// Accepted as a rupee STRING, not a float. A JSON number would be parsed
	// as float64 and 45.50 is not exactly representable — CLAUDE.md rule 1.
	PriceRupees string `json:"price_rupees"`
	IsActive    *bool  `json:"is_active"`
}

// mediaPayload is one item of a product's gallery as the supplier submits it.
//
// There is no sort_order field: position in the array IS the order. The
// supplier arranges the gallery by dragging, and an explicit index the client
// has to keep consistent with the array is a second thing to get wrong.
type mediaPayload struct {
	Kind      string `json:"kind"`
	ObjectKey string `json:"object_key"`
	// ContentType is what the upload was presigned as. Optional: a row
	// carried over from before the gallery existed has none.
	ContentType string `json:"content_type"`
}

// sizeCodePayload is one grade of a product as the supplier submits it: the
// code, its optional meta, its own gallery, and the packs it sells in.
//
// There is no sort_order field, for the same reason mediaPayload has none:
// position in the array IS the order.
type sizeCodePayload struct {
	Code     string `json:"code"`
	Meta     string `json:"meta"`
	IsActive *bool  `json:"is_active"`
	// HarvestSharePct is roughly what percent of this product's harvest comes
	// off as this grade — 55 for the M that is most of the field, 3 for the
	// XL2 that is a handful of fruit a tree.
	//
	// A POINTER because absent and zero are different answers: nil is "the
	// grower has not estimated their split", which is the normal starting
	// state and renders no rarity treatment at all. Zero is not a valid share
	// and is rejected.
	HarvestSharePct *int           `json:"harvest_share_pct"`
	Media           []mediaPayload `json:"media"`
	Packs           []unitPayload  `json:"packs"`
}

type productPayload struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Grade       string `json:"grade"`
	Description string `json:"description"`
	Status      string `json:"status"`
	// Media that belongs to the whole product — the field, the packing shed —
	// shown to the customer after every grade's own pictures. Position in the
	// array is the order, exactly as inside a grade.
	Media     []mediaPayload    `json:"media"`
	SizeCodes []sizeCodePayload `json:"size_codes"`
}

type unitResponse struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	// Optional detail, shown under the label wherever a pack is chosen.
	Meta        *string `json:"meta"`
	WeightGrams int32   `json:"weight_grams"`
	PricePaise  int64   `json:"price_paise"`
	// Formatted at the edge for display; never parsed back into logic.
	PriceDisplay string `json:"price_display"`
	IsActive     bool   `json:"is_active"`
	SortOrder    int32  `json:"sort_order"`
}

// mediaResponse is one gallery item. Both the key and a presigned URL are
// returned: the URL is what a client renders, the key is what it sends back
// on the next save.
type mediaResponse struct {
	ID          string  `json:"id"`
	Kind        string  `json:"kind"`
	ObjectKey   string  `json:"object_key"`
	URL         *string `json:"url"`
	ContentType *string `json:"content_type"`
	SortOrder   int32   `json:"sort_order"`
}

// sizeCodeResponse is one grade with everything hanging off it.
type sizeCodeResponse struct {
	ID        string  `json:"id"`
	Code      string  `json:"code"`
	Meta      *string `json:"meta"`
	IsActive  bool    `json:"is_active"`
	SortOrder int32   `json:"sort_order"`
	// Null when the grower has not estimated their split. Display only —
	// nothing in pricing, stock or fulfilment reads it.
	HarvestSharePct *int16 `json:"harvest_share_pct"`
	// ImageURL is THIS grade's cover — the first image of its gallery.
	ImageURL *string `json:"image_url"`
	// Media is this grade's whole gallery, images and videos interleaved as
	// the supplier arranged them. Switching grade on the storefront swaps it.
	Media []mediaResponse `json:"media"`
	Packs []unitResponse  `json:"packs"`
}

type productResponse struct {
	ID          string  `json:"id"`
	SupplierID  string  `json:"supplier_id"`
	Name        string  `json:"name"`
	Type        string  `json:"type"`
	Grade       *string `json:"grade"`
	Description *string `json:"description"`
	// ImageURL is the DEFAULT grade's cover — the first image of the first
	// size code's gallery — as a short-lived presigned GET generated at read
	// time, so changing bucket or host never breaks a stored row
	// (CLAUDE.md §5.2).
	//
	// Kept alongside SizeCodes so every surface that wants one thumbnail
	// (order thumbnails, the admin table) needs to know nothing about grades
	// or galleries. Null when no grade of the product has an image, which
	// includes one carrying only video.
	ImageURL *string `json:"image_url"`
	// Media is the product-level gallery — the pictures that are of the farm
	// rather than of a grade. Returned SEPARATELY from the grades, not folded
	// into each one: this is the supplier's own response, and the form edits
	// the two as the two things they are. The customer-facing catalogue is
	// where they are concatenated.
	Media []mediaResponse `json:"media"`
	// SizeCodes is the whole tree: each grade with its gallery and its packs.
	SizeCodes []sizeCodeResponse `json:"size_codes"`
	Status    string             `json:"status"`
	CreatedAt string             `json:"created_at"`
	UpdatedAt string             `json:"updated_at"`
}

func toUnitResponse(u store.ProductPackOption) unitResponse {
	return unitResponse{
		ID:           u.ID.String(),
		Label:        u.Label,
		Meta:         u.Meta,
		WeightGrams:  u.WeightGrams,
		PricePaise:   u.PricePaise,
		PriceDisplay: money.FormatRupees(money.Paise(u.PricePaise)),
		IsActive:     u.IsActive,
		SortOrder:    u.SortOrder,
	}
}

// presignMedia mints a presigned GET for one object key, returning nil rather
// than failing the whole response if it cannot: a listing that renders without
// one picture is better than a page that will not load.
//
// Presigning is a local HMAC computation, not a network call, so doing it per
// row on a list page is cheap.
func (a *API) presignMedia(ctx context.Context, productID uuid.UUID, key string) *string {
	if strings.TrimSpace(key) == "" {
		return nil
	}
	signed, err := a.storage.PresignGet(ctx, key)
	if err != nil {
		a.logger.WarnContext(ctx, "could not presign product media",
			slog.String("product_id", productID.String()),
			slog.String("object_key", key),
			slog.Any("error", err))
		return nil
	}
	return &signed
}

// coverImageURL presigns a gallery's cover — its FIRST image. Nil when there
// is none, which includes a product carrying only video: every list surface
// needs a still it can paint immediately, so a video is never a cover.
func (a *API) coverImageURL(
	ctx context.Context, productID uuid.UUID, media []store.ProductMedium,
) *string {
	for _, m := range media {
		if m.Kind == mediaKindImage {
			return a.presignMedia(ctx, productID, m.ObjectKey)
		}
	}
	return nil
}

// toMediaResponses presigns a whole gallery for the surfaces that show one.
func (a *API) toMediaResponses(
	ctx context.Context, productID uuid.UUID, media []store.ProductMedium,
) []mediaResponse {
	out := make([]mediaResponse, 0, len(media))
	for _, m := range media {
		out = append(out, mediaResponse{
			ID:          m.ID.String(),
			Kind:        m.Kind,
			ObjectKey:   m.ObjectKey,
			URL:         a.presignMedia(ctx, productID, m.ObjectKey),
			ContentType: m.ContentType,
			SortOrder:   m.SortOrder,
		})
	}
	return out
}

// productTree is a product with everything hanging off it, already loaded.
// Grouping happens once, in the bulk loaders, so a serialiser never queries.
type productTree struct {
	SizeCodes []store.ProductSizeCode
	// Packs and Media keyed by size code id.
	Packs map[uuid.UUID][]store.ProductPackOption
	Media map[uuid.UUID][]store.ProductMedium
	// CommonMedia belongs to the PRODUCT, not to any one grade — the field,
	// the packing shed, the grower. The customer sees it after each grade's
	// own pictures; the supplier edits it in one place instead of uploading
	// the same photograph once per crate.
	CommonMedia []store.ProductMedium
}

// gallery is what a CUSTOMER sees for one grade: that grade's own pictures,
// then the product's common ones.
//
// Concatenated here rather than in each client, so a phone and a browser
// cannot disagree about the order, and so the rule lives next to the tree it
// reads. Never mutates either slice — appending to tree.Media[id] in place
// would leak the common rows into the next grade.
func (t productTree) gallery(sizeCodeID uuid.UUID) []store.ProductMedium {
	own := t.Media[sizeCodeID]
	if len(t.CommonMedia) == 0 {
		return own
	}
	out := make([]store.ProductMedium, 0, len(own)+len(t.CommonMedia))
	out = append(out, own...)
	return append(out, t.CommonMedia...)
}

// toProductResponse serialises a product and its whole grade tree, minting a
// presigned URL per media row and picking each grade's cover out of them.
func (a *API) toProductResponse(
	ctx context.Context, p store.Product, tree productTree,
) productResponse {
	resp := productResponse{
		ID:          p.ID.String(),
		SupplierID:  p.SupplierID.String(),
		Name:        p.Name,
		Type:        p.Type,
		Grade:       p.Grade,
		Description: p.Description,
		Status:      p.Status,
		Media:       a.toMediaResponses(ctx, p.ID, tree.CommonMedia),
		SizeCodes:   make([]sizeCodeResponse, 0, len(tree.SizeCodes)),
		CreatedAt:   p.CreatedAt.Format(timeFormat),
		UpdatedAt:   p.UpdatedAt.Format(timeFormat),
	}

	for _, sc := range tree.SizeCodes {
		out := sizeCodeResponse{
			ID:              sc.ID.String(),
			Code:            sc.Code,
			Meta:            sc.Meta,
			IsActive:        sc.IsActive,
			SortOrder:       sc.SortOrder,
			HarvestSharePct: sc.HarvestSharePct,
			Media:           make([]mediaResponse, 0, len(tree.Media[sc.ID])),
			Packs:           make([]unitResponse, 0, len(tree.Packs[sc.ID])),
		}

		for _, m := range tree.Media[sc.ID] {
			url := a.presignMedia(ctx, p.ID, m.ObjectKey)
			out.Media = append(out.Media, mediaResponse{
				ID:          m.ID.String(),
				Kind:        m.Kind,
				ObjectKey:   m.ObjectKey,
				URL:         url,
				ContentType: m.ContentType,
				SortOrder:   m.SortOrder,
			})
			// The cover is the FIRST image of THIS grade, and media arrives
			// already ordered, so the first one seen wins. Videos are skipped:
			// a list surface needs a still it can paint without decoding
			// anything.
			if out.ImageURL == nil && m.Kind == mediaKindImage {
				out.ImageURL = url
			}
		}

		for _, pack := range tree.Packs[sc.ID] {
			out.Packs = append(out.Packs, toUnitResponse(pack))
		}

		// The product's own thumbnail is the first grade that HAS one, not
		// strictly the first grade: a grower who photographed only their XL
		// should still get a picture on the order history, not a placeholder.
		if resp.ImageURL == nil {
			resp.ImageURL = out.ImageURL
		}
		resp.SizeCodes = append(resp.SizeCodes, out)
	}

	// Failing that, the common gallery: a grower who photographed the field
	// but not the crates still has a thumbnail rather than a placeholder.
	if resp.ImageURL == nil {
		resp.ImageURL = a.coverImageURL(ctx, p.ID, tree.CommonMedia)
	}
	return resp
}

const timeFormat = "2006-01-02T15:04:05Z07:00"

// ---------------------------------------------------------------------------
// Validation
// ---------------------------------------------------------------------------

type validation struct{ fields map[string]any }

func newValidation() *validation { return &validation{fields: map[string]any{}} }

func (v *validation) add(field string, message any) { v.fields[field] = message }

func (v *validation) err() error {
	if len(v.fields) == 0 {
		return nil
	}
	return httpx.Validation("Some fields need attention.", v.fields)
}

// parsedUnit is a validated unit ready for insertion.
type parsedUnit struct {
	Label       string
	Meta        *string
	WeightGrams int32
	PricePaise  int64
	IsActive    bool
	SortOrder   int32
}

// parsedMedia is a validated gallery item ready for insertion.
type parsedMedia struct {
	Kind        string
	ObjectKey   string
	ContentType *string
	SortOrder   int32
}

// parsedSizeCode is a validated grade with its gallery and its packs.
type parsedSizeCode struct {
	Code            string
	Meta            *string
	IsActive        bool
	SortOrder       int32
	HarvestSharePct *int16
	Media           []parsedMedia
	Packs           []parsedUnit
}

// parsedProduct is a validated product payload ready for insertion. A struct
// rather than a run of return values: with a whole grade tree in it, the
// positional form had stopped being readable at the call site.
type parsedProduct struct {
	Name        string
	Type        string
	Grade       *string
	Description *string
	// The product-level gallery, shown after every grade's own.
	Media     []parsedMedia
	SizeCodes []parsedSizeCode
}

// productLimits are the configurable bounds validation applies to one payload.
type productLimits struct {
	MaxSizeCodes int
	MaxMedia     int
}

// validateProduct checks the whole payload: the product, every size code, and
// every gallery and pack set beneath them.
//
// The limits are passed in rather than read from package constants because
// they are env vars (MAX_SIZE_CODES_PER_PRODUCT, MAX_MEDIA_PER_SIZE_CODE) —
// CLAUDE.md rule 3.
func validateProduct(p productPayload, limits productLimits) (parsedProduct, error) {
	v := newValidation()
	var out parsedProduct

	name := strings.TrimSpace(p.Name)
	switch {
	case name == "":
		v.add("name", "is required")
	case len([]rune(name)) > 120:
		v.add("name", "must be at most 120 characters")
	}

	ptype := strings.ToLower(strings.TrimSpace(p.Type))
	if _, ok := productTypes[ptype]; !ok {
		v.add("type", "must be one of fruit, vegetable, microgreen, other")
	}

	out.Name = name
	out.Type = ptype
	out.Grade = optional(p.Grade, 40)
	out.Description = optional(p.Description, 2000)

	status := strings.ToLower(strings.TrimSpace(p.Status))
	if status != "" && status != statusDraft && status != statusActive && status != statusArchived {
		v.add("status", "must be draft, active or archived")
	}

	// The product-level gallery is held to the same cap as a grade's: it is
	// the same kind of bucket, and one of them holding four times as much
	// would be a rule nobody could predict from the form.
	commonValidation := newValidation()
	out.Media = validateMedia(p.Media, limits.MaxMedia, commonValidation)
	if commonValidation.err() != nil {
		v.add("media", commonValidation.fields["media"])
	}

	out.SizeCodes = validateSizeCodes(p.SizeCodes, limits, v)

	if err := v.err(); err != nil {
		return parsedProduct{}, err
	}
	return out, nil
}

// validateSizeCodes checks the grade tree, recording problems on v and
// returning the grades that parsed.
//
// At least one size code is required, always. A product that sells at one
// grade simply has one — a second code path for "flat" products would double
// every read, write and screen for a case the single-grade one already covers,
// and the storefront hides the selector when there is nothing to select.
func validateSizeCodes(
	items []sizeCodePayload, limits productLimits, v *validation,
) []parsedSizeCode {
	switch {
	case len(items) == 0:
		v.add("size_codes", "at least one size code is required")
		return nil
	case len(items) > limits.MaxSizeCodes:
		v.add("size_codes", fmt.Sprintf("at most %d size codes are allowed", limits.MaxSizeCodes))
		return nil
	}

	out := make([]parsedSizeCode, 0, len(items))
	problems := map[string]any{}
	// Would violate the unique index on (product_id, lower(btrim(code))) at
	// insert time; catching it here names the offending grade instead.
	seenCodes := map[string]int{}

	for i, item := range items {
		itemProblems := map[string]any{}

		code := strings.TrimSpace(item.Code)
		switch {
		case code == "":
			itemProblems["code"] = "is required"
		case len([]rune(code)) > 20:
			itemProblems["code"] = "must be at most 20 characters"
		default:
			key := strings.ToLower(code)
			if prior, dup := seenCodes[key]; dup {
				itemProblems["code"] = "duplicates size code " + strconv.Itoa(prior+1)
			} else {
				seenCodes[key] = i
			}
		}

		// The share of the harvest this grade is. Absent is the normal
		// state and stays absent; present has to be a share a harvest can
		// actually have.
		var share *int16
		if item.HarvestSharePct != nil {
			pct := *item.HarvestSharePct
			if pct < 1 || pct > 100 {
				itemProblems["harvest_share_pct"] = "must be between 1 and 100"
			} else {
				narrowed := int16(pct)
				share = &narrowed
			}
		}

		parsed := parsedSizeCode{
			Code:            code,
			Meta:            optional(item.Meta, 80),
			IsActive:        true,
			SortOrder:       int32(len(out)),
			HarvestSharePct: share,
		}
		if item.IsActive != nil {
			parsed.IsActive = *item.IsActive
		}

		// Each grade carries its OWN gallery and its OWN packs, so both are
		// validated per grade and reported under that grade's index.
		mediaValidation := newValidation()
		parsed.Media = validateMedia(item.Media, limits.MaxMedia, mediaValidation)
		if err := mediaValidation.err(); err != nil {
			itemProblems["media"] = mediaValidation.fields["media"]
		}

		packValidation := newValidation()
		parsed.Packs = validatePacks(item.Packs, packValidation)
		if err := packValidation.err(); err != nil {
			itemProblems["packs"] = packValidation.fields["packs"]
		}

		if len(itemProblems) > 0 {
			problems[strconv.Itoa(i)] = itemProblems
			continue
		}

		parsed.SortOrder = int32(len(out))
		out = append(out, parsed)
	}

	if len(problems) > 0 {
		v.add("size_codes", problems)
		return out
	}

	// A harvest split that adds up to more than the harvest is arithmetic
	// nobody can defend, and it would make every rarity badge on the product
	// wrong at once. UNDER 100 is fine and common: a grower may have estimated
	// only the grades worth calling out, and the rest is simply unlabelled.
	//
	// Checked only once every grade parsed, because a total computed from a
	// partial tree would report a second, invented problem on top of the real
	// one.
	total := 0
	for _, sc := range out {
		if sc.HarvestSharePct != nil {
			total += int(*sc.HarvestSharePct)
		}
	}
	if total > 100 {
		v.add("size_codes", fmt.Sprintf(
			"the harvest shares add up to %d%% — they cannot total more than 100%%", total))
		return nil
	}

	return out
}

// validatePacks checks one grade's pack options, recording problems on v.
func validatePacks(items []unitPayload, v *validation) []parsedUnit {
	switch {
	case len(items) == 0:
		// A grade with no packs cannot be bought, so it is never a meaningful
		// thing to save.
		v.add("packs", "at least one pack option is required")
		return nil
	case len(items) > maxPacksPerSizeCode:
		v.add("packs", fmt.Sprintf("at most %d pack options are allowed", maxPacksPerSizeCode))
		return nil
	}

	out := make([]parsedUnit, 0, len(items))
	problems := map[string]any{}
	seenWeights := map[int32]int{}

	for i, u := range items {
		itemProblems := map[string]string{}

		label := strings.TrimSpace(u.Label)
		switch {
		case label == "":
			itemProblems["label"] = "is required"
		case len([]rune(label)) > 40:
			itemProblems["label"] = "must be at most 40 characters"
		}

		switch {
		case u.WeightGrams <= 0:
			itemProblems["weight_grams"] = "must be greater than zero"
		case u.WeightGrams > 1_000_000:
			itemProblems["weight_grams"] = "must be at most 1000000 (1000 kg)"
		default:
			// Would violate UNIQUE (size_code_id, weight_grams) at insert
			// time; reporting it here names the offending row instead. The
			// same weight under a DIFFERENT grade is fine, and is the point of
			// size codes.
			if prior, dup := seenWeights[u.WeightGrams]; dup {
				itemProblems["weight_grams"] = "duplicates the weight on pack " + strconv.Itoa(prior+1)
			} else {
				seenWeights[u.WeightGrams] = i
			}
		}

		// The single rupee->paise conversion for the platform: integer only,
		// at most 2 decimals, never a float.
		paise, parseErr := money.ParseRupees(u.PriceRupees)
		switch {
		case strings.TrimSpace(u.PriceRupees) == "":
			itemProblems["price_rupees"] = "is required"
		case parseErr != nil:
			itemProblems["price_rupees"] = "must be an amount with at most 2 decimal places"
		case paise <= 0:
			itemProblems["price_rupees"] = "must be greater than zero"
		}

		if len(itemProblems) > 0 {
			problems[strconv.Itoa(i)] = itemProblems
			continue
		}

		isActive := true
		if u.IsActive != nil {
			isActive = *u.IsActive
		}
		out = append(out, parsedUnit{
			Label: label,
			// Truncated rather than rejected at 80, exactly as a size code's
			// detail is: the same field at two levels must not behave
			// differently depending on which one it is on.
			Meta:        optional(u.Meta, 80),
			WeightGrams: u.WeightGrams,
			PricePaise:  paise.Int64(),
			IsActive:    isActive,
			SortOrder:   int32(len(out)),
		})
	}

	if len(problems) > 0 {
		v.add("packs", problems)
	}
	return out
}

// validateMedia checks the gallery, recording problems on v and returning the
// rows that parsed. Order is the array's own; sort_order is assigned from it.
//
// An empty gallery is valid — a supplier may list produce before they have a
// photograph of it, exactly as they could when this was one nullable column.
func validateMedia(items []mediaPayload, maxMedia int, v *validation) []parsedMedia {
	if len(items) > maxMedia {
		v.add("media", fmt.Sprintf("at most %d images and videos are allowed", maxMedia))
		return nil
	}

	out := make([]parsedMedia, 0, len(items))
	problems := map[string]any{}
	// The same object attached twice would violate the unique index at insert
	// time; catching it here names the offending row instead.
	seen := map[string]int{}

	for i, m := range items {
		itemProblems := map[string]string{}

		kind := strings.ToLower(strings.TrimSpace(m.Kind))
		if kind != mediaKindImage && kind != mediaKindVideo {
			itemProblems["kind"] = "must be image or video"
		}

		key := strings.TrimSpace(m.ObjectKey)
		switch {
		case key == "":
			itemProblems["object_key"] = "is required"
		case len(key) > 400:
			itemProblems["object_key"] = "must be at most 400 characters"
		default:
			if prior, dup := seen[key]; dup {
				itemProblems["object_key"] = "duplicates item " + strconv.Itoa(prior+1)
			} else {
				seen[key] = i
			}
		}

		if len(itemProblems) > 0 {
			problems[strconv.Itoa(i)] = itemProblems
			continue
		}

		out = append(out, parsedMedia{
			Kind:        kind,
			ObjectKey:   key,
			ContentType: optional(m.ContentType, 100),
			SortOrder:   int32(len(out)),
		})
	}

	if len(problems) > 0 {
		v.add("media", problems)
	}
	return out
}

func optional(value string, max int) *string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	if len([]rune(trimmed)) > max {
		trimmed = string([]rune(trimmed)[:max])
	}
	return &trimmed
}

// ---------------------------------------------------------------------------
// Query helpers
// ---------------------------------------------------------------------------

type listFilters struct {
	Type   *string
	Status *string
	Search *string
	Limit  int32
	Offset int32
}

// parseListFilters reads the query string, rejecting values outside the
// allowed sets rather than silently ignoring them.
func parseListFilters(q url.Values) (listFilters, error) {
	f := listFilters{Limit: 25}

	if v := strings.TrimSpace(q.Get("type")); v != "" {
		if _, ok := productTypes[strings.ToLower(v)]; !ok {
			return f, httpx.BadRequest("type must be one of fruit, vegetable, microgreen, other.")
		}
		lowered := strings.ToLower(v)
		f.Type = &lowered
	}
	if v := strings.TrimSpace(q.Get("status")); v != "" {
		switch v {
		case statusDraft, statusActive, statusArchived:
			f.Status = &v
		default:
			return f, httpx.BadRequest("status must be draft, active or archived.")
		}
	}
	if v := strings.TrimSpace(q.Get("search")); v != "" {
		// Escape LIKE wildcards so a search for "100%" is a literal search
		// rather than a match-everything pattern.
		escaped := strings.NewReplacer("%", `\%`, "_", `\_`).Replace(v)
		f.Search = &escaped
	}
	if v, err := strconv.Atoi(q.Get("limit")); err == nil && v > 0 && v <= 100 {
		f.Limit = int32(v)
	}
	if v, err := strconv.Atoi(q.Get("offset")); err == nil && v >= 0 {
		f.Offset = int32(v)
	}
	return f, nil
}

// treesByProduct bulk-loads the whole grade tree for a page of products,
// avoiding N+1: three queries for any number of products, whatever the shape.
//
// Rows come back ordered by product then the grower's own grade order, so each
// group is already arranged and nothing here sorts.
func (a *API) treesByProduct(
	ctx context.Context, products []store.Product,
) (map[uuid.UUID]productTree, error) {
	trees := map[uuid.UUID]productTree{}
	if len(products) == 0 {
		return trees, nil
	}
	ids := make([]uuid.UUID, 0, len(products))
	for _, p := range products {
		ids = append(ids, p.ID)
		trees[p.ID] = productTree{
			Packs: map[uuid.UUID][]store.ProductPackOption{},
			Media: map[uuid.UUID][]store.ProductMedium{},
		}
	}

	sizeCodes, err := a.queries.ListSizeCodesForProducts(ctx, ids)
	if err != nil {
		return nil, err
	}
	for _, sc := range sizeCodes {
		tree := trees[sc.ProductID]
		tree.SizeCodes = append(tree.SizeCodes, sc)
		trees[sc.ProductID] = tree
	}

	packs, err := a.queries.ListPackOptionsForProducts(ctx, ids)
	if err != nil {
		return nil, err
	}
	for _, pack := range packs {
		if tree, ok := trees[pack.ProductID]; ok {
			tree.Packs[pack.SizeCodeID] = append(tree.Packs[pack.SizeCodeID], pack)
		}
	}

	media, err := a.queries.ListMediaForProducts(ctx, ids)
	if err != nil {
		return nil, err
	}
	for _, m := range media {
		tree, ok := trees[m.ProductID]
		if !ok {
			continue
		}
		// A NULL size code is the product's own bucket, not a grade's. The
		// query already sorts those last within the product, so appending
		// keeps the grower's order.
		if m.SizeCodeID == nil {
			tree.CommonMedia = append(tree.CommonMedia, m)
			trees[m.ProductID] = tree
			continue
		}
		tree.Media[*m.SizeCodeID] = append(tree.Media[*m.SizeCodeID], m)
	}

	return trees, nil
}

// treeForProduct loads one product's grade tree. The single-product form of
// treesByProduct, and deliberately built on it so the two cannot disagree
// about ordering or grouping.
func (a *API) treeForProduct(
	ctx context.Context, p store.Product,
) (productTree, error) {
	trees, err := a.treesByProduct(ctx, []store.Product{p})
	if err != nil {
		return productTree{}, err
	}
	return trees[p.ID], nil
}

// productLimits returns the configured bounds on one product payload.
func (a *API) productLimits() productLimits {
	return productLimits{
		MaxSizeCodes: a.limits.MaxSizeCodesPerProduct,
		MaxMedia:     a.limits.MaxMediaPerSizeCode,
	}
}

// csvLimits returns the configured CSV bounds.
func (a *API) csvLimits() csvimport.Limits {
	return csvimport.Limits{MaxBytes: a.limits.MaxCSVBytes, MaxRows: a.limits.MaxCSVRows}
}
