// Package analyticsevents writes order events to order_events_outbox, the
// orders half of the analytics pipeline (CLAUDE.md §5.4).
//
// Every event carries the whole order as analytics may see it AFTER the
// change, so the consumer upserts rather than replays deltas, and a missed or
// repeated event still converges on the right row.
//
// The payload is built from the types below and nothing else. They have no
// field for a customer's name, phone, email, street address or landmark, no
// Razorpay or bank reference and no free text (cancel reasons are written by
// admins and routinely name the customer): the analytics role is meant to see
// none of it, and a type that cannot hold a field is a stronger guarantee than
// a review that remembers to strip one. The delivery location is kept to
// city, state and pincode — what a regional breakdown needs.
package analyticsevents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vayal-mikrogreenz/vm-go-common/isttime"
	"github.com/vayal-mikrogreenz/vm-go-common/logging"

	"github.com/vayal-mikrogreenz/vm-orders-api/internal/store"
)

// SchemaVersion is the payload shape below. Bump it on any change a consumer
// would have to handle differently, and teach the consumer first.
const SchemaVersion = 1

// Event types. Must match the CHECK on order_events_outbox.event_type.
const (
	OrderPlaced     = "order.placed"
	OrderPaid       = "order.paid"
	OrderExpired    = "order.expired"
	OrderCancelled  = "order.cancelled"
	OrderProcessed  = "order.processed"
	OrderDispatched = "order.dispatched"
	OrderSnapshot   = "order.snapshot"
)

// Actors. Must match the CHECK on order_events_outbox.actor_role.
const (
	ActorCustomer = "customer"
	ActorAdmin    = "admin"
	ActorSystem   = "system"
)

// Order is the analytics view of an order.
type Order struct {
	OrderID     uuid.UUID  `json:"order_id"`
	OrderNumber string     `json:"order_number"`
	CustomerID  uuid.UUID  `json:"customer_id"`
	ScheduleID  *uuid.UUID `json:"schedule_id"`
	// "checkout" or "schedule" — which flow placed it.
	Source string `json:"source"`
	Status string `json:"status"`

	SubtotalPaise    int64 `json:"subtotal_paise"`
	PlatformFeePaise int64 `json:"platform_fee_paise"`
	DeliveryFeePaise int64 `json:"delivery_fee_paise"`
	TotalPaise       int64 `json:"total_paise"`
	// Sum of the lines' markup: what we added on top of growers' prices.
	MarkupPaise int64 `json:"markup_paise"`

	PlacedAt             time.Time `json:"placed_at"`
	ProcessingAt         time.Time `json:"processing_at"`
	DeliveryDay          string    `json:"delivery_day"`           // YYYY-MM-DD, IST
	ExpectedDeliveryDate string    `json:"expected_delivery_date"` // YYYY-MM-DD, IST
	PlacedDateIST        string    `json:"placed_date_ist"`
	PlacedHourIST        int       `json:"placed_hour_ist"`
	// Processed the day it was placed, i.e. placed before the cutoff
	// (CLAUDE.md §6.1). Derived from the stored timeline rather than from the
	// cutoff hour, so it can never disagree with what the order was promised.
	PlacedBeforeCutoff bool `json:"placed_before_cutoff"`

	// Empty until a payment is captured; set from the event that paid it.
	PaymentMethod string `json:"payment_method,omitempty"`
	// How it was paid: "razorpay_webhook", "admin_manual" or "wallet".
	PaidVia string `json:"paid_via,omitempty"`
	// When it was cancelled, from the order row — so a backfilled snapshot of
	// a cancelled order still says when.
	CancelledAt *time.Time `json:"cancelled_at"`
	// The status an order was in when an admin cancelled it. "paid" or later
	// means money was taken and a refund is owed.
	CancelledFromStatus string `json:"cancelled_from_status,omitempty"`

	ShipCity    string `json:"ship_city"`
	ShipState   string `json:"ship_state"`
	ShipPincode string `json:"ship_pincode"`

	Items   []Item   `json:"items"`
	Payouts []Payout `json:"payouts"`
}

// Item is one order line, as sold.
type Item struct {
	OrderItemID    uuid.UUID  `json:"order_item_id"`
	SupplierID     uuid.UUID  `json:"supplier_id"`
	ProductID      uuid.UUID  `json:"product_id"`
	SizeCodeID     *uuid.UUID `json:"size_code_id"`
	PackOptionID   uuid.UUID  `json:"pack_option_id"`
	ProductName    string     `json:"product_name"`
	ProductType    string     `json:"product_type,omitempty"`
	Grade          string     `json:"grade,omitempty"`
	SizeCode       string     `json:"size_code,omitempty"`
	PackLabel      string     `json:"pack_label"`
	WeightGrams    int32      `json:"weight_grams"`
	Qty            int32      `json:"qty"`
	UnitPricePaise int64      `json:"unit_price_paise"`
	LineTotalPaise int64      `json:"line_total_paise"`
	LineMarkup     int64      `json:"line_markup_paise"`
}

// Payout is what one supplier is owed for the order. Present once paid.
type Payout struct {
	SupplierID  uuid.UUID `json:"supplier_id"`
	AmountPaise int64     `json:"amount_paise"`
}

// Options carries what only the caller knows about this transition.
type Options struct {
	PaymentMethod       string
	PaidVia             string
	CancelledFromStatus string
}

// Paid-via values.
const (
	PaidViaWebhook = "razorpay_webhook"
	PaidViaAdmin   = "admin_manual"
	PaidViaWallet  = "wallet"
)

// Emit writes one order event in the caller's transaction.
//
// `q` must be bound to that transaction: the order is re-read through it, so
// the payload is the state being committed, and the event rolls back with
// the change it describes.
func Emit(
	ctx context.Context, q store.Querier, orderID uuid.UUID,
	eventType, actor string, occurredAt time.Time, opts Options,
) error {
	snapshot, err := Build(ctx, q, orderID, opts)
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
	_, err = q.InsertOrderEvent(ctx, store.InsertOrderEventParams{
		AggregateID:   orderID,
		EventType:     eventType,
		SchemaVersion: SchemaVersion,
		OccurredAt:    occurredAt,
		ActorRole:     actor,
		RequestID:     requestID,
		Payload:       payload,
	})
	if err != nil {
		return fmt.Errorf("writing %s event: %w", eventType, err)
	}
	return nil
}

// Build assembles the analytics view of an order.
func Build(ctx context.Context, q store.Querier, orderID uuid.UUID, opts Options) (Order, error) {
	order, err := q.GetOrderByID(ctx, orderID)
	if err != nil {
		return Order{}, err
	}
	items, err := q.ListOrderItems(ctx, orderID)
	if err != nil {
		return Order{}, err
	}
	payouts, err := q.ListPayoutsForOrder(ctx, orderID)
	if err != nil {
		return Order{}, err
	}

	out := Order{
		OrderID:              order.ID,
		OrderNumber:          order.OrderNumber,
		CustomerID:           order.CustomerID,
		ScheduleID:           order.ScheduleID,
		Source:               "checkout",
		Status:               order.Status,
		SubtotalPaise:        order.SubtotalPaise,
		PlatformFeePaise:     order.PlatformFeePaise,
		DeliveryFeePaise:     order.DeliveryFeePaise,
		TotalPaise:           order.TotalPaise,
		PlacedAt:             order.PlacedAt.UTC(),
		ProcessingAt:         order.ProcessingAt.UTC(),
		DeliveryDay:          isttime.FormatISODate(order.DeliveryDay),
		ExpectedDeliveryDate: isttime.FormatISODate(order.ExpectedDeliveryDate),
		PlacedDateIST:        isttime.FormatISODate(order.PlacedAt),
		PlacedHourIST:        isttime.ToIST(order.PlacedAt).Hour(),
		PlacedBeforeCutoff:   isttime.SameDayIST(order.PlacedAt, order.ProcessingAt),
		PaymentMethod:        opts.PaymentMethod,
		PaidVia:              opts.PaidVia,
		CancelledFromStatus:  opts.CancelledFromStatus,
		CancelledAt:          order.CancelledAt,
		Items:                make([]Item, 0, len(items)),
		Payouts:              make([]Payout, 0, len(payouts)),
	}
	if order.ScheduleID != nil {
		out.Source = "schedule"
	}

	// The address is unmarshalled into a struct with ONLY the three fields
	// analytics may see; the rest of the snapshot is never decoded.
	var place struct {
		City    string `json:"city"`
		State   string `json:"state"`
		Pincode string `json:"pincode"`
	}
	if len(order.AddressSnapshot) > 0 {
		if err := json.Unmarshal(order.AddressSnapshot, &place); err != nil {
			return Order{}, fmt.Errorf("reading address snapshot: %w", err)
		}
	}
	out.ShipCity, out.ShipState, out.ShipPincode = place.City, place.State, place.Pincode

	// A captured payment's method, when this event did not supply one — so a
	// later event (processed, dispatched) still says how the order was paid.
	if out.PaymentMethod == "" {
		payment, err := q.GetPaymentForOrder(ctx, orderID)
		switch {
		case err == nil:
			if payment.Status == "captured" && payment.Method != nil {
				out.PaymentMethod = *payment.Method
			}
		case !errors.Is(err, pgx.ErrNoRows):
			return Order{}, err
		}
	}

	for _, it := range items {
		out.MarkupPaise += it.LineMarkupPaise
		out.Items = append(out.Items, Item{
			OrderItemID:    it.ID,
			SupplierID:     it.SupplierID,
			ProductID:      it.ProductID,
			SizeCodeID:     it.SizeCodeID,
			PackOptionID:   it.ProductUnitID,
			ProductName:    it.ProductNameSnapshot,
			ProductType:    deref(it.ProductTypeSnapshot),
			Grade:          deref(it.GradeSnapshot),
			SizeCode:       deref(it.SizeCodeSnapshot),
			PackLabel:      it.UnitLabelSnapshot,
			WeightGrams:    it.WeightGrams,
			Qty:            it.Qty,
			UnitPricePaise: it.UnitPricePaise,
			LineTotalPaise: it.LineTotalPaise,
			LineMarkup:     it.LineMarkupPaise,
		})
	}
	for _, p := range payouts {
		out.Payouts = append(out.Payouts, Payout{SupplierID: p.SupplierID, AmountPaise: p.AmountPaise})
	}
	return out, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
