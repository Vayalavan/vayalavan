package pricing

import "testing"

func TestCustomerPaise(t *testing.T) {
	tests := []struct {
		name     string
		supplier int64
		bps      int32
		want     int64
	}{
		// The rule, in the shape it was asked for: 30% on a ₹1,000 pack and on
		// a ₹2,000 pack of the same produce.
		{"30% on a 5 kg pack", 100000, 3000, 130000},
		{"30% on the 10 kg pack of the same produce", 200000, 3000, 260000},

		{"no markup leaves the price alone", 21000, 0, 21000},
		{"a fractional rate", 21000, 250, 21525}, // 2.5% of ₹210 = ₹5.25
		{"100% doubles the price", 50000, 10000, 100000},
		{"a free product stays free", 0, 3000, 0},

		// The column is CHECKed to 0–10000, so neither of these can come from
		// the database — but a negative would discount produce below what the
		// grower is owed, and an over-range one would price it into orbit.
		{"a negative rate is ignored, never a discount", 21000, -500, 21000},
		{"an over-range rate is clamped at 100%", 50000, 25000, 100000},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := CustomerPaise(tc.supplier, tc.bps); got != tc.want {
				t.Errorf("CustomerPaise(%d, %d bps) = %d, want %d",
					tc.supplier, tc.bps, got, tc.want)
			}
		})
	}
}

// Rounding is half-up in integer paise, the same rule as the platform fee and
// the supplier commission — two rates on one amount must never disagree by a
// paise, and no float may appear anywhere near a price (CLAUDE.md rule 1).
func TestMarkupRoundsHalfUp(t *testing.T) {
	tests := []struct {
		supplier int64
		bps      int32
		want     int64
	}{
		// 3% of 4550 = 136.5 paise, which must round UP to 137.
		{4550, 300, 137},
		// 3% of 4549 = 136.47, rounds down.
		{4549, 300, 136},
		// 1% of 50 = 0.5, rounds up to 1.
		{50, 100, 1},
		// 1% of 49 = 0.49, rounds down to 0 — a markup can legitimately come
		// to nothing on a small enough price.
		{49, 100, 0},
	}

	for _, tc := range tests {
		if got := MarkupPaise(tc.supplier, tc.bps); got != tc.want {
			t.Errorf("MarkupPaise(%d, %d bps) = %d, want %d",
				tc.supplier, tc.bps, got, tc.want)
		}
	}
}

// The whole reason for the change from a flat amount: the markup must scale
// with the pack. If this fails, someone has made it flat again and every large
// pack is being sold at a fraction of the intended margin.
func TestMarkupScalesWithPrice(t *testing.T) {
	const rate int32 = 3000 // 30%

	small := MarkupPaise(100000, rate) // ₹1,000
	large := MarkupPaise(200000, rate) // ₹2,000

	if small != 30000 {
		t.Errorf("markup on the ₹1,000 pack = %d, want 30000", small)
	}
	if large != 60000 {
		t.Errorf("markup on the ₹2,000 pack = %d, want 60000", large)
	}
	if large != small*2 {
		t.Errorf("a pack at twice the price took %d, want %d — the rate is not scaling",
			large, small*2)
	}
}

func TestFormatBPS(t *testing.T) {
	tests := []struct {
		bps  int32
		want string
	}{
		{0, "0%"},
		{300, "3%"},
		{3000, "30%"},
		{250, "2.5%"},
		{1025, "10.25%"},
		{10000, "100%"},
	}

	for _, tc := range tests {
		if got := FormatBPS(tc.bps); got != tc.want {
			t.Errorf("FormatBPS(%d) = %q, want %q", tc.bps, got, tc.want)
		}
	}
}
