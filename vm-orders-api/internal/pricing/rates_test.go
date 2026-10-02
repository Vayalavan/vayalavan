package pricing

import (
	"testing"

	"github.com/vayal-mikrogreenz/vm-go-common/money"
)

// paise is a readability shim so the tables read in plain integers.
func paise(v int64) money.Paise { return money.Paise(v) }

// TestSupplierCommissionAndPayable pins the grower's side of the deal.
//
// The commission is DEDUCTED from what a supplier is paid, unlike the platform
// fee which is ADDED to what a customer pays. Getting the direction wrong
// would silently underpay or overpay every grower, so the two are asserted
// together on the same subtotal.
func TestSupplierCommissionAndPayable(t *testing.T) {
	r := DefaultRates()

	tests := []struct {
		name           string
		subtotalPaise  int64
		wantCommission int64
		wantPayable    int64
	}{
		{"round hundred", 100000, 3000, 97000}, // ₹1000 → ₹30, ₹970
		{"the seeded order", 6000, 180, 5820},  // ₹60 → ₹1.80, ₹58.20
		{"zero sells nothing", 0, 0, 0},
		{"one paise rounds to zero commission", 1, 0, 1},
		// 1650 * 300 / 10000 = 49.5 -> half-up 50
		{"half a paise rounds up", 1650, 50, 1600},
		// 1616 * 300 / 10000 = 48.48 -> 48
		{"below half rounds down", 1616, 48, 1568},
		{"large basket", 5000000, 150000, 4850000},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := int64(r.SupplierCommission(paise(tc.subtotalPaise))); got != tc.wantCommission {
				t.Errorf("commission = %d, want %d", got, tc.wantCommission)
			}
			if got := int64(r.SupplierPayable(paise(tc.subtotalPaise))); got != tc.wantPayable {
				t.Errorf("payable = %d, want %d", got, tc.wantPayable)
			}
			// The invariant that matters: nothing is created or destroyed.
			sum := int64(r.SupplierCommission(paise(tc.subtotalPaise))) +
				int64(r.SupplierPayable(paise(tc.subtotalPaise)))
			if sum != tc.subtotalPaise {
				t.Errorf("commission + payable = %d, want %d — paise went missing",
					sum, tc.subtotalPaise)
			}
		})
	}
}

// TestEarningsSplit — our revenue has three sources that behave differently,
// and the total must equal their sum exactly.
func TestEarningsSplit(t *testing.T) {
	r := DefaultRates()
	e := r.EarningsFor(paise(100000), paise(100000))

	if int64(e.PlatformFeePaise) != 3000 {
		t.Errorf("platform fee = %d, want 3000", e.PlatformFeePaise)
	}
	if int64(e.SupplierCommissionPaise) != 3000 {
		t.Errorf("supplier commission = %d, want 3000", e.SupplierCommissionPaise)
	}
	if int64(e.DeliveryMarginPaise) != 500 {
		t.Errorf("delivery margin = %d, want 500", e.DeliveryMarginPaise)
	}
	if int64(e.TotalPaise) != 6500 {
		t.Errorf("total = %d, want 6500", e.TotalPaise)
	}
}

// TestDeliveryMarginIsFlat — the margin does not scale with basket size, which
// is the whole reason it is reported separately from the percentage fees.
func TestDeliveryMarginIsFlat(t *testing.T) {
	r := DefaultRates()
	small := r.EarningsFor(paise(1000), paise(1000))
	large := r.EarningsFor(paise(10000000), paise(10000000))
	if small.DeliveryMarginPaise != large.DeliveryMarginPaise {
		t.Errorf("delivery margin varied with basket size: %d vs %d",
			small.DeliveryMarginPaise, large.DeliveryMarginPaise)
	}
}

func TestValidateRatesRejectsImpossibleConfigurations(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Rates)
		wantErr bool
	}{
		{"defaults are valid", func(*Rates) {}, false},
		{"zero commission is valid — suppliers paid in full",
			func(r *Rates) { r.SupplierCommissionBPS = 0 }, false},
		{"negative platform fee",
			func(r *Rates) { r.PlatformFeeBPS = -1 }, true},
		{"negative commission",
			func(r *Rates) { r.SupplierCommissionBPS = -1 }, true},
		{"commission above 100%",
			func(r *Rates) { r.SupplierCommissionBPS = 10001 }, true},
		{"commission of exactly 100% is allowed, if absurd",
			func(r *Rates) { r.SupplierCommissionBPS = 10000 }, false},
		{"negative delivery fee",
			func(r *Rates) { r.DeliveryFeePaise = -1 }, true},
		// The one most likely to be typed by mistake: keeping more of the
		// delivery charge than the customer actually pays.
		{"margin exceeding the delivery fee",
			func(r *Rates) { r.DeliveryMarginPaise = 2000 }, true},
		{"margin equal to the fee is allowed — no courier cost",
			func(r *Rates) { r.DeliveryMarginPaise = r.DeliveryFeePaise }, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := DefaultRates()
			tc.mutate(&r)
			err := ValidateRates(r)
			if tc.wantErr && err == nil {
				t.Error("expected an error, got none")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

// TestSupplierPayableNeverNegative — a misconfiguration must not turn into an
// invoice sent to a farmer.
func TestSupplierPayableNeverNegative(t *testing.T) {
	r := DefaultRates()
	r.SupplierCommissionBPS = 20000 // 200%, rejected by ValidateRates
	if got := r.SupplierPayable(paise(1000)); got < 0 {
		t.Errorf("payable = %d, want clamped at 0", got)
	}
}

// TestPerSupplierCommissionOverride — a negotiated rate replaces the platform
// default for that supplier only.
func TestPerSupplierCommissionOverride(t *testing.T) {
	r := DefaultRates() // 300 bps = 3%
	bps := func(v int64) *int64 { return &v }

	tests := []struct {
		name           string
		override       *int64
		subtotal       int64
		wantCommission int64
		wantPayable    int64
	}{
		{"no override falls back to the platform rate", nil, 100000, 3000, 97000},
		{"a lower negotiated rate", bps(150), 100000, 1500, 98500},
		{"a higher negotiated rate", bps(1000), 100000, 10000, 90000},
		// The reason the override is a POINTER: zero is a real arrangement,
		// and must not be mistaken for "not set".
		{"zero commission is honoured, not treated as unset", bps(0), 100000, 0, 100000},
		{"100% is permitted, if absurd", bps(10000), 100000, 100000, 0},
		// Out of range means something upstream is broken. Falling back beats
		// charging a farmer a rate nobody agreed to.
		{"a negative override falls back", bps(-1), 100000, 3000, 97000},
		{"an over-100% override falls back", bps(10001), 100000, 3000, 97000},
		// Rounding follows the same half-up rule as everything else.
		{"rounds half up at a custom rate", bps(150), 1000, 15, 985},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := int64(r.SupplierCommissionAt(paise(tc.subtotal), tc.override))
			if got != tc.wantCommission {
				t.Errorf("commission = %d, want %d", got, tc.wantCommission)
			}
			payable := int64(r.SupplierPayableAt(paise(tc.subtotal), tc.override))
			if payable != tc.wantPayable {
				t.Errorf("payable = %d, want %d", payable, tc.wantPayable)
			}
			// Nothing is created or destroyed, whatever the rate.
			if got+payable != tc.subtotal {
				t.Errorf("commission + payable = %d, want %d — paise went missing",
					got+payable, tc.subtotal)
			}
		})
	}
}

// TestOverrideDoesNotLeakBetweenSuppliers — one grower's negotiated rate must
// not change what anyone else is paid.
func TestOverrideDoesNotLeakBetweenSuppliers(t *testing.T) {
	r := DefaultRates()
	negotiated := int64(150)

	withOverride := r.SupplierPayableAt(paise(100000), &negotiated)
	withoutOverride := r.SupplierPayableAt(paise(100000), nil)

	if withOverride == withoutOverride {
		t.Fatal("the override had no effect")
	}
	if int64(withoutOverride) != 97000 {
		t.Errorf("default supplier payable = %d, want 97000 — the override "+
			"changed the platform rate", withoutOverride)
	}
}

// ---------------------------------------------------------------------------
// Markup — the fourth source of revenue.
//
// The trap this guards is double counting. Commission is derived from
// "supplier gross minus payouts", and if the markup is left inside supplier
// gross it falls into that difference AND is added again as markup revenue —
// a total that overstates what the business earned, in a plausible way.
// ---------------------------------------------------------------------------

func TestEarningsWithMarkup(t *testing.T) {
	rates := DefaultRates() // 3% platform, 3% commission, ₹15 delivery, ₹5 margin

	// One 5 kg pack: grower charges ₹210, we add ₹10, customer pays ₹220.
	const (
		customerSubtotal = money.Paise(22000)
		supplierSubtotal = money.Paise(21000)
		markup           = money.Paise(1000)
	)

	got := rates.EarningsWithMarkup(customerSubtotal, supplierSubtotal, markup)

	// The platform fee is charged on what the customer paid, which includes
	// the markup: 3% of ₹220.
	if want := money.Paise(660); got.PlatformFeePaise != want {
		t.Errorf("platform fee = %d, want %d", got.PlatformFeePaise, want)
	}
	// Commission is charged on the GROWER's value, not the marked-up one:
	// 3% of ₹210, not of ₹220.
	if want := money.Paise(630); got.SupplierCommissionPaise != want {
		t.Errorf("supplier commission = %d, want %d", got.SupplierCommissionPaise, want)
	}
	if want := money.Paise(1000); got.MarkupPaise != want {
		t.Errorf("markup = %d, want %d", got.MarkupPaise, want)
	}
	// 660 + 630 + 500 + 1000.
	if want := money.Paise(2790); got.TotalPaise != want {
		t.Errorf("total = %d, want %d", got.TotalPaise, want)
	}
}

// The markup is counted once, in its own bucket — never also inside the
// commission by way of an inflated supplier subtotal.
func TestMarkupIsCountedOnce(t *testing.T) {
	rates := DefaultRates()

	const (
		customerSubtotal = money.Paise(22000)
		supplierSubtotal = money.Paise(21000)
		markup           = money.Paise(1000)
	)

	withMarkup := rates.EarningsWithMarkup(customerSubtotal, supplierSubtotal, markup)

	// The same order with no markup: the grower charged the whole ₹220.
	without := rates.EarningsWithMarkup(customerSubtotal, customerSubtotal, 0)

	// Our total must rise by the markup MINUS the commission we no longer
	// charge on it — not by the markup on top of an unchanged commission.
	commissionOnMarkup := money.ApplyBPS(markup, rates.SupplierCommissionBPS)
	wantDelta := markup - commissionOnMarkup
	if delta := withMarkup.TotalPaise - without.TotalPaise; delta != wantDelta {
		t.Errorf("markup changed our total by %d, want %d", delta, wantDelta)
	}
}

// A grower's payout must be computed on their own price. This is the arithmetic
// behind SumOrderItemsBySupplier, which subtracts the snapshotted markup before
// commission is applied.
func TestSupplierIsUnaffectedByMarkup(t *testing.T) {
	rates := DefaultRates()

	const (
		supplierSubtotal = money.Paise(21000)
		markup           = money.Paise(1000)
	)

	// What the grower is owed, whether or not we marked the product up.
	payable := rates.SupplierPayableAt(supplierSubtotal, nil)

	// The same figure, reached the way the query does: customer line total
	// minus the markup, then commission.
	customerLine := supplierSubtotal + markup
	viaSubtraction := rates.SupplierPayableAt(customerLine-markup, nil)

	if payable != viaSubtraction {
		t.Errorf("payout via subtraction = %d, want %d", viaSubtraction, payable)
	}
	// And it must be strictly less than what a naive payout on the customer
	// price would hand over — the bug this whole split exists to prevent.
	if naive := rates.SupplierPayableAt(customerLine, nil); naive <= payable {
		t.Fatalf("paying on the customer price (%d) should exceed the correct payout (%d)",
			naive, payable)
	}
}
