package money

import (
	"errors"
	"testing"
)

func TestRoundHalfUp(t *testing.T) {
	tests := []struct {
		name        string
		numerator   int64
		denominator int64
		want        int64
	}{
		{"exact division", 1000, 10, 100},
		{"below half rounds down", 104, 10, 10},
		{"exactly half rounds up", 105, 10, 11},
		{"above half rounds up", 106, 10, 11},
		{"zero", 0, 10, 0},
		{"negative below half", -104, 10, -10},
		{"negative exactly half rounds away from zero", -105, 10, -11},
		{"negative above half", -106, 10, -11},
		{"denominator of one is identity", 12345, 1, 12345},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := RoundHalfUp(tc.numerator, tc.denominator)
			if got != tc.want {
				t.Errorf("RoundHalfUp(%d, %d) = %d, want %d",
					tc.numerator, tc.denominator, got, tc.want)
			}
		})
	}
}

func TestRoundHalfUpPanicsOnNonPositiveDenominator(t *testing.T) {
	for _, denominator := range []int64{0, -10} {
		t.Run("denominator", func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Errorf("RoundHalfUp(1, %d) did not panic", denominator)
				}
			}()
			RoundHalfUp(1, denominator)
		})
	}
}

// TestApplyBPS covers the platform fee path from CLAUDE.md §6.2, including
// the subtotals that land on exactly half a paise — the case where a naive
// truncating implementation silently under-charges.
func TestApplyBPS(t *testing.T) {
	const platformFeeBPS = 300 // 3.00%, the configured default

	tests := []struct {
		name     string
		subtotal Paise
		bps      int64
		want     Paise
	}{
		{"zero subtotal", 0, platformFeeBPS, 0},
		// 10000 * 300 / 10000 = 300 exactly.
		{"clean 3 percent of Rs.100", 10000, platformFeeBPS, 300},
		// 15000 * 300 / 10000 = 450 exactly.
		{"clean 3 percent of Rs.150", 15000, platformFeeBPS, 450},
		// 5050 * 300 = 1515000; /10000 = 151.5 -> half-up -> 152.
		{"exactly half a paise rounds up", 5050, platformFeeBPS, 152},
		// 1683 * 300 = 504900; /10000 = 50.49 -> 50.
		{"below half rounds down", 1683, platformFeeBPS, 50},
		// 1684 * 300 = 505200; /10000 = 50.52 -> 51.
		{"above half rounds up", 1684, platformFeeBPS, 51},
		{"zero rate yields zero", 99999, 0, 0},
		{"full 100 percent returns the amount", 12345, 10000, 12345},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ApplyBPS(tc.subtotal, tc.bps); got != tc.want {
				t.Errorf("ApplyBPS(%d, %d) = %d, want %d",
					tc.subtotal, tc.bps, got, tc.want)
			}
		})
	}
}

// TestOrderTotal exercises the full checkout arithmetic from CLAUDE.md §6.2
// end to end, so a change to any single step is caught by a failing total.
func TestOrderTotal(t *testing.T) {
	const (
		platformFeeBPS   = 300
		deliveryFeePaise = 1500
	)

	// Three lines: 2 x Rs.120.50, 1 x Rs.85, 3 x Rs.40.
	lines := []struct {
		unitPrice Paise
		qty       int64
	}{
		{12050, 2},
		{8500, 1},
		{4000, 3},
	}

	var subtotal Paise
	for _, l := range lines {
		subtotal = subtotal.Add(l.unitPrice.Mul(l.qty))
	}

	const wantSubtotal Paise = 24100 + 8500 + 12000 // 44600
	if subtotal != wantSubtotal {
		t.Fatalf("subtotal = %d, want %d", subtotal, wantSubtotal)
	}

	// 44600 * 300 / 10000 = 1338 exactly.
	platformFee := ApplyBPS(subtotal, platformFeeBPS)
	if want := Paise(1338); platformFee != want {
		t.Errorf("platformFee = %d, want %d", platformFee, want)
	}

	total := Sum(subtotal, platformFee, Paise(deliveryFeePaise))
	if want := Paise(44600 + 1338 + 1500); total != want {
		t.Errorf("total = %d, want %d", total, want)
	}
}

func TestSum(t *testing.T) {
	if got := Sum(); got != 0 {
		t.Errorf("Sum() of nothing = %d, want 0", got)
	}
	if got := Sum(100, 250, 3); got != 353 {
		t.Errorf("Sum(100, 250, 3) = %d, want 353", got)
	}
	if got := Sum(500, -200); got != 300 {
		t.Errorf("Sum(500, -200) = %d, want 300", got)
	}
}

func TestMul(t *testing.T) {
	if got := Paise(12050).Mul(3); got != 36150 {
		t.Errorf("Paise(12050).Mul(3) = %d, want 36150", got)
	}
	if got := Paise(12050).Mul(0); got != 0 {
		t.Errorf("Paise(12050).Mul(0) = %d, want 0", got)
	}
}

func TestParseRupees(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  Paise
	}{
		{"whole number", "150", 15000},
		{"two decimals", "149.50", 14950},
		{"one decimal pads to two", "149.5", 14950},
		{"zero", "0", 0},
		{"zero with decimals", "0.00", 0},
		{"sub-rupee", "0.99", 99},
		{"leading dot shorthand", ".50", 50},
		{"surrounding whitespace", "  75.25  ", 7525},
		{"rupee symbol", "₹99.99", 9999},
		{"thousands separators", "1,23,456.78", 12345678},
		{"explicit plus", "+10.00", 1000},
		{"negative", "-10.50", -1050},
		// The classic float trap: 0.1 + 0.2 != 0.3 in binary floating point.
		// Parsing as integers makes this exact.
		{"value unrepresentable as float64", "0.10", 10},
		{"large amount", "9999999.99", 999999999},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseRupees(tc.input)
			if err != nil {
				t.Fatalf("ParseRupees(%q) returned error: %v", tc.input, err)
			}
			if got != tc.want {
				t.Errorf("ParseRupees(%q) = %d, want %d", tc.input, got, tc.want)
			}
		})
	}
}

func TestParseRupeesRejectsInvalid(t *testing.T) {
	invalid := []struct {
		name  string
		input string
	}{
		{"empty", ""},
		{"whitespace only", "   "},
		{"three decimal places", "10.001"},
		{"not a number", "abc"},
		{"trailing garbage", "10.00x"},
		{"embedded letters", "1a.00"},
		{"bare dot", "."},
		{"two dots", "1.2.3"},
		{"trailing decimal point", "1."},
		{"lone minus", "-"},
		{"lone plus", "+"},
		// The following all used to parse to plausible-but-wrong amounts,
		// because validation was delegated to ParseInt, which accepts a
		// leading sign. A supplier CSV containing any of these must be
		// rejected outright rather than priced at a number nobody entered.
		{"sign inside the fraction", "1.-5"},  // parsed as 95 paise
		{"plus inside the fraction", "1.+5"},  // parsed as 105 paise
		{"double negative", "--5"},            // parsed as +500 paise
		{"sign inside the whole part", "1-5"}, // ParseInt rejected, pinned here
		{"space inside the number", "1 5.00"},
	}

	for _, tc := range invalid {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseRupees(tc.input)
			if err == nil {
				t.Fatalf("ParseRupees(%q) = %d, want an error", tc.input, got)
			}
			if !errors.Is(err, ErrInvalidRupees) {
				t.Errorf("ParseRupees(%q) error = %v, want it to wrap ErrInvalidRupees",
					tc.input, err)
			}
		})
	}
}

// TestParseRupeesFormatRupeesRoundTrip guards the boundary in both
// directions: whatever the CSV importer accepts must render back identically.
func TestParseRupeesFormatRupeesRoundTrip(t *testing.T) {
	for _, input := range []string{"0.00", "1.00", "99.99", "1,234.50", "12,34,567.89"} {
		t.Run(input, func(t *testing.T) {
			parsed, err := ParseRupees(input)
			if err != nil {
				t.Fatalf("ParseRupees(%q) returned error: %v", input, err)
			}
			if got, want := FormatRupees(parsed), "₹"+input; got != want {
				t.Errorf("round trip of %q = %q, want %q", input, got, want)
			}
		})
	}
}

func TestFormatRupees(t *testing.T) {
	tests := []struct {
		name  string
		input Paise
		want  string
	}{
		{"zero", 0, "₹0.00"},
		{"paise only", 5, "₹0.05"},
		{"tens of paise", 50, "₹0.50"},
		{"one rupee", 100, "₹1.00"},
		{"rupees and paise", 14950, "₹149.50"},
		{"three digits", 99999, "₹999.99"},
		// Indian grouping starts here: 1000 rupees is 1,000 not 1000.
		{"one thousand", 100000, "₹1,000.00"},
		{"ten thousand", 1000000, "₹10,000.00"},
		// One lakh groups as 1,00,000 — not the Western 100,000.
		{"one lakh", 10000000, "₹1,00,000.00"},
		{"ten lakh", 100000000, "₹10,00,000.00"},
		// One crore groups as 1,00,00,000.
		{"one crore", 1000000000, "₹1,00,00,000.00"},
		{"mixed lakhs", 123456789, "₹12,34,567.89"},
		{"negative", -14950, "-₹149.50"},
		{"negative sub-rupee", -5, "-₹0.05"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := FormatRupees(tc.input); got != tc.want {
				t.Errorf("FormatRupees(%d) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestFromRupees(t *testing.T) {
	if got := FromRupees(150); got != 15000 {
		t.Errorf("FromRupees(150) = %d, want 15000", got)
	}
	if got := FromRupees(0); got != 0 {
		t.Errorf("FromRupees(0) = %d, want 0", got)
	}
}
