// Package catalogclient talks to vm-catalog-api.
//
// Product prices, availability and stock all live in the catalog schema, which
// this service may not read (CLAUDE.md §3). Everything it needs about produce
// comes through here.
package catalogclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/vayal-mikrogreenz/vm-go-common/httpx"
	"github.com/vayal-mikrogreenz/vm-go-common/logging"
	"github.com/vayal-mikrogreenz/vm-go-common/money"
)

const requestTimeout = 5 * time.Second

// Client calls vm-catalog-api.
type Client struct {
	baseURL       string
	internalToken string
	http          *http.Client
}

// New builds a client.
func New(baseURL, internalToken string) *Client {
	return &Client{
		baseURL:       baseURL,
		internalToken: internalToken,
		http:          &http.Client{Timeout: requestTimeout},
	}
}

// Unit is a purchasable pack as the catalogue currently prices it.
//
// TWO prices, and confusing them is how a grower gets paid our margin:
//
//	PricePaise   what the CUSTOMER pays — this is what a cart is priced at
//	MarkupPaise  the part of that price which is ours, flat per pack
//
// The supplier's own share is PricePaise - MarkupPaise. Both come from one
// row in one call, so they cannot drift apart between requests.
type Unit struct {
	ID         uuid.UUID
	ProductID  uuid.UUID
	SupplierID uuid.UUID
	// SizeCodeID is the GRADE this pack belongs to, and the key its stock is
	// held against. SizeCode and SizeMeta travel with it so an order line can
	// snapshot what grade it was — the orders schema cannot look either up
	// later (CLAUDE.md §3).
	SizeCodeID  uuid.UUID
	SizeCode    string
	SizeMeta    string
	ProductName string
	// Type is the produce category — fruit, vegetable, microgreen, other.
	Type        string
	Grade       string
	Label       string
	WeightGrams int32
	// PricePaise is the CUSTOMER price: supplier price + markup.
	PricePaise money.Paise
	// MarkupPaise is ours, already included in PricePaise. It is the amount
	// MarkupBPS came to on THIS pack — a percentage of the grower's price, so
	// it differs between pack sizes of the same product.
	MarkupPaise money.Paise
	// MarkupBPS is the rate that produced it, carried for the record.
	MarkupBPS int32
	// Purchasable is false when the pack is larger than today's remaining stock.
	Purchasable bool
	// Available is false when the product has no open availability today.
	Available bool
}

// SupplierPaise is what the grower is owed for one pack.
func (u Unit) SupplierPaise() money.Paise {
	supplier := u.PricePaise - u.MarkupPaise
	if supplier < 0 {
		return 0
	}
	return supplier
}

type catalogUnitDTO struct {
	ID          string `json:"id"`
	SizeCodeID  string `json:"size_code_id"`
	SupplierID  string `json:"supplier_id"`
	Label       string `json:"label"`
	WeightGrams int32  `json:"weight_grams"`
	// The CUSTOMER price, the part of it that is ours, and the rate behind it.
	PricePaise  int64 `json:"price_paise"`
	MarkupPaise int64 `json:"markup_paise"`
	MarkupBPS   int32 `json:"markup_bps"`
	Purchasable bool  `json:"purchasable"`
}

// catalogGradeDTO is one GRADE of a product: the stock pool and the packs that
// draw on it. One entry per (product, size code).
type catalogGradeDTO struct {
	ID             string           `json:"product_id"`
	Name           string           `json:"product_name"`
	SizeCodeID     string           `json:"size_code_id"`
	SizeCode       string           `json:"size_code"`
	SizeMeta       *string          `json:"size_meta"`
	Type           string           `json:"product_type"`
	Grade          *string          `json:"grade"`
	RemainingGrams int32            `json:"remaining_grams"`
	Units          []catalogUnitDTO `json:"units"`
}

type catalogResponse struct {
	Grades []catalogGradeDTO `json:"grades"`
}

// Today is everything needed to price and validate a cart for today: every
// purchasable pack keyed by unit id, and the grams left per product.
type Today struct {
	Units map[uuid.UUID]Unit
	// RemainingGrams is per SIZE CODE, because that is how availability works
	// — every pack size of ONE GRADE competes for one pool, and the grades do
	// not share (CLAUDE.md §5.2). Keyed by size code id, never product id: a
	// product-level total would let a cart of XL draw on the M crate.
	RemainingGrams map[uuid.UUID]int32
}

// TodaysCatalogue reads the internal units endpoint.
//
// One call for the whole cart rather than one per line: a cart of eight items
// would otherwise make eight round trips on every read, and the cart is
// re-priced on EVERY read (CLAUDE.md §5.3).
//
// It deliberately does NOT read the customer-facing /catalog, which it used
// to. That endpoint returns one price — the marked-up one — and must never
// publish the markup beside it, because publishing the markup publishes the
// grower's price. Reading both prices and the stock from one internal row is
// also what stops them drifting between two calls and mis-splitting an order.
func (c *Client) TodaysCatalogue(ctx context.Context) (Today, error) {
	var body catalogResponse
	if err := c.get(ctx, "/internal/units/today", &body); err != nil {
		return Today{}, err
	}

	out := Today{
		Units:          map[uuid.UUID]Unit{},
		RemainingGrams: map[uuid.UUID]int32{},
	}
	for _, grade := range body.Grades {
		productID, err := uuid.Parse(grade.ID)
		if err != nil {
			continue
		}
		sizeCodeID, err := uuid.Parse(grade.SizeCodeID)
		if err != nil {
			continue
		}
		out.RemainingGrams[sizeCodeID] = grade.RemainingGrams

		for _, unit := range grade.Units {
			unitID, err := uuid.Parse(unit.ID)
			if err != nil {
				continue
			}
			supplierID, err := uuid.Parse(unit.SupplierID)
			if err != nil {
				continue
			}
			productGrade := ""
			if grade.Grade != nil {
				productGrade = *grade.Grade
			}
			sizeMeta := ""
			if grade.SizeMeta != nil {
				sizeMeta = *grade.SizeMeta
			}
			out.Units[unitID] = Unit{
				ID:          unitID,
				ProductID:   productID,
				SupplierID:  supplierID,
				SizeCodeID:  sizeCodeID,
				SizeCode:    grade.SizeCode,
				SizeMeta:    sizeMeta,
				ProductName: grade.Name,
				// Type and Grade are carried so the order line can snapshot
				// them. Grade was declared on this struct from the start but
				// never filled in — every order placed before this read a
				// blank grade onto its lines, and no report could recover it,
				// because the grade lives in the catalog schema this service
				// may not read (CLAUDE.md §3).
				Type:        grade.Type,
				Grade:       productGrade,
				Label:       unit.Label,
				WeightGrams: unit.WeightGrams,
				PricePaise:  money.Paise(unit.PricePaise),
				MarkupPaise: money.Paise(unit.MarkupPaise),
				MarkupBPS:   unit.MarkupBPS,
				Purchasable: unit.Purchasable,
				Available:   true,
			}
		}
	}
	return out, nil
}

// ReserveLine is one GRADE's gram requirement — the size code owns the pool.
type ReserveLine struct {
	SizeCodeID uuid.UUID `json:"size_code_id"`
	Grams      int32     `json:"grams"`
}

// Hold is one granted reservation.
type Hold struct {
	HoldID    uuid.UUID
	ProductID uuid.UUID
	Grams     int32
	ExpiresAt time.Time
}

type reserveRequestDTO struct {
	OrderRef   string        `json:"order_ref"`
	TTLSeconds int           `json:"ttl_seconds"`
	Lines      []ReserveLine `json:"lines"`
}

type holdDTO struct {
	HoldID    string `json:"hold_id"`
	ProductID string `json:"product_id"`
	Grams     int32  `json:"grams"`
	ExpiresAt string `json:"expires_at"`
}

type reserveResponseDTO struct {
	Holds []holdDTO `json:"holds"`
}

// Reserve holds stock for an order — CLAUDE.md §6.3 step 1.
//
// All or nothing: catalog either grants every line or none, so a customer is
// never charged for an order only partly secured.
func (c *Client) Reserve(
	ctx context.Context, orderRef uuid.UUID, ttl time.Duration, lines []ReserveLine,
) ([]Hold, error) {
	var body reserveResponseDTO
	err := c.post(ctx, "/internal/reservations", reserveRequestDTO{
		OrderRef:   orderRef.String(),
		TTLSeconds: int(ttl.Seconds()),
		Lines:      lines,
	}, &body)
	if err != nil {
		return nil, err
	}

	holds := make([]Hold, 0, len(body.Holds))
	for _, h := range body.Holds {
		holdID, err := uuid.Parse(h.HoldID)
		if err != nil {
			return nil, fmt.Errorf("catalogclient: bad hold id %q", h.HoldID)
		}
		productID, err := uuid.Parse(h.ProductID)
		if err != nil {
			return nil, fmt.Errorf("catalogclient: bad product id %q", h.ProductID)
		}
		expiresAt, err := time.Parse(time.RFC3339, h.ExpiresAt)
		if err != nil {
			return nil, fmt.Errorf("catalogclient: bad expiry %q", h.ExpiresAt)
		}
		holds = append(holds, Hold{
			HoldID: holdID, ProductID: productID, Grams: h.Grams, ExpiresAt: expiresAt,
		})
	}
	return holds, nil
}

type productImageDTO struct {
	ProductID string `json:"product_id"`
	ImageURL  string `json:"image_url"`
}

type productImagesResponseDTO struct {
	Products []productImageDTO `json:"products"`
}

// ProductImages returns a presigned image URL per product, for the products
// that have one.
//
// Orders carry a product id but no photograph: an order line snapshots the
// name, price and pack (CLAUDE.md §5.3), while the image is an object key in
// the catalog schema whose URL is minted at read time. A product with no photo,
// or one since deleted, is absent from the result and the caller falls back to
// a placeholder.
//
// The URLs are short-lived by design, so they are fetched per response and
// never cached across one.
func (c *Client) ProductImages(
	ctx context.Context, productIDs []uuid.UUID,
) (map[uuid.UUID]string, error) {
	if len(productIDs) == 0 {
		return map[uuid.UUID]string{}, nil
	}

	ids := make([]string, 0, len(productIDs))
	for _, id := range productIDs {
		ids = append(ids, id.String())
	}

	var body productImagesResponseDTO
	if err := c.post(ctx, "/internal/products/images",
		map[string]any{"product_ids": ids}, &body); err != nil {
		return nil, err
	}

	out := make(map[uuid.UUID]string, len(body.Products))
	for _, line := range body.Products {
		productID, err := uuid.Parse(line.ProductID)
		if err != nil {
			return nil, fmt.Errorf("catalogclient: bad product id %q", line.ProductID)
		}
		out[productID] = line.ImageURL
	}
	return out, nil
}

// Settle commits or releases holds — §6.3 steps 2 and 3. Idempotent.
func (c *Client) Settle(ctx context.Context, outcome string, holdIDs []uuid.UUID) error {
	ids := make([]string, 0, len(holdIDs))
	for _, id := range holdIDs {
		ids = append(ids, id.String())
	}
	return c.post(ctx, "/internal/reservations/settle", map[string]any{
		"outcome": outcome, "hold_ids": ids,
	}, nil)
}

func (c *Client) get(ctx context.Context, path string, out any) error {
	return c.do(ctx, http.MethodGet, path, nil, out)
}

func (c *Client) post(ctx context.Context, path string, in, out any) error {
	return c.do(ctx, http.MethodPost, path, in, out)
}

func (c *Client) do(ctx context.Context, method, path string, in, out any) error {
	var body *bytes.Reader
	if in != nil {
		encoded, err := json.Marshal(in)
		if err != nil {
			return fmt.Errorf("catalogclient: encoding request: %w", err)
		}
		body = bytes.NewReader(encoded)
	} else {
		body = bytes.NewReader(nil)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return fmt.Errorf("catalogclient: building request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(httpx.InternalTokenHeader, c.internalToken)
	if id := logging.RequestIDFrom(ctx); id != "" {
		req.Header.Set(logging.RequestIDHeader, id)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("catalogclient: calling vm-catalog-api: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		// Catalog speaks the same error envelope, so a stock conflict is
		// relayed to the customer intact rather than flattened to a 500.
		var envelope struct {
			Error struct {
				Code    string         `json:"code"`
				Message string         `json:"message"`
				Details map[string]any `json:"details"`
			} `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&envelope)

		if resp.StatusCode == http.StatusConflict {
			return httpx.Conflict(envelope.Error.Code, envelope.Error.Message).
				WithDetails(envelope.Error.Details)
		}
		return fmt.Errorf("catalogclient: vm-catalog-api returned %d: %s",
			resp.StatusCode, envelope.Error.Message)
	}

	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return fmt.Errorf("catalogclient: decoding response: %w", err)
		}
	}
	return nil
}

// Check returns a readiness probe confirming vm-catalog-api is reachable.
func (c *Client) Check() func(ctx context.Context) error {
	return func(ctx context.Context) error {
		var body catalogResponse
		return c.get(ctx, "/catalog?limit=1", &body)
	}
}
