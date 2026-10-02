// Package pricing computes order totals — CLAUDE.md §6.2.
//
// Every value is int64 paise and every operation is integer arithmetic
// (CLAUDE.md rule 1). No float appears anywhere in this package, because a
// float rupee amount drifts once it has been through enough arithmetic, and
// for a ledger that means totals that do not reconcile.
package pricing

import (
	"github.com/google/uuid"

	"github.com/vayal-mikrogreenz/vm-go-common/money"
)

// Config holds the env-driven rates (CLAUDE.md §8).
type Config struct {
	// PlatformFeeBPS is the platform fee in basis points. 300 = 3.00%.
	// Basis points keep the rate an exact integer, so no float ever touches a
	// fee calculation.
	PlatformFeeBPS int64
	// DeliveryFeePaise is the flat delivery charge. 1500 = Rs. 15.00.
	DeliveryFeePaise money.Paise
}

// Line is one order line before pricing.
type Line struct {
	SupplierID uuid.UUID
	// UnitPricePaise is the price of the WHOLE pack, not per kg.
	UnitPricePaise money.Paise
	Qty            int64
}

// LineTotal is the price of this line: unit price times quantity.
func (l Line) LineTotal() money.Paise { return l.UnitPricePaise.Mul(l.Qty) }

// Breakdown is what the customer sees at checkout, and what is stored on the
// order row.
type Breakdown struct {
	SubtotalPaise    money.Paise
	PlatformFeePaise money.Paise
	DeliveryFeePaise money.Paise
	TotalPaise       money.Paise
	// LineTotals is parallel to the input slice, so a caller can persist each
	// line without recomputing.
	LineTotals []money.Paise
}

// Compute prices an order.
//
// The platform and delivery fees are added ON TOP of the subtotal and paid by
// the customer; suppliers receive their listed prices in full (CLAUDE.md §6.2).
//
// An empty order prices to zero rather than erroring: rejecting an empty cart
// is the caller's decision, not the calculator's.
func Compute(lines []Line, cfg Config) Breakdown {
	lineTotals := make([]money.Paise, len(lines))

	var subtotal money.Paise
	for i, line := range lines {
		lineTotals[i] = line.LineTotal()
		subtotal = subtotal.Add(lineTotals[i])
	}

	// Integer half-up: (subtotal*bps + 5000) / 10000, via money.ApplyBPS.
	// Half-up rather than banker's rounding because that is what an Indian
	// customer reading a 3% fee on a receipt expects.
	platformFee := money.ApplyBPS(subtotal, cfg.PlatformFeeBPS)

	// A zero-value order carries no delivery charge: charging Rs.15 to deliver
	// nothing would be indefensible on a receipt.
	deliveryFee := cfg.DeliveryFeePaise
	if subtotal == 0 {
		deliveryFee = 0
	}

	return Breakdown{
		SubtotalPaise:    subtotal,
		PlatformFeePaise: platformFee,
		DeliveryFeePaise: deliveryFee,
		TotalPaise:       money.Sum(subtotal, platformFee, deliveryFee),
		LineTotals:       lineTotals,
	}
}

// SupplierPayable is what one supplier is owed for an order: the sum of THEIR
// line totals, with no deductions.
//
// The platform fee and delivery charge are ours and are added on top of the
// subtotal — they are not taken out of what the supplier listed (CLAUDE.md
// §6.2). This is the number that becomes a supplier_payouts row.
func SupplierPayable(lines []Line, supplierID uuid.UUID) money.Paise {
	var payable money.Paise
	for _, line := range lines {
		if line.SupplierID == supplierID {
			payable = payable.Add(line.LineTotal())
		}
	}
	return payable
}

// PayableBySupplier splits an order into what each supplier is owed.
//
// A single order may contain items from several suppliers (CLAUDE.md §5.3), and
// each gets their own payout row.
func PayableBySupplier(lines []Line) map[uuid.UUID]money.Paise {
	payable := map[uuid.UUID]money.Paise{}
	for _, line := range lines {
		payable[line.SupplierID] = payable[line.SupplierID].Add(line.LineTotal())
	}
	return payable
}
