package pricing

import (
	"testing"

	"github.com/google/uuid"

	"github.com/vayal-mikrogreenz/vm-go-common/money"
)

// The configured defaults from CLAUDE.md §6.2 / §8.
var defaultConfig = Config{PlatformFeeBPS: 300, DeliveryFeePaise: 1500}

func line(price money.Paise, qty int64) Line {
	return Line{SupplierID: uuid.New(), UnitPricePaise: price, Qty: qty}
}

// TestComputeMatchesTheSpecFormula walks the §6.2 arithmetic end to end.
func TestComputeMatchesTheSpecFormula(t *testing.T) {
	tests := []struct {
		name            string
		lines           []Line
		wantSubtotal    money.Paise
		wantPlatformFee money.Paise
		wantDelivery    money.Paise
		wantTotal       money.Paise
	}{
		{
			name:            "single line, clean percentage",
			lines:           []Line{line(10000, 1)}, // Rs.100
			wantSubtotal:    10000,
			wantPlatformFee: 300, // 3.00% of Rs.100 = Rs.3
			wantDelivery:    1500,
			wantTotal:       11800,
		},
		{
			name:            "quantity multiplies the line",
			lines:           []Line{line(12050, 3)}, // Rs.120.50 x 3
			wantSubtotal:    36150,
			wantPlatformFee: 1085, // 36150*300/10000 = 1084.5 -> half-up -> 1085
			wantDelivery:    1500,
			wantTotal:       38735,
		},
		{
			name: "several lines from several suppliers",
			lines: []Line{
				line(12050, 2), // 24100
				line(8500, 1),  //  8500
				line(4000, 3),  // 12000
			},
			wantSubtotal:    44600,
			wantPlatformFee: 1338, // 44600*300/10000 = 1338 exactly
			wantDelivery:    1500,
			wantTotal:       47438,
		},
		{
			// Delivery is not charged on an empty order.
			name:            "empty order",
			lines:           nil,
			wantSubtotal:    0,
			wantPlatformFee: 0,
			wantDelivery:    0,
			wantTotal:       0,
		},
		{
			name:            "one paise subtotal",
			lines:           []Line{line(1, 1)},
			wantSubtotal:    1,
			wantPlatformFee: 0, // 1*300/10000 = 0.03 -> 0
			wantDelivery:    1500,
			wantTotal:       1501,
		},
		{
			name:            "large order",
			lines:           []Line{line(99999999, 1)}, // Rs.999,999.99
			wantSubtotal:    99999999,
			wantPlatformFee: 3000000, // 99999999*300/10000 = 2999999.97 -> 3000000
			wantDelivery:    1500,
			wantTotal:       103001499,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Compute(tc.lines, defaultConfig)

			if got.SubtotalPaise != tc.wantSubtotal {
				t.Errorf("subtotal = %d, want %d", got.SubtotalPaise, tc.wantSubtotal)
			}
			if got.PlatformFeePaise != tc.wantPlatformFee {
				t.Errorf("platform fee = %d, want %d", got.PlatformFeePaise, tc.wantPlatformFee)
			}
			if got.DeliveryFeePaise != tc.wantDelivery {
				t.Errorf("delivery fee = %d, want %d", got.DeliveryFeePaise, tc.wantDelivery)
			}
			if got.TotalPaise != tc.wantTotal {
				t.Errorf("total = %d, want %d", got.TotalPaise, tc.wantTotal)
			}
			// The invariant the orders table also enforces with a CHECK.
			if sum := got.SubtotalPaise + got.PlatformFeePaise + got.DeliveryFeePaise; got.TotalPaise != sum {
				t.Errorf("total %d does not reconcile with its parts (%d)", got.TotalPaise, sum)
			}
		})
	}
}

// TestPlatformFeeRoundingIsHalfUp is the rounding boundary. A truncating
// implementation under-charges by a paise on every order that lands on .5 —
// small individually, and a reconciliation problem at volume.
func TestPlatformFeeRoundingIsHalfUp(t *testing.T) {
	tests := []struct {
		name     string
		subtotal money.Paise
		wantFee  money.Paise
	}{
		// 5050 * 300 = 1,515,000; / 10000 = 151.5 -> half-up -> 152.
		{"exactly half a paise rounds UP", 5050, 152},
		// 1683 * 300 = 504,900; / 10000 = 50.49 -> 50.
		{"just below half rounds down", 1683, 50},
		// 1684 * 300 = 505,200; / 10000 = 50.52 -> 51.
		{"just above half rounds up", 1684, 51},
		// Exact, no rounding involved.
		{"exact multiple", 10000, 300},
		{"zero", 0, 0},
		// 50 * 300 = 15,000; /10000 = 1.5 -> 2.
		{"tiny subtotal landing on half", 50, 2},
		// 150 * 300 = 45,000; /10000 = 4.5 -> 5.
		{"another half case", 150, 5},
		// 16 * 300 = 4800; /10000 = 0.48 -> 0.
		{"rounds away to nothing", 16, 0},
		// 17 * 300 = 5100; /10000 = 0.51 -> 1.
		{"rounds up to one paise", 17, 1},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Compute([]Line{line(tc.subtotal, 1)}, defaultConfig)
			if got.PlatformFeePaise != tc.wantFee {
				t.Errorf("fee on subtotal %d = %d, want %d",
					tc.subtotal, got.PlatformFeePaise, tc.wantFee)
			}
		})
	}
}

// TestFeeRatesAreConfigurable — the rates come from env (CLAUDE.md §8), so a
// change must not need a code edit.
func TestFeeRatesAreConfigurable(t *testing.T) {
	tests := []struct {
		name      string
		cfg       Config
		subtotal  money.Paise
		wantFee   money.Paise
		wantTotal money.Paise
	}{
		{"3 percent", Config{300, 1500}, 10000, 300, 11800},
		{"5 percent", Config{500, 1500}, 10000, 500, 12000},
		{"zero fee", Config{0, 1500}, 10000, 0, 11500},
		{"free delivery", Config{300, 0}, 10000, 300, 10300},
		{"both zero", Config{0, 0}, 10000, 0, 10000},
		// 12.5% expressed exactly, which a float percentage could not do.
		{"fractional percentage via bps", Config{1250, 1500}, 10000, 1250, 12750},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Compute([]Line{line(tc.subtotal, 1)}, tc.cfg)
			if got.PlatformFeePaise != tc.wantFee {
				t.Errorf("fee = %d, want %d", got.PlatformFeePaise, tc.wantFee)
			}
			if got.TotalPaise != tc.wantTotal {
				t.Errorf("total = %d, want %d", got.TotalPaise, tc.wantTotal)
			}
		})
	}
}

func TestLineTotals(t *testing.T) {
	lines := []Line{line(12050, 2), line(8500, 1), line(4000, 3)}
	got := Compute(lines, defaultConfig)

	want := []money.Paise{24100, 8500, 12000}
	if len(got.LineTotals) != len(want) {
		t.Fatalf("got %d line totals, want %d", len(got.LineTotals), len(want))
	}
	for i := range want {
		if got.LineTotals[i] != want[i] {
			t.Errorf("line %d total = %d, want %d", i, got.LineTotals[i], want[i])
		}
	}
}

// TestSupplierPayableExcludesOurFees — suppliers are paid their listed prices
// in full; the platform and delivery fees are ours and sit on top.
func TestSupplierPayableExcludesOurFees(t *testing.T) {
	supplierA, supplierB := uuid.New(), uuid.New()

	lines := []Line{
		{SupplierID: supplierA, UnitPricePaise: 12050, Qty: 2}, // 24100
		{SupplierID: supplierB, UnitPricePaise: 8500, Qty: 1},  //  8500
		{SupplierID: supplierA, UnitPricePaise: 4000, Qty: 3},  // 12000
	}

	if got, want := SupplierPayable(lines, supplierA), money.Paise(36100); got != want {
		t.Errorf("supplier A payable = %d, want %d", got, want)
	}
	if got, want := SupplierPayable(lines, supplierB), money.Paise(8500); got != want {
		t.Errorf("supplier B payable = %d, want %d", got, want)
	}
	if got := SupplierPayable(lines, uuid.New()); got != 0 {
		t.Errorf("unrelated supplier payable = %d, want 0", got)
	}

	// The suppliers' shares must add up to exactly the subtotal — no more, no
	// less. Our fees are additional, not deducted.
	breakdown := Compute(lines, defaultConfig)
	total := SupplierPayable(lines, supplierA) + SupplierPayable(lines, supplierB)
	if total != breakdown.SubtotalPaise {
		t.Errorf("supplier payables sum to %d, want the subtotal %d",
			total, breakdown.SubtotalPaise)
	}
	if breakdown.TotalPaise <= total {
		t.Error("customer total should exceed the suppliers' share by our fees")
	}
}

func TestPayableBySupplier(t *testing.T) {
	supplierA, supplierB := uuid.New(), uuid.New()
	lines := []Line{
		{SupplierID: supplierA, UnitPricePaise: 10000, Qty: 1},
		{SupplierID: supplierB, UnitPricePaise: 5000, Qty: 2},
		{SupplierID: supplierA, UnitPricePaise: 2500, Qty: 4},
	}

	payable := PayableBySupplier(lines)
	if len(payable) != 2 {
		t.Fatalf("got %d suppliers, want 2", len(payable))
	}
	if got, want := payable[supplierA], money.Paise(20000); got != want {
		t.Errorf("supplier A = %d, want %d", got, want)
	}
	if got, want := payable[supplierB], money.Paise(10000); got != want {
		t.Errorf("supplier B = %d, want %d", got, want)
	}
}

// TestNoFloatDrift prices a hundred awkward lines and checks the total against
// an independently accumulated integer sum. A float implementation drifts here.
func TestNoFloatDrift(t *testing.T) {
	var lines []Line
	var expectedSubtotal money.Paise

	// 10.10, 20.20, ... none of which are exactly representable as float64.
	for i := 1; i <= 100; i++ {
		price := money.Paise(i * 1010)
		lines = append(lines, line(price, 3))
		expectedSubtotal += price * 3
	}

	got := Compute(lines, defaultConfig)
	if got.SubtotalPaise != expectedSubtotal {
		t.Errorf("subtotal = %d, want %d — arithmetic drifted",
			got.SubtotalPaise, expectedSubtotal)
	}
	if got.TotalPaise != got.SubtotalPaise+got.PlatformFeePaise+got.DeliveryFeePaise {
		t.Error("total does not reconcile with its parts")
	}
}
