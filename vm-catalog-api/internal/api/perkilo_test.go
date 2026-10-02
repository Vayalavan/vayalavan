package api

import "testing"

// TestPerKilo pins the comparison figure shown on the product detail page.
//
// Integer paise throughout (CLAUDE.md rule 1). It is a shopping aid only —
// nothing is ever priced from it — but a wrong per-kilo figure makes the
// bigger pack look like the worse deal, which is the one job it has.
func TestPerKilo(t *testing.T) {
	tests := []struct {
		name        string
		pricePaise  int64
		weightGrams int32
		want        string
	}{
		{"exactly one kilo passes through", 13000, 1000, "₹130.00/kg"},
		{"three kilo pack", 36000, 3000, "₹120.00/kg"},
		{"five kilo pack is cheaper per kilo", 55000, 5000, "₹110.00/kg"},
		{"half kilo pack doubles", 7500, 500, "₹150.00/kg"},
		{"250g pack quadruples", 2500, 250, "₹100.00/kg"},
		// 10000*1000/750 = 13333.33 -> half-up 13333
		{"rounds half up, not down", 10000, 750, "₹133.33/kg"},
		// 1000*1000/300 = 3333.33
		{"repeating division still rounds", 1000, 300, "₹33.33/kg"},
		// 5*1000/3 = 1666.67 -> 1667
		{"tiny amounts round up correctly", 5, 3, "₹16.67/kg"},
		{"free produce is still zero", 0, 1000, "₹0.00/kg"},
		// Never divide by zero, and never render a nonsense figure.
		{"zero weight yields nothing", 13000, 0, ""},
		{"negative weight yields nothing", 13000, -5, ""},
		{"large pack keeps precision", 250000, 20000, "₹125.00/kg"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := perKilo(tc.pricePaise, tc.weightGrams); got != tc.want {
				t.Errorf("perKilo(%d, %d) = %q, want %q",
					tc.pricePaise, tc.weightGrams, got, tc.want)
			}
		})
	}
}
