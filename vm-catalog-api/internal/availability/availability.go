// Package availability holds the rules that decide whether produce can be
// bought right now.
//
// Every rule here is a pure function of its inputs, with the business day
// passed in rather than read from the clock. That is what makes the day
// boundary testable — and the day boundary is the whole game, because
// "today" for this platform means today in Asia/Kolkata (CLAUDE.md rule 2),
// not today wherever the server happens to be running.
package availability

import (
	"time"

	"github.com/vayal-mikrogreenz/vm-go-common/isttime"
)

// Availability row states.
const (
	StatusOpen   = "open"
	StatusClosed = "closed"
)

// Sheet is one product's declaration for one day.
type Sheet struct {
	// AvailableOn is a DATE. Compared as an IST calendar day, never as an
	// instant.
	AvailableOn time.Time
	// TotalGrams is what the supplier declared.
	TotalGrams int32
	// ReservedGrams is held by unpaid carts (CLAUDE.md §6.3).
	ReservedGrams int32
	// SoldGrams is committed by paid orders.
	SoldGrams int32
	Status    string
}

// RemainingGrams is what can still be sold.
//
// Clamped at zero: a negative remaining would be a broken invariant, and
// letting it flow into a comparison would make an oversold product look
// purchasable again.
func (s Sheet) RemainingGrams() int32 {
	remaining := s.TotalGrams - s.ReservedGrams - s.SoldGrams
	if remaining < 0 {
		return 0
	}
	return remaining
}

// SellableGrams is what a customer could actually buy right now.
//
// Zero once the supplier has closed the product, even though the declared
// total is untouched. Closing stops new sales (CLAUDE.md §5.2), so reporting
// "50 kg left" on a closed sheet describes stock nobody can buy — which is
// what the supplier screen was doing, and it read as though closing had not
// worked.
//
// Kept separate from RemainingGrams rather than folded into it: reopening has
// to restore the declaration intact, so the status-blind number still has to
// exist underneath.
func (s Sheet) SellableGrams() int32 {
	if !s.IsOpen() {
		return 0
	}
	return s.RemainingGrams()
}

// CommittedGrams is what customers already hold — the floor below which a
// supplier may not reduce their declaration.
func (s Sheet) CommittedGrams() int32 { return s.ReservedGrams + s.SoldGrams }

// IsOpen reports whether the supplier is still selling this product.
func (s Sheet) IsOpen() bool { return s.Status == StatusOpen }

// BusinessDay returns the IST calendar day an instant falls on.
//
// This is the ONLY way a "today" should be derived. A client-supplied date is
// never acceptable for a purchasability decision (requirement 4): a browser
// with a wrong clock, or a crafted request, could otherwise buy against
// yesterday's stock.
func BusinessDay(now time.Time) time.Time { return isttime.StartOfDayIST(now) }

// Today returns the current IST business day.
func Today() time.Time { return isttime.Today() }

// SellableOn reports whether this sheet can be sold from on the given
// business day.
//
// Three conditions, all required (CLAUDE.md §5.2):
//   - the declaration is for that exact day — availability never carries over
//   - the supplier has not closed it early
//   - grams remain
func (s Sheet) SellableOn(businessDay time.Time) bool {
	if !isttime.SameDayIST(s.AvailableOn, businessDay) {
		return false
	}
	return s.IsOpen() && s.RemainingGrams() > 0
}

// UnitPurchasable reports whether one pack size can still be bought.
//
// A 5 kg pack is not purchasable from 3 kg of remaining stock even though the
// product itself is not sold out — which is why this is per unit rather than
// a single flag on the product.
func UnitPurchasable(remainingGrams, unitWeightGrams int32) bool {
	if unitWeightGrams <= 0 {
		return false
	}
	return remainingGrams >= unitWeightGrams
}

// Stock hints. Deliberately coarse strings, never numbers.
const (
	// HintFew warns a customer that stock is nearly gone.
	HintFew = "Only a few left"
	// HintNone means say nothing.
	HintNone = ""
)

// lowStockUnits is how many of the smallest pack size counts as "few".
const lowStockUnits = 3

// StockHint returns a coarse availability hint for a customer.
//
// It never reveals the remaining grams. Exact stock is competitor-useful
// intelligence — a rival supplier could watch a product's numbers fall and
// infer daily sales volume — so the API exposes a nudge or nothing at all.
//
// Scaled to the smallest pack size rather than a fixed gram threshold: "a few
// left" means a few PACKS, and 2 kg is nearly sold out for a 1 kg pack while
// being irrelevant for a 100 g one.
// A remainder smaller than the smallest pack says nothing at all. Those grams
// cannot be bought by anyone — the card is already showing as sold out — and
// "Only a few left" over a sold-out card is an invitation to click something
// that is not there.
func StockHint(remainingGrams, smallestUnitGrams int32) string {
	if remainingGrams <= 0 || smallestUnitGrams <= 0 {
		return HintNone
	}
	if remainingGrams < smallestUnitGrams {
		return HintNone
	}
	if remainingGrams <= smallestUnitGrams*lowStockUnits {
		return HintFew
	}
	return HintNone
}

// ReductionError describes an attempt to declare less than customers hold.
type ReductionError struct {
	RequestedGrams int32
	CommittedGrams int32
}

// Error implements error with a message written for the supplier.
func (e ReductionError) Error() string {
	return "cannot reduce below what customers have already reserved or bought"
}

// ValidateNewTotal checks a proposed declaration against committed stock.
//
// Returns a ReductionError when the supplier is trying to declare less than
// customers already hold. The database enforces the same invariant with a
// CHECK constraint; this exists so the supplier gets a sentence they can act
// on rather than a constraint violation.
func ValidateNewTotal(sheet Sheet, requestedTotalGrams int32) error {
	if requestedTotalGrams < sheet.CommittedGrams() {
		return ReductionError{
			RequestedGrams: requestedTotalGrams,
			CommittedGrams: sheet.CommittedGrams(),
		}
	}
	return nil
}
