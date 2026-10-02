// Package stockcheck answers one question about a cart: does what the customer
// has chosen still fit in what is left today?
//
// It exists because "can this pack be bought at all" and "can this cart be
// bought" are different questions, and the storefront only ever answered the
// first. A card is purchasable when ONE pack fits in the remaining grams
// (CLAUDE.md §5.2), so three 5 kg packs sat happily in a cart with 5 kg left,
// the checkout button stayed enabled, and the customer found out at the very
// end — from "there is not enough stock left for one of these items", which
// does not even say which one.
//
// Two things make this more than a subtraction:
//
//   - Availability is per PRODUCT, in grams, while a cart holds PACKS. A cart
//     can hold a 1 kg and a 5 kg pack of the same tomatoes, and they compete
//     for the same grams.
//   - Telling someone "this does not fit" is useless without saying how much
//     does. Every line therefore gets a MaxQty, apportioned in cart order so
//     the numbers across lines of one product add up to no more than exists.
//     Two lines each told "you may keep 2" would together still oversell.
//
// Advisory only. Overselling is prevented by the locked re-check in
// vm-catalog-api at reservation time (CLAUDE.md §6.3); this runs unlocked on
// every cart read, so its numbers can be a few seconds stale. Its job is to
// move the bad news from the last screen to the cart, and to name the produce.
package stockcheck

import "github.com/google/uuid"

// Line is one cart line, in cart order.
type Line struct {
	// SizeCodeID is the pool this line draws on. The GRADE, not the product:
	// M and XL of one produce are separate crates, so a cart holding both
	// competes against neither (CLAUDE.md §5.2).
	SizeCodeID uuid.UUID
	// ProductID is carried for reporting only — it names the produce in a
	// shortfall and never keys a pool.
	ProductID uuid.UUID
	// WeightGrams is the weight of ONE pack.
	WeightGrams int32
	Qty         int32
}

// LineVerdict is the outcome for one line, positionally matching the input.
type LineVerdict struct {
	// MaxQty is the CEILING for this line: how many packs it could hold, once
	// any earlier line of the same product has taken its share. Deliberately
	// not capped at the quantity asked for — a stepper needs to know how far
	// it may still go, and a MaxQty that always equalled Qty would disable the
	// + button on every line in the cart.
	MaxQty int32
	// ExceedsBy is how many packs must go for the line to fit. Zero when it
	// already does.
	ExceedsBy int32
}

// Fits reports whether the line can be bought as it stands.
func (v LineVerdict) Fits() bool { return v.ExceedsBy == 0 }

// Shortfall is a GRADE the cart asks too much of.
type Shortfall struct {
	SizeCodeID     uuid.UUID
	ProductID      uuid.UUID
	RequestedGrams int32
	RemainingGrams int32
}

// ShortBy is how many grams the cart is over by.
func (s Shortfall) ShortBy() int32 { return s.RequestedGrams - s.RemainingGrams }

// Result is the verdict for a whole cart.
type Result struct {
	// Lines matches the input slice position for position.
	Lines []LineVerdict
	// Shortfalls lists each over-committed product once, in the order it first
	// appears in the cart, so the message a customer reads is stable between
	// reads of the same cart.
	Shortfalls []Shortfall
}

// Fits reports whether the whole cart can be bought.
func (r Result) Fits() bool { return len(r.Shortfalls) == 0 }

// Check apportions today's remaining grams across the cart.
//
// remaining is keyed by product id; a product missing from the map has no
// availability today and every line of it is short by its whole quantity.
func Check(lines []Line, remaining map[uuid.UUID]int32) Result {
	result := Result{Lines: make([]LineVerdict, len(lines))}

	// Grams still unclaimed as we walk the cart, seeded from what exists.
	left := make(map[uuid.UUID]int32, len(remaining))
	requested := make(map[uuid.UUID]int32, len(lines))
	// First-appearance order, so the reported shortfalls do not reorder
	// themselves between two reads of an unchanged cart.
	order := make([]uuid.UUID, 0, len(lines))
	// Which produce each grade belongs to, so a shortfall can be named.
	productBySizeCode := make(map[uuid.UUID]uuid.UUID, len(lines))

	for _, line := range lines {
		if _, seen := left[line.SizeCodeID]; !seen {
			left[line.SizeCodeID] = remaining[line.SizeCodeID]
			productBySizeCode[line.SizeCodeID] = line.ProductID
			order = append(order, line.SizeCodeID)
		}
	}

	for i, line := range lines {
		if line.Qty <= 0 {
			continue
		}
		// A pack with no weight consumes no stock. Bad data rather than a real
		// listing, but treating it as free is the harmless reading — the
		// alternative divides by zero. It neither blocks checkout nor grows.
		if line.WeightGrams <= 0 {
			result.Lines[i] = LineVerdict{MaxQty: line.Qty}
			continue
		}

		requested[line.SizeCodeID] += line.WeightGrams * line.Qty

		// How many packs the grams still unclaimed would buy. This is the
		// ceiling reported to the caller; only what the line actually takes is
		// subtracted, so a later line of the same product sees the rest.
		affordable := left[line.SizeCodeID] / line.WeightGrams
		if affordable < 0 {
			affordable = 0
		}
		taken := line.Qty
		if affordable < taken {
			taken = affordable
		}
		left[line.SizeCodeID] -= taken * line.WeightGrams

		result.Lines[i] = LineVerdict{MaxQty: affordable, ExceedsBy: line.Qty - taken}
	}

	for _, sizeCodeID := range order {
		want := requested[sizeCodeID]
		have := remaining[sizeCodeID]
		if want > have {
			result.Shortfalls = append(result.Shortfalls, Shortfall{
				SizeCodeID:     sizeCodeID,
				ProductID:      productBySizeCode[sizeCodeID],
				RequestedGrams: want,
				RemainingGrams: have,
			})
		}
	}

	return result
}
