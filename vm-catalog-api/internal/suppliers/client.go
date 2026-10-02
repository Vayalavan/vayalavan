// Package suppliers resolves which suppliers are allowed to sell.
//
// Supplier approval lives in vm-profile-api's schema, and CLAUDE.md §3 forbids
// this service reading it directly, so the answer comes over HTTP and is
// cached briefly.
package suppliers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/vayal-mikrogreenz/vm-go-common/httpx"
	"github.com/vayal-mikrogreenz/vm-go-common/logging"
)

// cacheTTL bounds how stale the allow-list may be.
//
// Short, because it is the delay between an admin suspending a supplier and
// their produce leaving the storefront. Not zero, because the storefront is
// the hottest read path on the platform and every page load would otherwise
// make a second service call.
const cacheTTL = 30 * time.Second

// requestTimeout keeps a slow profile service from stalling the storefront.
const requestTimeout = 3 * time.Second

// Client fetches the approved-supplier allow-list.
type Client struct {
	baseURL       string
	internalToken string
	http          *http.Client
	logger        *slog.Logger

	mu        sync.RWMutex
	cached    []uuid.UUID
	cachedAt  time.Time
	cacheOnce sync.Mutex
}

// NewClient builds a client against vm-profile-api.
func NewClient(baseURL, internalToken string, logger *slog.Logger) *Client {
	return &Client{
		baseURL:       baseURL,
		internalToken: internalToken,
		http:          &http.Client{Timeout: requestTimeout},
		logger:        logger,
	}
}

type approvedResponse struct {
	SupplierIDs []string `json:"supplier_ids"`
}

// ApprovedIDs returns the ids of suppliers currently allowed to sell.
//
// Returns an error rather than an empty list when the lookup fails and nothing
// is cached. An empty list would render as "no produce today", which is
// indistinguishable from a genuine empty catalogue — a dependency outage must
// look like an outage, not like a quiet Sunday.
func (c *Client) ApprovedIDs(ctx context.Context) ([]uuid.UUID, error) {
	if ids, ok := c.fresh(); ok {
		return ids, nil
	}

	// One in-flight refresh at a time: without this, a burst of storefront
	// requests on a cold cache all stampede vm-profile-api at once.
	c.cacheOnce.Lock()
	defer c.cacheOnce.Unlock()

	// Another goroutine may have refreshed while we waited for the lock.
	if ids, ok := c.fresh(); ok {
		return ids, nil
	}

	ids, err := c.fetch(ctx)
	if err != nil {
		// Serve stale rather than failing outright: produce that was on sale a
		// minute ago is a better answer than an error page, and the window is
		// bounded by how long profile stays down.
		if stale := c.stale(); stale != nil {
			c.logger.WarnContext(ctx, "serving stale approved-supplier list",
				slog.Any("error", err), slog.Int("suppliers", len(stale)))
			return stale, nil
		}
		return nil, err
	}

	c.mu.Lock()
	c.cached, c.cachedAt = ids, time.Now()
	c.mu.Unlock()

	return ids, nil
}

func (c *Client) fresh() ([]uuid.UUID, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.cached != nil && time.Since(c.cachedAt) < cacheTTL {
		return c.cached, true
	}
	return nil, false
}

func (c *Client) stale() []uuid.UUID {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.cached
}

func (c *Client) fetch(ctx context.Context) ([]uuid.UUID, error) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.baseURL+"/internal/suppliers/approved-ids", nil)
	if err != nil {
		return nil, fmt.Errorf("suppliers: building request: %w", err)
	}
	// Proves we are an internal caller (CLAUDE.md rule 5).
	req.Header.Set(httpx.InternalTokenHeader, c.internalToken)
	// Carries the correlation id across the service hop.
	if id := logging.RequestIDFrom(ctx); id != "" {
		req.Header.Set(logging.RequestIDHeader, id)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("suppliers: calling vm-profile-api: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("suppliers: vm-profile-api returned %d", resp.StatusCode)
	}

	var body approvedResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("suppliers: decoding response: %w", err)
	}

	ids := make([]uuid.UUID, 0, len(body.SupplierIDs))
	for _, raw := range body.SupplierIDs {
		parsed, err := uuid.Parse(raw)
		if err != nil {
			// One malformed id should not blank the whole storefront.
			c.logger.WarnContext(ctx, "skipping unparseable supplier id",
				slog.String("value", raw))
			continue
		}
		ids = append(ids, parsed)
	}
	return ids, nil
}

// Check returns a readiness probe confirming vm-profile-api is reachable.
func (c *Client) Check() func(ctx context.Context) error {
	return func(ctx context.Context) error {
		_, err := c.fetch(ctx)
		return err
	}
}

// nameResponse is vm-profile-api's directory reply.
type nameResponse struct {
	Suppliers []struct {
		// "supplier_id", not "id" — that is the key vm-profile-api's
		// directory endpoint actually sends.
		SupplierID   string `json:"supplier_id"`
		BusinessName string `json:"business_name"`
	} `json:"suppliers"`
}

// Name resolves one supplier's public business name.
//
// Only the trading name — never contact details, address or bank information.
// This is rendered on a public product page, so the response shape is the
// access control: there is nothing here to leak even if the page is scraped.
//
// Uncached, unlike ApprovedIDs: it is one lookup on one product page rather
// than a call on every storefront render, and a stale business name on a
// detail page is a worse trade than an extra internal request.
//
// Returns "" rather than an error when the name cannot be resolved. A product
// page that renders without the grower's name is a small loss; one that fails
// to render because an internal call was slow is a lost sale.
func (c *Client) Name(ctx context.Context, id uuid.UUID) string {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	payload, err := json.Marshal(map[string]any{"supplier_ids": []string{id.String()}})
	if err != nil {
		return ""
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/internal/suppliers/directory", bytes.NewReader(payload))
	if err != nil {
		return ""
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(httpx.InternalTokenHeader, c.internalToken)
	if rid := logging.RequestIDFrom(ctx); rid != "" {
		req.Header.Set(logging.RequestIDHeader, rid)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		c.logger.WarnContext(ctx, "supplier name lookup failed", slog.Any("error", err))
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}

	var body nameResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return ""
	}
	for _, s := range body.Suppliers {
		if s.SupplierID == id.String() {
			return s.BusinessName
		}
	}
	return ""
}

// Names resolves several suppliers' business names in one call.
//
// The admin products screen lists produce from every grower at once, and
// "which supplier is this?" is the first question asked of such a list. One
// request for the page rather than Name() per row.
//
// Best effort, like Name: an id that cannot be resolved is simply absent from
// the map, and the caller shows the id or nothing rather than failing a page.
func (c *Client) Names(ctx context.Context, ids []uuid.UUID) map[uuid.UUID]string {
	out := map[uuid.UUID]string{}
	if len(ids) == 0 {
		return out
	}

	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	raw := make([]string, 0, len(ids))
	for _, id := range ids {
		raw = append(raw, id.String())
	}

	payload, err := json.Marshal(map[string]any{"supplier_ids": raw})
	if err != nil {
		return out
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/internal/suppliers/directory", bytes.NewReader(payload))
	if err != nil {
		return out
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(httpx.InternalTokenHeader, c.internalToken)
	if rid := logging.RequestIDFrom(ctx); rid != "" {
		req.Header.Set(logging.RequestIDHeader, rid)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		c.logger.WarnContext(ctx, "supplier directory lookup failed", slog.Any("error", err))
		return out
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return out
	}

	var body nameResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return out
	}
	for _, s := range body.Suppliers {
		id, parseErr := uuid.Parse(s.SupplierID)
		if parseErr != nil {
			continue
		}
		out[id] = s.BusinessName
	}
	return out
}
