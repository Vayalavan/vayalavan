// Package pricing turns a grower's price into the price a customer pays.
//
// There are two prices for every pack on this platform and they must never be
// confused:
//
//	supplier price  what the grower set and what they are paid on
//	customer price  supplier price + the admin's markup for that product
//
// The markup is a PERCENTAGE of the supplier price, in basis points — the same
// unit as every other rate here (3000 = 30.00%). One rate per product, applied
// to each pack in proportion:
//
//	5 kg   ₹1,000 + 30%  →  ₹1,300
//	10 kg  ₹2,000 + 30%  →  ₹2,600
//
// A flat amount was the first version of this and priced both packs the same
// ₹300, which is 30% of one and 15% of the other. A rate holds its shape as
// pack sizes change, and as growers change their prices underneath it.
//
// One function, used by every path that shows or charges a customer price, so
// a new endpoint cannot quietly serve produce at the supplier's price. The
// supplier's own screens call none of this — they show what the grower set.
package pricing

import "github.com/vayal-mikrogreenz/vm-go-common/money"

// MarkupPaise is what we add to one pack at the given rate.
//
// Integer arithmetic, rounded half-up — the same rule as the platform fee and
// the supplier commission, so two rates applied to the same amount can never
// disagree by a paise (CLAUDE.md §6.2). No float is involved at any point.
//
// A non-positive rate adds nothing. The column is CHECKed to 0–10000, so an
// out-of-range value cannot come from the database; it is clamped here anyway
// rather than discounting produce below what a grower is owed.
func MarkupPaise(supplierPricePaise int64, markupBPS int32) int64 {
	if markupBPS <= 0 || supplierPricePaise <= 0 {
		return 0
	}
	rate := int64(markupBPS)
	if rate > 10000 {
		rate = 10000
	}
	return int64(money.ApplyBPS(money.Paise(supplierPricePaise), rate))
}

// CustomerPaise is what a customer pays for one pack.
func CustomerPaise(supplierPricePaise int64, markupBPS int32) int64 {
	return supplierPricePaise + MarkupPaise(supplierPricePaise, markupBPS)
}

// FormatBPS renders a rate the way it is written on a screen: "30%", "2.5%".
//
// Trailing zeroes are dropped, because "30.00%" reads as a precision nobody
// entered. Kept here beside the arithmetic so the label and the maths cannot
// come to disagree about what a rate means.
func FormatBPS(bps int32) string {
	return money.FormatBPS(int64(bps))
}
