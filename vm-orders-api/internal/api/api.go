// Package api implements vm-orders-api's HTTP handlers.
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vayal-mikrogreenz/vm-go-common/httpx"
	"github.com/vayal-mikrogreenz/vm-go-common/isttime"
	"github.com/vayal-mikrogreenz/vm-go-common/logging"
	"github.com/vayal-mikrogreenz/vm-go-common/money"
	"github.com/vayal-mikrogreenz/vm-go-common/serviceable"

	"github.com/vayal-mikrogreenz/vm-orders-api/internal/catalogclient"
	"github.com/vayal-mikrogreenz/vm-orders-api/internal/pricing"
	"github.com/vayal-mikrogreenz/vm-orders-api/internal/razorpay"
	"github.com/vayal-mikrogreenz/vm-orders-api/internal/store"
	"github.com/vayal-mikrogreenz/vm-orders-api/internal/timeline"
)

// Order lifecycle states (mirrors the CHECK constraint).
const (
	statusPendingPayment = "pending_payment"
	statusExpired        = "expired"
)

// Reservation outcomes shared with vm-catalog-api.
const (
	outcomeCommitted = "committed"
	outcomeReleased  = "released"
)

// API carries the dependencies every handler needs.
type API struct {
	pool        *pgxpool.Pool
	queries     *store.Queries
	catalog     *catalogclient.Client
	profileURL  string
	internalTok string
	pricing     pricing.Config
	// Rates is the full commercial model — see internal/pricing/rates.go.
	Rates       pricing.Rates
	cutoffHour  int
	reserveTTL  time.Duration
	supportMail string
	// razorpay signs and verifies; the two secrets are distinct and must not
	// be interchanged (CLAUDE.md §6.4).
	razorpay              *razorpay.Client
	razorpayKeyID         string
	razorpayKeySecret     string
	razorpayWebhookSecret string
	webhookMaxAttempts    int
	wallet                WalletLimits
	schedules             ScheduleConfig
	logger                *slog.Logger
}

// Deps is what New needs.
type Deps struct {
	Pool          *pgxpool.Pool
	Catalog       *catalogclient.Client
	ProfileAPIURL string
	InternalToken string
	Pricing       pricing.Config
	// Rates carries the commission and delivery-margin figures the checkout
	// Config does not need. One source of truth for the commercial model —
	// see internal/pricing/rates.go.
	Rates                 pricing.Rates
	CutoffHourIST         int
	ReserveTTL            time.Duration
	SupportEmail          string
	Razorpay              *razorpay.Client
	RazorpayKeyID         string
	RazorpayKeySecret     string
	RazorpayWebhookSecret string
	// WebhookMaxAttempts caps retries of a temporarily failing webhook event.
	// Zero means DefaultWebhookMaxAttempts.
	WebhookMaxAttempts int
	Wallet             WalletLimits
	Schedules          ScheduleConfig
	Logger             *slog.Logger
}

// New builds the API.
func New(d Deps) *API {
	webhookMaxAttempts := d.WebhookMaxAttempts
	if webhookMaxAttempts <= 0 {
		webhookMaxAttempts = DefaultWebhookMaxAttempts
	}
	return &API{
		pool:                  d.Pool,
		queries:               store.New(d.Pool),
		catalog:               d.Catalog,
		profileURL:            d.ProfileAPIURL,
		internalTok:           d.InternalToken,
		pricing:               d.Pricing,
		Rates:                 d.Rates,
		cutoffHour:            d.CutoffHourIST,
		reserveTTL:            d.ReserveTTL,
		supportMail:           d.SupportEmail,
		razorpay:              d.Razorpay,
		razorpayKeyID:         d.RazorpayKeyID,
		razorpayKeySecret:     d.RazorpayKeySecret,
		razorpayWebhookSecret: d.RazorpayWebhookSecret,
		webhookMaxAttempts:    webhookMaxAttempts,
		wallet:                d.Wallet,
		schedules:             d.Schedules,
		logger:                d.Logger,
	}
}

func (a *API) respond(ctx context.Context, w http.ResponseWriter, status int, body any) {
	httpx.WriteJSON(ctx, w, a.logger, status, body)
}

func (a *API) fail(ctx context.Context, w http.ResponseWriter, err error) {
	httpx.WriteError(ctx, w, a.logger, err)
}

// customerFromRequest returns the authenticated customer.
func customerFromRequest(r *http.Request) (uuid.UUID, error) {
	actor, err := httpx.RequireActor(r.Context())
	if err != nil {
		return uuid.Nil, err
	}
	if actor.Role != httpx.RoleCustomer {
		return uuid.Nil, httpx.Forbidden("Only customers can do this.")
	}
	return actor.UserID, nil
}

// addressSnapshot is the delivery address frozen at placement time.
//
// Stored as JSONB on the order so it renders correctly even after the customer
// edits or deletes the address (CLAUDE.md §5.3).
type addressSnapshot struct {
	ID            string `json:"id"`
	Label         string `json:"label,omitempty"`
	RecipientName string `json:"recipient_name"`
	Phone         string `json:"phone"`
	Line1         string `json:"line1"`
	Line2         string `json:"line2,omitempty"`
	Landmark      string `json:"landmark,omitempty"`
	City          string `json:"city"`
	State         string `json:"state"`
	Pincode       string `json:"pincode"`
}

// Lines renders the snapshot as an address block for an email.
//
// Empty parts are skipped rather than printed as blank lines: a customer with
// no landmark should not receive a confirmation with a gap in the middle of
// their address.
func (a addressSnapshot) Lines() []string {
	lines := make([]string, 0, 6)
	for _, part := range []string{
		a.RecipientName, a.Phone, a.Line1, a.Line2, a.Landmark,
	} {
		if part != "" {
			lines = append(lines, part)
		}
	}
	// City, state and pincode read as one line on an envelope.
	tail := strings.TrimSpace(strings.Join([]string{a.City, a.State, a.Pincode}, " "))
	if tail != "" {
		lines = append(lines, tail)
	}
	return lines
}

// fetchAddress reads one of the customer's own addresses from vm-profile-api.
//
// Forwards the caller's identity headers, so profile applies its own ownership
// filter — this service never has to be trusted to scope the lookup correctly.
func (a *API) fetchAddress(
	ctx context.Context, customerID, addressID uuid.UUID,
) (addressSnapshot, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		fmt.Sprintf("%s/addresses/%s", a.profileURL, addressID), nil)
	if err != nil {
		return addressSnapshot{}, httpx.Internal(err)
	}
	req.Header.Set(httpx.InternalTokenHeader, a.internalTok)
	req.Header.Set(httpx.UserIDHeader, customerID.String())
	req.Header.Set(httpx.UserRoleHeader, httpx.RoleCustomer)
	if id := logging.RequestIDFrom(ctx); id != "" {
		req.Header.Set(logging.RequestIDHeader, id)
	}

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return addressSnapshot{}, httpx.Unavailable("Could not verify your delivery address.")
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		// Profile filters by owner, so "not found" also covers "not yours".
		return addressSnapshot{}, httpx.Validation("That delivery address was not found.",
			map[string]any{"address_id": "is not one of your addresses"})
	}
	if resp.StatusCode != http.StatusOK {
		return addressSnapshot{}, httpx.Unavailable("Could not verify your delivery address.")
	}

	var body struct {
		ID            string  `json:"id"`
		Label         *string `json:"label"`
		RecipientName string  `json:"recipient_name"`
		Phone         string  `json:"phone"`
		Line1         string  `json:"line1"`
		Line2         *string `json:"line2"`
		Landmark      *string `json:"landmark"`
		City          string  `json:"city"`
		State         string  `json:"state"`
		Pincode       string  `json:"pincode"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return addressSnapshot{}, httpx.Internal(err)
	}

	// The delivery area, re-checked at the last moment it can be.
	//
	// vm-profile-api already refuses to save an address outside it, so this
	// normally passes — but an address stored before the area was narrowed is
	// still sitting in the customer's address book, and this is the money
	// path. Better a 422 at checkout than a parcel we cannot deliver.
	if !serviceable.Pincode(body.Pincode) {
		return addressSnapshot{}, httpx.Validation(
			"We do not deliver to that address yet.",
			map[string]any{"address_id": serviceable.PincodeFieldError})
	}

	return addressSnapshot{
		ID:            body.ID,
		Label:         deref(body.Label),
		RecipientName: body.RecipientName,
		Phone:         body.Phone,
		Line1:         body.Line1,
		Line2:         deref(body.Line2),
		Landmark:      deref(body.Landmark),
		City:          body.City,
		State:         body.State,
		Pincode:       body.Pincode,
	}, nil
}

func deref(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

// ---------------------------------------------------------------------------
// Shared response shapes
// ---------------------------------------------------------------------------

type orderItemResponse struct {
	ID            string `json:"id"`
	ProductID     string `json:"product_id"`
	ProductUnitID string `json:"product_unit_id"`
	ProductName   string `json:"product_name"`
	UnitLabel     string `json:"unit_label"`
	Grade         string `json:"grade,omitempty"`
	// The GRADE this pack was bought at, snapshotted at placement. Omitted on
	// lines from ungraded listings, and on every order placed before size
	// codes existed — the clients render a missing grade as no grade.
	SizeCode       string `json:"size_code,omitempty"`
	SizeMeta       string `json:"size_meta,omitempty"`
	WeightGrams    int32  `json:"weight_grams"`
	UnitPricePaise int64  `json:"unit_price_paise"`
	UnitPrice      string `json:"unit_price_display"`
	Qty            int32  `json:"qty"`
	LineTotalPaise int64  `json:"line_total_paise"`
	LineTotal      string `json:"line_total_display"`
	// ImageURL is the one field here that is NOT a snapshot, and cannot be:
	// products store an object key and the URL is presigned at read time
	// (CLAUDE.md §5.2). Absent when the product has no photograph, was
	// deleted, or when the catalogue could not be reached — an order history
	// must render either way, so the client shows a placeholder.
	ImageURL string `json:"image_url,omitempty"`
}

type orderResponse struct {
	ID          string `json:"id"`
	OrderNumber string `json:"order_number"`
	Status      string `json:"status"`

	SubtotalPaise    int64  `json:"subtotal_paise"`
	Subtotal         string `json:"subtotal_display"`
	PlatformFeePaise int64  `json:"platform_fee_paise"`
	PlatformFee      string `json:"platform_fee_display"`
	DeliveryFeePaise int64  `json:"delivery_fee_paise"`
	DeliveryFee      string `json:"delivery_fee_display"`
	TotalPaise       int64  `json:"total_paise"`
	Total            string `json:"total_display"`

	PlacedAt string `json:"placed_at"`
	// The fulfilment dates. Present on every order response, not just the
	// detail view: the admin queue shows a Delivery column, and without these
	// it rendered blank for every row.
	DeliveryDay      string `json:"delivery_day"`
	ExpectedDelivery string `json:"expected_delivery_date"`
	// Milestones carry `completed` derived from timestamps at READ time
	// (CLAUDE.md §6.1) — never a stored flag.
	Milestones           []timeline.Milestone `json:"milestones"`
	ExpectedDeliveryText string               `json:"expected_delivery_text"`
	CourierNotice        string               `json:"courier_notice"`
	SupportNotice        string               `json:"support_notice"`

	// Present on a freshly-created order so the browser can open Checkout.
	RazorpayOrderID *string `json:"razorpay_order_id,omitempty"`
	RazorpayKeyID   string  `json:"razorpay_key_id,omitempty"`

	// The schedule that placed this order, when a repeat or scheduled delivery
	// did (CLAUDE.md §6.7) — so a client can label it.
	ScheduleID *string `json:"schedule_id,omitempty"`

	Address addressSnapshot     `json:"address"`
	Items   []orderItemResponse `json:"items"`
}

// toOrderResponse renders an order, deriving milestone completion from now.
func (a *API) toOrderResponse(
	order store.Order, items []store.OrderItem, now time.Time,
) orderResponse {
	schedule := timeline.Schedule{
		PlacedAt:             order.PlacedAt,
		ProcessingAt:         order.ProcessingAt,
		DeliveryDay:          order.DeliveryDay,
		ExpectedDeliveryDate: order.ExpectedDeliveryDate,
	}

	var address addressSnapshot
	_ = json.Unmarshal(order.AddressSnapshot, &address)

	out := orderResponse{
		ID:          order.ID.String(),
		OrderNumber: order.OrderNumber,
		Status:      order.Status,

		SubtotalPaise:    order.SubtotalPaise,
		Subtotal:         money.FormatRupees(money.Paise(order.SubtotalPaise)),
		PlatformFeePaise: order.PlatformFeePaise,
		PlatformFee:      money.FormatRupees(money.Paise(order.PlatformFeePaise)),
		DeliveryFeePaise: order.DeliveryFeePaise,
		DeliveryFee:      money.FormatRupees(money.Paise(order.DeliveryFeePaise)),
		TotalPaise:       order.TotalPaise,
		Total:            money.FormatRupees(money.Paise(order.TotalPaise)),

		PlacedAt:             order.PlacedAt.Format(time.RFC3339),
		DeliveryDay:          isttime.FormatDate(order.DeliveryDay),
		ExpectedDelivery:     isttime.FormatDate(order.ExpectedDeliveryDate),
		Milestones:           schedule.MilestonesForStatus(now, order.Status),
		ExpectedDeliveryText: schedule.ExpectedDeliveryText(),
		CourierNotice:        timeline.CourierNotice,
		SupportNotice:        timeline.SupportNotice + a.supportMail,

		Address: address,
		Items:   make([]orderItemResponse, 0, len(items)),
	}
	if order.ScheduleID != nil {
		id := order.ScheduleID.String()
		out.ScheduleID = &id
	}

	for _, item := range items {
		out.Items = append(out.Items, orderItemResponse{
			ID:             item.ID.String(),
			ProductID:      item.ProductID.String(),
			ProductUnitID:  item.ProductUnitID.String(),
			ProductName:    item.ProductNameSnapshot,
			UnitLabel:      item.UnitLabelSnapshot,
			Grade:          deref(item.GradeSnapshot),
			SizeCode:       deref(item.SizeCodeSnapshot),
			SizeMeta:       deref(item.SizeMetaSnapshot),
			WeightGrams:    item.WeightGrams,
			UnitPricePaise: item.UnitPricePaise,
			UnitPrice:      money.FormatRupees(money.Paise(item.UnitPricePaise)),
			Qty:            item.Qty,
			LineTotalPaise: item.LineTotalPaise,
			LineTotal:      money.FormatRupees(money.Paise(item.LineTotalPaise)),
		})
	}
	return out
}

// attachItemImages fills in each line's ImageURL from the catalogue.
//
// A separate pass rather than part of toOrderResponse, which is a pure mapper
// used by the admin screens too: this needs a context and a network call, and
// putting I/O inside the mapper would give every admin order fetch a catalogue
// round trip it has no use for.
//
// BEST EFFORT, deliberately. A customer's order history is a record of what
// they bought and what they paid — it must render when the catalogue is down,
// when a product has been deleted, and when an image key no longer signs. Every
// one of those cases leaves ImageURL empty and the client shows a placeholder,
// which is already what it does for produce that never had a photograph.
//
// One call for the whole page, deduplicated by product: twenty orders of the
// same tomatoes ask once.
func (a *API) attachItemImages(ctx context.Context, orders []orderResponse) {
	ids := make([]uuid.UUID, 0, len(orders))
	seen := map[uuid.UUID]bool{}
	for i := range orders {
		for _, item := range orders[i].Items {
			productID, err := uuid.Parse(item.ProductID)
			if err != nil || seen[productID] {
				continue
			}
			seen[productID] = true
			ids = append(ids, productID)
		}
	}
	if len(ids) == 0 {
		return
	}

	images, err := a.catalog.ProductImages(ctx, ids)
	if err != nil {
		// Logged, not returned: see the note above about rendering anyway.
		a.logger.WarnContext(ctx, "could not fetch order item images",
			slog.String("error", err.Error()))
		return
	}

	for i := range orders {
		for j := range orders[i].Items {
			productID, parseErr := uuid.Parse(orders[i].Items[j].ProductID)
			if parseErr != nil {
				continue
			}
			if url, found := images[productID]; found {
				orders[i].Items[j].ImageURL = url
			}
		}
	}
}
