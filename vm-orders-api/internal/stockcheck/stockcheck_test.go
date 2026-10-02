package stockcheck

import (
	"testing"

	"github.com/google/uuid"
)

// The ids below are pool keys — SIZE CODES, not products. In the single-grade
// cases `tomato` and `okra` stand for each product's one implicit grade, which
// is what a pre-grade listing carries.
var (
	tomato = uuid.MustParse("11111111-1111-1111-1111-111111111111")
	okra   = uuid.MustParse("22222222-2222-2222-2222-222222222222")

	// Two real grades of ONE product, for the cases that prove the pools are
	// separate.
	tomatoM  = uuid.MustParse("33333333-3333-3333-3333-333333333333")
	tomatoXL = uuid.MustParse("44444444-4444-4444-4444-444444444444")
)

func TestCheck(t *testing.T) {
	tests := []struct {
		name      string
		lines     []Line
		remaining map[uuid.UUID]int32
		// wantMax and wantExceeds are positional, matching lines.
		wantMax     []int32
		wantExceeds []int32
		wantShort   []Shortfall
	}{
		{
			// The case the storefront could not see: one pack fits, so the card
			// and the cart both said "purchasable", and three of them did not.
			name:        "quantity exceeds what one pack's purchasability implies",
			lines:       []Line{{SizeCodeID: tomato, WeightGrams: 5000, Qty: 3}},
			remaining:   map[uuid.UUID]int32{tomato: 5000},
			wantMax:     []int32{1},
			wantExceeds: []int32{2},
			wantShort: []Shortfall{
				{SizeCodeID: tomato, RequestedGrams: 15000, RemainingGrams: 5000},
			},
		},
		{
			name:        "exactly the remaining stock fits",
			lines:       []Line{{SizeCodeID: tomato, WeightGrams: 5000, Qty: 2}},
			remaining:   map[uuid.UUID]int32{tomato: 10000},
			wantMax:     []int32{2},
			wantExceeds: []int32{0},
			wantShort:   nil,
		},
		{
			// The ceiling is what is left, not what was asked for — a stepper
			// reading MaxQty must still be able to go up.
			name:        "max qty reports headroom, not the current quantity",
			lines:       []Line{{SizeCodeID: tomato, WeightGrams: 1000, Qty: 2}},
			remaining:   map[uuid.UUID]int32{tomato: 10000},
			wantMax:     []int32{10},
			wantExceeds: []int32{0},
			wantShort:   nil,
		},
		{
			// Two pack sizes of one product compete for one pool of grams. The
			// first line takes what it needs; the second sees the rest.
			name: "two pack sizes of the same product share the grams",
			lines: []Line{
				{SizeCodeID: tomato, WeightGrams: 5000, Qty: 1},
				{SizeCodeID: tomato, WeightGrams: 1000, Qty: 4},
			},
			remaining:   map[uuid.UUID]int32{tomato: 7000},
			wantMax:     []int32{1, 2},
			wantExceeds: []int32{0, 2},
			wantShort: []Shortfall{
				{SizeCodeID: tomato, RequestedGrams: 9000, RemainingGrams: 7000},
			},
		},
		{
			// The apportioned ceilings must never add up to more than exists:
			// two lines each told "2" would oversell by 2 kg.
			name: "apportioned ceilings never oversell",
			lines: []Line{
				{SizeCodeID: tomato, WeightGrams: 1000, Qty: 2},
				{SizeCodeID: tomato, WeightGrams: 1000, Qty: 2},
			},
			remaining:   map[uuid.UUID]int32{tomato: 3000},
			wantMax:     []int32{3, 1},
			wantExceeds: []int32{0, 1},
			wantShort: []Shortfall{
				{SizeCodeID: tomato, RequestedGrams: 4000, RemainingGrams: 3000},
			},
		},
		{
			name: "one product short does not implicate another",
			lines: []Line{
				{SizeCodeID: tomato, WeightGrams: 1000, Qty: 5},
				{SizeCodeID: okra, WeightGrams: 500, Qty: 2},
			},
			remaining:   map[uuid.UUID]int32{tomato: 2000, okra: 10000},
			wantMax:     []int32{2, 20},
			wantExceeds: []int32{3, 0},
			wantShort: []Shortfall{
				{SizeCodeID: tomato, RequestedGrams: 5000, RemainingGrams: 2000},
			},
		},
		{
			// No availability row today at all: absent from the map, not zero.
			name:        "a product with no sheet today is short by everything",
			lines:       []Line{{SizeCodeID: tomato, WeightGrams: 1000, Qty: 2}},
			remaining:   map[uuid.UUID]int32{},
			wantMax:     []int32{0},
			wantExceeds: []int32{2},
			wantShort: []Shortfall{
				{SizeCodeID: tomato, RequestedGrams: 2000, RemainingGrams: 0},
			},
		},
		{
			// The property size codes exist for. Two GRADES of one produce are
			// separate crates: a cart holding both must not have the M eat
			// into the XL's grams, and a shortfall in one must not implicate
			// the other. Under the flat model these two lines shared a pool
			// and this cart would have been reported as over-committed.
			name: "two grades of one product do not share grams",
			lines: []Line{
				{SizeCodeID: tomatoM, ProductID: tomato, WeightGrams: 1000, Qty: 3},
				{SizeCodeID: tomatoXL, ProductID: tomato, WeightGrams: 1000, Qty: 3},
			},
			remaining:   map[uuid.UUID]int32{tomatoM: 3000, tomatoXL: 3000},
			wantMax:     []int32{3, 3},
			wantExceeds: []int32{0, 0},
			wantShort:   nil,
		},
		{
			// And when one grade IS short, only that grade is named — the
			// customer has to know which of the two lines to change.
			name: "a short grade does not implicate its sibling",
			lines: []Line{
				{SizeCodeID: tomatoM, ProductID: tomato, WeightGrams: 1000, Qty: 5},
				{SizeCodeID: tomatoXL, ProductID: tomato, WeightGrams: 1000, Qty: 2},
			},
			remaining:   map[uuid.UUID]int32{tomatoM: 2000, tomatoXL: 10000},
			wantMax:     []int32{2, 10},
			wantExceeds: []int32{3, 0},
			wantShort: []Shortfall{
				{SizeCodeID: tomatoM, ProductID: tomato, RequestedGrams: 5000, RemainingGrams: 2000},
			},
		},
		{
			// Bad data, not a real listing. It must not divide by zero and must
			// not block a checkout on its own.
			name:        "a weightless pack consumes nothing",
			lines:       []Line{{SizeCodeID: tomato, WeightGrams: 0, Qty: 3}},
			remaining:   map[uuid.UUID]int32{tomato: 1000},
			wantMax:     []int32{3},
			wantExceeds: []int32{0},
			wantShort:   nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Check(tc.lines, tc.remaining)

			if len(got.Lines) != len(tc.lines) {
				t.Fatalf("verdicts = %d, want %d", len(got.Lines), len(tc.lines))
			}
			for i, verdict := range got.Lines {
				if verdict.MaxQty != tc.wantMax[i] {
					t.Errorf("line %d MaxQty = %d, want %d", i, verdict.MaxQty, tc.wantMax[i])
				}
				if verdict.ExceedsBy != tc.wantExceeds[i] {
					t.Errorf("line %d ExceedsBy = %d, want %d",
						i, verdict.ExceedsBy, tc.wantExceeds[i])
				}
			}

			if len(got.Shortfalls) != len(tc.wantShort) {
				t.Fatalf("shortfalls = %+v, want %+v", got.Shortfalls, tc.wantShort)
			}
			for i, short := range got.Shortfalls {
				if short != tc.wantShort[i] {
					t.Errorf("shortfall %d = %+v, want %+v", i, short, tc.wantShort[i])
				}
			}

			if got.Fits() != (len(tc.wantShort) == 0) {
				t.Errorf("Fits() = %v, want %v", got.Fits(), len(tc.wantShort) == 0)
			}
		})
	}
}

// The ceilings handed to the UI must never, in total, exceed what exists —
// otherwise following every "you may have up to N" still oversells.
func TestApportionedCeilingsAreSafe(t *testing.T) {
	lines := []Line{
		{SizeCodeID: tomato, WeightGrams: 1000, Qty: 1},
		{SizeCodeID: tomato, WeightGrams: 2000, Qty: 1},
		{SizeCodeID: tomato, WeightGrams: 500, Qty: 1},
	}
	const available = 6000

	got := Check(lines, map[uuid.UUID]int32{tomato: available})

	// Each line taken to its own ceiling, in order, consuming as it goes.
	left := int32(available)
	for i, line := range lines {
		ceiling := got.Lines[i].MaxQty
		if ceiling*line.WeightGrams > left {
			t.Fatalf("line %d ceiling %d packs of %d g exceeds the %d g still free",
				i, ceiling, line.WeightGrams, left)
		}
		left -= line.Qty * line.WeightGrams
	}
}
