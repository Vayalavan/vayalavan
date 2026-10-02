package analyticsevents

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vayal-mikrogreenz/vm-orders-api/internal/store"
)

// fakeQueries answers the reads Build makes and records the event written.
// Embedding the interface means any OTHER query panics, which is the point:
// building an event must not reach anything unexpected.
type fakeQueries struct {
	store.Querier
	order   store.Order
	items   []store.OrderItem
	payouts []store.SupplierPayout
	payment *store.Payment
	written []store.InsertOrderEventParams
}

func (f *fakeQueries) GetOrderByID(context.Context, uuid.UUID) (store.Order, error) {
	return f.order, nil
}
func (f *fakeQueries) ListOrderItems(context.Context, uuid.UUID) ([]store.OrderItem, error) {
	return f.items, nil
}
func (f *fakeQueries) ListPayoutsForOrder(context.Context, uuid.UUID) ([]store.SupplierPayout, error) {
	return f.payouts, nil
}
func (f *fakeQueries) GetPaymentForOrder(context.Context, uuid.UUID) (store.Payment, error) {
	if f.payment == nil {
		return store.Payment{}, pgx.ErrNoRows
	}
	return *f.payment, nil
}
func (f *fakeQueries) InsertOrderEvent(_ context.Context, p store.InsertOrderEventParams) (uuid.UUID, error) {
	f.written = append(f.written, p)
	return uuid.New(), nil
}

func ptr[T any](v T) *T { return &v }

func ist(t *testing.T, value string) time.Time {
	t.Helper()
	loc, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := time.ParseInLocation("2006-01-02 15:04", value, loc)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

// sampleOrder is an order whose snapshot is full of exactly the personal data
// analytics must never receive.
func sampleOrder(t *testing.T, placed, processing string) *fakeQueries {
	address, _ := json.Marshal(map[string]string{
		"id": "addr-1", "label": "Home", "recipient_name": "Meena Raman",
		"phone": "9876543210", "line1": "12 Temple Street", "line2": "Flat 4",
		"landmark": "Opp. the tank", "city": "Chennai", "state": "Tamil Nadu",
		"pincode": "600004",
	})
	orderID := uuid.New()
	return &fakeQueries{
		order: store.Order{
			ID: orderID, OrderNumber: "VM-260615-0001", CustomerID: uuid.New(),
			Status: "paid", AddressSnapshot: address,
			SubtotalPaise: 50000, PlatformFeePaise: 1500, DeliveryFeePaise: 1500, TotalPaise: 53000,
			PlacedAt: ist(t, placed), ProcessingAt: ist(t, processing),
			DeliveryDay: ist(t, "2026-06-16 00:00"), ExpectedDeliveryDate: ist(t, "2026-06-17 00:00"),
			RazorpayOrderID: ptr("order_SECRET"), RazorpayPaymentID: ptr("pay_SECRET"),
			CancelReason: ptr("Customer Meena called to cancel"),
		},
		items: []store.OrderItem{{
			ID: uuid.New(), OrderID: orderID, SupplierID: uuid.New(), ProductID: uuid.New(),
			ProductUnitID: uuid.New(), ProductNameSnapshot: "Tomato", UnitLabelSnapshot: "1 kg box",
			SizeCodeSnapshot: ptr("M"), WeightGrams: 1000, Qty: 2, UnitPricePaise: 25000,
			LineTotalPaise: 50000, LineMarkupPaise: 2000, ProductTypeSnapshot: ptr("vegetable"),
		}},
		payouts: []store.SupplierPayout{{SupplierID: uuid.New(), AmountPaise: 46500,
			ReferenceNo: ptr("UTR-SECRET")}},
		payment: &store.Payment{Status: "captured", Method: ptr("upi"),
			ProviderPaymentID: ptr("pay_SECRET")},
	}
}

// TestEventCarriesNoPersonalData — the payload's keys are an allow-list, and
// none of the personal or payment values in the source rows reach it.
func TestEventCarriesNoPersonalData(t *testing.T) {
	f := sampleOrder(t, "2026-06-15 09:00", "2026-06-15 16:00")
	if err := Emit(context.Background(), f, f.order.ID, OrderPaid, ActorSystem,
		time.Now(), Options{PaymentMethod: "upi", PaidVia: PaidViaWebhook}); err != nil {
		t.Fatal(err)
	}
	if len(f.written) != 1 {
		t.Fatalf("wrote %d events, want 1", len(f.written))
	}
	raw := string(f.written[0].Payload)

	for _, secret := range []string{
		"Meena", "9876543210", "Temple", "Flat 4", "Opp. the tank", "addr-1", "Home",
		"order_SECRET", "pay_SECRET", "UTR-SECRET", "called to cancel",
	} {
		if strings.Contains(raw, secret) {
			t.Errorf("payload leaks %q: %s", secret, raw)
		}
	}

	var top map[string]json.RawMessage
	if err := json.Unmarshal(f.written[0].Payload, &top); err != nil {
		t.Fatal(err)
	}
	allowed := []string{
		"order_id", "order_number", "customer_id", "schedule_id", "source", "status",
		"subtotal_paise", "platform_fee_paise", "delivery_fee_paise", "total_paise",
		"markup_paise", "placed_at", "processing_at", "delivery_day",
		"expected_delivery_date", "placed_date_ist", "placed_hour_ist",
		"placed_before_cutoff", "payment_method", "paid_via", "cancelled_from_status", "cancelled_at",
		"ship_city", "ship_state", "ship_pincode", "items", "payouts",
	}
	allow := map[string]bool{}
	for _, k := range allowed {
		allow[k] = true
	}
	var unexpected []string
	for k := range top {
		if !allow[k] {
			unexpected = append(unexpected, k)
		}
	}
	sort.Strings(unexpected)
	if len(unexpected) > 0 {
		t.Errorf("payload has keys outside the allow-list: %v — adding a field to the "+
			"analytics payload is a decision about what the analytics role may see", unexpected)
	}
}

// TestEventFields — the derived fields analytics relies on.
func TestEventFields(t *testing.T) {
	tests := []struct {
		name, placed, processing string
		wantBefore               bool
		wantHour                 int
		wantDate                 string
	}{
		{"before the cutoff", "2026-06-15 09:00", "2026-06-15 16:00", true, 9, "2026-06-15"},
		{"at the cutoff", "2026-06-15 16:00", "2026-06-16 16:00", false, 16, "2026-06-15"},
		{"late evening", "2026-06-15 23:45", "2026-06-16 16:00", false, 23, "2026-06-15"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := sampleOrder(t, tc.placed, tc.processing)
			got, err := Build(context.Background(), f, f.order.ID, Options{})
			if err != nil {
				t.Fatal(err)
			}
			if got.PlacedBeforeCutoff != tc.wantBefore {
				t.Errorf("placed_before_cutoff = %v, want %v", got.PlacedBeforeCutoff, tc.wantBefore)
			}
			if got.PlacedHourIST != tc.wantHour || got.PlacedDateIST != tc.wantDate {
				t.Errorf("placed IST = %s %d, want %s %d",
					got.PlacedDateIST, got.PlacedHourIST, tc.wantDate, tc.wantHour)
			}
			if got.ShipCity != "Chennai" || got.ShipPincode != "600004" {
				t.Errorf("ship = %s %s", got.ShipCity, got.ShipPincode)
			}
			if got.MarkupPaise != 2000 || len(got.Items) != 1 || len(got.Payouts) != 1 {
				t.Errorf("markup %d, items %d, payouts %d", got.MarkupPaise, len(got.Items), len(got.Payouts))
			}
			// No method on this event: the captured payment's is used, so a
			// processed event does not erase how the order was paid.
			if got.PaymentMethod != "upi" {
				t.Errorf("payment_method = %q, want upi", got.PaymentMethod)
			}
			if got.Source != "checkout" {
				t.Errorf("source = %q", got.Source)
			}
		})
	}
}
