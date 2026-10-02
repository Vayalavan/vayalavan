package pricing

import "github.com/vayal-mikrogreenz/vm-go-common/money"

// Every rate that decides who pays what, and what we keep, in one place.
//
// These four numbers are the entire commercial model. They were previously
// scattered between a config struct, a handler and two UI strings, which meant
// changing the delivery charge required finding every copy. Changing a rate
// should be one edit here (or one environment variable), and nothing else.
//
// All values are integers — basis points for percentages, paise for money —
// because CLAUDE.md rule 1 forbids float in money logic. A float rate drifts
// once it has been through enough arithmetic, and for a ledger that means
// totals that do not reconcile.
//
// # Where the money goes
//
// For an order with subtotal S:
//
//	customer pays     = S + platform fee + delivery charge
//	supplier receives = (S − markup) − supplier commission
//	we keep           = platform fee + supplier commission + delivery margin
//	                    + markup
//	courier receives  = delivery charge − delivery margin
//
// S — the subtotal — is the sum of what the CUSTOMER paid per line, and that
// already contains the admin markup: a product's price on the storefront is
// the grower's price plus a flat per-pack markup set in the admin console. The
// grower is neither paid it nor commissioned on it, which is why the supplier
// line above subtracts it before commission.
//
// Note that supplier commission is DEDUCTED from the grower's payout, while
// the platform fee is ADDED on top of what the customer pays. They are
// different rates on different sides of the same order and must not be
// conflated — see SupplierCommissionBPS.
const (
	// DefaultPlatformFeeBPS is charged to the CUSTOMER, added on top of the
	// subtotal. 300 = 3.00%. Override with PLATFORM_FEE_BPS.
	DefaultPlatformFeeBPS int64 = 300

	// DefaultSupplierCommissionBPS is deducted from the SUPPLIER's payout.
	// 300 = 3.00%. Override with SUPPLIER_COMMISSION_BPS.
	//
	// This is our commission for listing and selling their produce. The
	// supplier's own screen states it explicitly and shows the net figure, so
	// a grower always knows what will land in their bank.
	//
	// Set to 0 to pay suppliers their listed prices in full.
	DefaultSupplierCommissionBPS int64 = 300

	// DefaultDeliveryFeePaise is the flat charge to the customer.
	// 1500 = Rs. 15.00. Override with DELIVERY_FEE_PAISE.
	DefaultDeliveryFeePaise money.Paise = 1500

	// DefaultDeliveryMarginPaise is our share of the delivery charge; the rest
	// goes to the courier. 500 = Rs. 5.00 of the Rs. 15.00 collected.
	// Override with DELIVERY_MARGIN_PAISE.
	//
	// Purely an accounting split: the customer pays the delivery fee either
	// way, and this decides how much of it is revenue rather than cost. It
	// must never exceed the delivery fee — ValidateRates enforces that.
	DefaultDeliveryMarginPaise money.Paise = 500
)

// Rates is the live configuration, loaded from the environment at startup.
type Rates struct {
	PlatformFeeBPS        int64
	SupplierCommissionBPS int64
	DeliveryFeePaise      money.Paise
	DeliveryMarginPaise   money.Paise
}

// DefaultRates returns the constants above.
func DefaultRates() Rates {
	return Rates{
		PlatformFeeBPS:        DefaultPlatformFeeBPS,
		SupplierCommissionBPS: DefaultSupplierCommissionBPS,
		DeliveryFeePaise:      DefaultDeliveryFeePaise,
		DeliveryMarginPaise:   DefaultDeliveryMarginPaise,
	}
}

// SupplierCommission is what we deduct from one supplier's share of an order,
// at the platform's default rate.
//
// Rounded half-up in integer paise, the same rule as the platform fee, so the
// two never disagree by a paise on the same subtotal.
func (r Rates) SupplierCommission(supplierSubtotal money.Paise) money.Paise {
	return r.SupplierCommissionAt(supplierSubtotal, nil)
}

// SupplierCommissionAt applies a supplier's own negotiated rate.
//
// `override` is nil for the great majority of suppliers, who are on the
// platform default. Passing the rate explicitly rather than reading it from a
// field means the caller has to have resolved it — which is what stops a code
// path quietly using the default because it forgot to look one up.
func (r Rates) SupplierCommissionAt(
	supplierSubtotal money.Paise, override *int64,
) money.Paise {
	return money.ApplyBPS(supplierSubtotal, r.effectiveBPS(override))
}

// SupplierPayableAt is what reaches a grower's bank at their own rate.
func (r Rates) SupplierPayableAt(
	supplierSubtotal money.Paise, override *int64,
) money.Paise {
	payable := supplierSubtotal - r.SupplierCommissionAt(supplierSubtotal, override)
	if payable < 0 {
		return 0
	}
	return payable
}

// effectiveBPS resolves an optional override against the platform default.
//
// An out-of-range override is ignored rather than clamped: the value has
// already been validated at the point it was entered, so seeing one here means
// something is wrong, and silently charging 100% because a bad number reached
// this far would be far worse than falling back to the configured rate.
func (r Rates) effectiveBPS(override *int64) int64 {
	if override == nil || *override < 0 || *override > 10000 {
		return r.SupplierCommissionBPS
	}
	return *override
}

// SupplierPayable is what actually reaches the grower's bank.
//
// Clamped at zero: a commission rate above 100% would otherwise produce a
// negative payout, which would show up as us invoicing a farmer.
func (r Rates) SupplierPayable(supplierSubtotal money.Paise) money.Paise {
	payable := supplierSubtotal - r.SupplierCommission(supplierSubtotal)
	if payable < 0 {
		return 0
	}
	return payable
}

// PlatformFee is what the customer pays on top of the subtotal.
func (r Rates) PlatformFee(subtotal money.Paise) money.Paise {
	return money.ApplyBPS(subtotal, r.PlatformFeeBPS)
}

// Earnings is our revenue from one order, split by source.
//
// The four sources are reported separately because they behave differently:
// the platform fee and supplier commission scale with basket size, the
// delivery margin is flat per order, and markup is flat per PACK sold. A
// single blended number hides which one is actually paying for the business.
type Earnings struct {
	PlatformFeePaise        money.Paise
	SupplierCommissionPaise money.Paise
	DeliveryMarginPaise     money.Paise
	// MarkupPaise is what we added to growers' prices, summed from the markup
	// snapshotted on each order line — never recomputed from a product's
	// current markup, which an admin may have changed since the sale.
	MarkupPaise money.Paise
	TotalPaise  money.Paise
}

// Total sums the sources. One place, so a new source cannot be added to the
// struct and forgotten in the addition.
func (e Earnings) Total() money.Paise {
	return e.PlatformFeePaise + e.SupplierCommissionPaise +
		e.DeliveryMarginPaise + e.MarkupPaise
}

// EarningsFor computes our take on one order.
//
// supplierSubtotal is the sum of ALL suppliers' lines, which for a
// single-supplier order equals the order subtotal. Commission is charged on
// the total supplier value, so a multi-supplier order yields the same
// commission as a single-supplier one of the same size.
func (r Rates) EarningsFor(subtotal, supplierSubtotal money.Paise) Earnings {
	return r.EarningsWithMarkup(subtotal, supplierSubtotal, 0)
}

// EarningsWithMarkup is EarningsFor plus the markup collected on the order.
//
// markup is passed in rather than derived: it lives on the order's lines,
// snapshotted at placement, and a rate cannot reproduce it.
//
// Note what supplierSubtotal must be — the growers' OWN value, with the markup
// already taken out. Passing the customer subtotal here would commission
// growers on our margin and count that margin twice in the total.
func (r Rates) EarningsWithMarkup(
	subtotal, supplierSubtotal, markup money.Paise,
) Earnings {
	earnings := Earnings{
		PlatformFeePaise:        r.PlatformFee(subtotal),
		SupplierCommissionPaise: r.SupplierCommission(supplierSubtotal),
		DeliveryMarginPaise:     r.DeliveryMarginPaise,
		MarkupPaise:             markup,
	}
	earnings.TotalPaise = earnings.Total()
	return earnings
}

// ValidateRates rejects a configuration that cannot be true.
//
// Called at startup so a bad environment variable stops the deploy rather than
// silently mispaying every supplier for a week (CLAUDE.md rule 3).
func ValidateRates(r Rates) error {
	if r.PlatformFeeBPS < 0 {
		return rateError("PLATFORM_FEE_BPS cannot be negative")
	}
	if r.SupplierCommissionBPS < 0 {
		return rateError("SUPPLIER_COMMISSION_BPS cannot be negative")
	}
	if r.SupplierCommissionBPS > 10000 {
		return rateError("SUPPLIER_COMMISSION_BPS above 10000 would take more " +
			"than the whole of a supplier's sale")
	}
	if r.DeliveryFeePaise < 0 {
		return rateError("DELIVERY_FEE_PAISE cannot be negative")
	}
	if r.DeliveryMarginPaise < 0 {
		return rateError("DELIVERY_MARGIN_PAISE cannot be negative")
	}
	if r.DeliveryMarginPaise > r.DeliveryFeePaise {
		return rateError("DELIVERY_MARGIN_PAISE cannot exceed DELIVERY_FEE_PAISE — " +
			"we would be keeping more than the customer pays for delivery")
	}
	return nil
}

type rateError string

func (e rateError) Error() string { return "pricing: " + string(e) }
