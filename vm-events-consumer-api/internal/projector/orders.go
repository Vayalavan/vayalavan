package projector

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"github.com/vayal-mikrogreenz/vm-events-consumer-api/internal/store"
)

// orderPayload mirrors vm-orders-api/internal/analyticsevents.Order, schema
// version 1. A separate copy on purpose: the consumer must keep reading the
// versions already in the stream when the producer moves on.
type orderPayload struct {
	OrderID              uuid.UUID  `json:"order_id"`
	OrderNumber          string     `json:"order_number"`
	CustomerID           uuid.UUID  `json:"customer_id"`
	ScheduleID           *uuid.UUID `json:"schedule_id"`
	Source               string     `json:"source"`
	Status               string     `json:"status"`
	SubtotalPaise        int64      `json:"subtotal_paise"`
	PlatformFeePaise     int64      `json:"platform_fee_paise"`
	DeliveryFeePaise     int64      `json:"delivery_fee_paise"`
	TotalPaise           int64      `json:"total_paise"`
	MarkupPaise          int64      `json:"markup_paise"`
	PlacedAt             time.Time  `json:"placed_at"`
	ProcessingAt         time.Time  `json:"processing_at"`
	DeliveryDay          string     `json:"delivery_day"`
	ExpectedDeliveryDate string     `json:"expected_delivery_date"`
	PlacedDateIST        string     `json:"placed_date_ist"`
	PlacedHourIST        int16      `json:"placed_hour_ist"`
	PlacedBeforeCutoff   bool       `json:"placed_before_cutoff"`
	PaymentMethod        string     `json:"payment_method"`
	PaidVia              string     `json:"paid_via"`
	CancelledAt          *time.Time `json:"cancelled_at"`
	CancelledFromStatus  string     `json:"cancelled_from_status"`
	ShipCity             string     `json:"ship_city"`
	ShipState            string     `json:"ship_state"`
	ShipPincode          string     `json:"ship_pincode"`
	Items                []struct {
		OrderItemID    uuid.UUID  `json:"order_item_id"`
		SupplierID     uuid.UUID  `json:"supplier_id"`
		ProductID      uuid.UUID  `json:"product_id"`
		SizeCodeID     *uuid.UUID `json:"size_code_id"`
		PackOptionID   uuid.UUID  `json:"pack_option_id"`
		ProductName    string     `json:"product_name"`
		ProductType    string     `json:"product_type"`
		Grade          string     `json:"grade"`
		SizeCode       string     `json:"size_code"`
		PackLabel      string     `json:"pack_label"`
		WeightGrams    int32      `json:"weight_grams"`
		Qty            int32      `json:"qty"`
		UnitPricePaise int64      `json:"unit_price_paise"`
		LineTotalPaise int64      `json:"line_total_paise"`
		LineMarkup     int64      `json:"line_markup_paise"`
	} `json:"items"`
	Payouts []struct {
		SupplierID  uuid.UUID `json:"supplier_id"`
		AmountPaise int64     `json:"amount_paise"`
	} `json:"payouts"`
}

func projectOrder(ctx context.Context, q *store.Queries, ev Event) error {
	if ev.SchemaVersion != 1 {
		return permanent("order event schema_version %d is not supported", ev.SchemaVersion)
	}
	var p orderPayload
	if err := json.Unmarshal(ev.Payload, &p); err != nil {
		return permanent("order payload: %w", err)
	}
	if p.OrderID == uuid.Nil || p.OrderID != ev.AggregateID {
		return permanent("order payload is for %s, event aggregate is %s", p.OrderID, ev.AggregateID)
	}
	placedDate, err := parseDate(p.PlacedDateIST)
	if err != nil {
		return err
	}
	deliveryDay, err := parseDate(p.DeliveryDay)
	if err != nil {
		return err
	}
	expected, err := parseDate(p.ExpectedDeliveryDate)
	if err != nil {
		return err
	}

	// The event says which transition happened; its time is when.
	at := ev.OccurredAt
	var paidAt, processedAt, dispatchedAt, expiredAt, refundedAt *time.Time
	switch ev.EventType {
	case "order.paid":
		paidAt = &at
	case "order.processed":
		processedAt = &at
	case "order.dispatched":
		dispatchedAt = &at
	case "order.expired":
		expiredAt = &at
	case "order.refunded":
		refundedAt = &at
	}
	cancelledAt := p.CancelledAt
	if cancelledAt == nil && ev.EventType == "order.cancelled" {
		cancelledAt = &at
	}

	var payable *int64
	if len(p.Payouts) > 0 {
		var sum int64
		for _, po := range p.Payouts {
			sum += po.AmountPaise
		}
		payable = &sum
	}
	var qty int32
	var grams int64
	for _, it := range p.Items {
		qty += it.Qty
		grams += int64(it.WeightGrams) * int64(it.Qty)
	}

	rows, err := q.UpsertOrder(ctx, store.UpsertOrderParams{
		OrderID:              p.OrderID,
		OrderNumber:          p.OrderNumber,
		CustomerID:           p.CustomerID,
		ScheduleID:           p.ScheduleID,
		Source:               p.Source,
		Status:               p.Status,
		PaymentMethod:        nonEmpty(p.PaymentMethod),
		PaidVia:              nonEmpty(p.PaidVia),
		SubtotalPaise:        p.SubtotalPaise,
		PlatformFeePaise:     p.PlatformFeePaise,
		DeliveryFeePaise:     p.DeliveryFeePaise,
		TotalPaise:           p.TotalPaise,
		MarkupPaise:          p.MarkupPaise,
		SupplierPayablePaise: payable,
		ItemCount:            int32(len(p.Items)),
		TotalQty:             qty,
		TotalGrams:           grams,
		PlacedAt:             p.PlacedAt,
		PaidAt:               paidAt,
		ProcessedAt:          processedAt,
		DispatchedAt:         dispatchedAt,
		CancelledAt:          cancelledAt,
		ExpiredAt:            expiredAt,
		RefundedAt:           refundedAt,
		CancelledFromStatus:  nonEmpty(p.CancelledFromStatus),
		PlacedDateIst:        placedDate,
		PlacedHourIst:        p.PlacedHourIST,
		PlacedBeforeCutoff:   p.PlacedBeforeCutoff,
		ProcessingAt:         p.ProcessingAt,
		DeliveryDay:          deliveryDay,
		ExpectedDeliveryDate: expected,
		ShipCity:             nonEmpty(p.ShipCity),
		ShipState:            nonEmpty(p.ShipState),
		ShipPincode:          nonEmpty(p.ShipPincode),
		LastEventID:          ev.ID,
		LastEventType:        ev.EventType,
		LastEventAt:          ev.OccurredAt,
	})
	if err != nil {
		return err
	}
	if rows == 0 {
		return nil // stale: the row already reflects a later event
	}

	for _, it := range p.Items {
		if err := q.UpsertOrderItem(ctx, store.UpsertOrderItemParams{
			OrderItemID:     it.OrderItemID,
			OrderID:         p.OrderID,
			SupplierID:      it.SupplierID,
			ProductID:       it.ProductID,
			SizeCodeID:      it.SizeCodeID,
			PackOptionID:    it.PackOptionID,
			ProductName:     it.ProductName,
			ProductType:     nonEmpty(it.ProductType),
			Grade:           nonEmpty(it.Grade),
			SizeCode:        nonEmpty(it.SizeCode),
			PackLabel:       it.PackLabel,
			WeightGrams:     it.WeightGrams,
			Qty:             it.Qty,
			UnitPricePaise:  it.UnitPricePaise,
			LineTotalPaise:  it.LineTotalPaise,
			LineMarkupPaise: it.LineMarkup,
			LineGrams:       int64(it.WeightGrams) * int64(it.Qty),
		}); err != nil {
			return err
		}
	}
	return nil
}

// parseDate reads a YYYY-MM-DD business day. The producer already resolved it
// in IST, so it is stored as the calendar date it names.
func parseDate(s string) (time.Time, error) {
	d, err := time.Parse(time.DateOnly, s)
	if err != nil {
		return time.Time{}, permanent("date %q: %w", s, err)
	}
	return d, nil
}

func nonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
