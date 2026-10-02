// Package money represents Indian rupee amounts as integer paise.
//
// CLAUDE.md rule 1 is absolute: money is int64 paise, never float, never
// decimal strings in logic. Floats cannot represent 0.1 exactly, so a float
// rupee amount silently drifts once it has been through enough arithmetic —
// which for an e-commerce ledger means totals that do not reconcile. This
// package is the only place rupees and paise convert into one another.
//
// Formatting to rupees happens at the UI edge only (see FormatRupees).
package money

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Paise is a monetary amount in Indian paise. 100 paise = 1 rupee.
//
// Deliberately a distinct type rather than a bare int64: it makes an
// accidental mix of "rupee number" and "paise number" a compile error.
type Paise int64

// Zero is the additive identity, provided so callers need not write Paise(0).
const Zero Paise = 0

// paisePerRupee is the conversion base. Named rather than inlined so the
// intent is visible at every use site.
const paisePerRupee = 100

// ErrInvalidRupees is returned by ParseRupees for input that is not a
// well-formed rupee amount with at most two decimal places.
var ErrInvalidRupees = errors.New("money: invalid rupee amount")

// Int64 returns the raw paise value, for storage in a BIGINT column.
func (p Paise) Int64() int64 { return int64(p) }

// Mul multiplies an amount by a whole-number quantity, e.g. a unit price by
// an order line's qty.
func (p Paise) Mul(qty int64) Paise { return Paise(int64(p) * qty) }

// Add returns p + q.
func (p Paise) Add(q Paise) Paise { return p + q }

// Sub returns p - q.
func (p Paise) Sub(q Paise) Paise { return p - q }

// Sum totals a slice of amounts, e.g. order line totals into a subtotal.
func Sum(amounts ...Paise) Paise {
	var total Paise
	for _, a := range amounts {
		total += a
	}
	return total
}

// RoundHalfUp divides numerator by denominator, rounding halves away from
// zero. It is the single rounding rule for the whole platform.
//
// Half-up (not banker's rounding) is what an Indian customer reading a 3%
// fee on a receipt expects, and matches the integer formula in CLAUDE.md
// §6.2: (subtotal*bps + 5000) / 10000.
//
// Panics on a non-positive denominator: that is a programming error, and
// returning a zero would silently under-charge.
func RoundHalfUp(numerator, denominator int64) int64 {
	if denominator <= 0 {
		panic("money: RoundHalfUp requires a positive denominator")
	}
	if numerator >= 0 {
		return (numerator + denominator/2) / denominator
	}
	// Go truncates toward zero, so negatives need the half subtracted to
	// round away from zero rather than toward it.
	return -((-numerator + denominator/2) / denominator)
}

// bpsDenominator is the basis-point base: 10000 bps = 100%.
const bpsDenominator = 10000

// ApplyBPS returns amount * bps / 10000, rounded half-up.
//
// This is how the platform fee is computed (PLATFORM_FEE_BPS=300 -> 3.00%).
// Basis points keep the rate an exact integer, so no float ever touches a
// fee calculation.
func ApplyBPS(amount Paise, bps int64) Paise {
	return Paise(RoundHalfUp(int64(amount)*bps, bpsDenominator))
}

// FromRupees converts a whole number of rupees to paise.
func FromRupees(rupees int64) Paise { return Paise(rupees * paisePerRupee) }

// ParseRupees converts a decimal rupee string such as "149.50" into paise
// exactly, without going through a float.
//
// It exists for the two places rupees legitimately enter the system as text:
// the supplier CSV import's price_rupees column (CLAUDE.md §6.5) and admin
// data entry. At most two decimal places are accepted — a third would mean
// sub-paise precision that cannot be stored or charged.
func ParseRupees(s string) (Paise, error) {
	raw := strings.TrimSpace(s)
	raw = strings.TrimPrefix(raw, "₹")
	// Thousands separators are common in pasted spreadsheet data.
	raw = strings.ReplaceAll(raw, ",", "")
	raw = strings.TrimSpace(raw)

	if raw == "" {
		return 0, fmt.Errorf("%w: empty", ErrInvalidRupees)
	}

	negative := false
	switch raw[0] {
	case '-':
		negative, raw = true, raw[1:]
	case '+':
		raw = raw[1:]
	}
	// A lone sign has no digits to parse.
	if raw == "" {
		return 0, fmt.Errorf("%w: %q has no digits", ErrInvalidRupees, s)
	}

	whole, frac, hasFrac := strings.Cut(raw, ".")
	// "1." and "." are malformed. Padding them to "1.00" would be inventing
	// a price the supplier never typed.
	if hasFrac && frac == "" {
		return 0, fmt.Errorf("%w: %q has a trailing decimal point", ErrInvalidRupees, s)
	}
	if whole == "" {
		// Allow the ".50" shorthand.
		whole = "0"
	}
	if len(frac) > 2 {
		return 0, fmt.Errorf("%w: %q has more than 2 decimal places", ErrInvalidRupees, s)
	}

	// Validate digits explicitly rather than delegating to ParseInt, which
	// accepts a leading sign. Without this, "1.-5" parses as 100 + (-5) =
	// 95 paise and "--5" as +500 — a malformed CSV price silently becoming
	// a plausible wrong one, which is worse than a rejected import.
	if !isDigits(whole) || (hasFrac && !isDigits(frac)) {
		return 0, fmt.Errorf("%w: %q", ErrInvalidRupees, s)
	}

	// Right-pad so "5.5" means 50 paise, not 5.
	for len(frac) < 2 {
		frac += "0"
	}

	wholeVal, err := strconv.ParseInt(whole, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: %q", ErrInvalidRupees, s)
	}
	fracVal, err := strconv.ParseInt(frac, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: %q", ErrInvalidRupees, s)
	}

	total := wholeVal*paisePerRupee + fracVal
	if negative {
		total = -total
	}
	return Paise(total), nil
}

// isDigits reports whether s is non-empty and contains only ASCII 0-9.
//
// Deliberately ASCII-only: unicode.IsDigit would accept Devanagari and other
// non-ASCII digit forms that strconv.ParseInt then rejects, so the two would
// disagree about what counts as a number.
func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// FormatRupees renders an amount for display, e.g. "₹12,34,567.89".
//
// Grouping follows the Indian convention (last three digits, then pairs), not
// the Western thousands convention, because every reader of this number is
// shopping in India.
//
// This is a UI-edge function. Never parse its output back into logic.
func FormatRupees(p Paise) string {
	value := int64(p)
	sign := ""
	if value < 0 {
		sign = "-"
		value = -value
	}

	rupees := value / paisePerRupee
	paise := value % paisePerRupee

	return fmt.Sprintf("%s₹%s.%02d", sign, groupIndian(rupees), paise)
}

// groupIndian inserts separators into a non-negative rupee amount using the
// Indian numbering system: the rightmost group holds three digits and every
// group to its left holds two (1,23,45,678).
func groupIndian(n int64) string {
	digits := strconv.FormatInt(n, 10)
	if len(digits) <= 3 {
		return digits
	}

	head, tail := digits[:len(digits)-3], digits[len(digits)-3:]

	// Walk the head right-to-left in pairs, collecting groups in reverse.
	var groups []string
	for len(head) > 2 {
		groups = append(groups, head[len(head)-2:])
		head = head[:len(head)-2]
	}
	if head != "" {
		groups = append(groups, head)
	}

	var b strings.Builder
	for i := len(groups) - 1; i >= 0; i-- {
		b.WriteString(groups[i])
		b.WriteByte(',')
	}
	b.WriteString(tail)
	return b.String()
}

// RupeesString renders an amount as a plain, ungrouped decimal: "10.50".
//
// For the value of a form field, not for display. FormatRupees produces
// "₹1,23,456.00", which is right on a page and useless in an input — a UI that
// wants to prefill an edit box would have to strip the symbol and the Indian
// grouping back off, which is parsing formatted money, which is exactly what
// the rest of this package exists to avoid.
//
// Round trips through ParseRupees exactly.
func RupeesString(p Paise) string {
	value := int64(p)
	sign := ""
	if value < 0 {
		sign = "-"
		value = -value
	}
	return fmt.Sprintf("%s%d.%02d", sign, value/paisePerRupee, value%paisePerRupee)
}

// FormatBPS renders a rate in basis points as a percentage: "3%", "2.5%",
// "30%", "0.05%".
//
// Trailing zeroes are dropped, because "30.00%" reads as a precision nobody
// entered. Basis points are how every rate on this platform is stored — the
// platform fee, supplier commission and product markup alike — so they are all
// written the same way wherever they are shown.
//
// A UI-edge function. Never parse its output back into logic.
func FormatBPS(bps int64) string {
	sign := ""
	if bps < 0 {
		sign = "-"
		bps = -bps
	}

	whole := bps / 100
	frac := bps % 100

	switch {
	case frac == 0:
		return sign + strconv.FormatInt(whole, 10) + "%"
	case frac%10 == 0:
		return sign + strconv.FormatInt(whole, 10) + "." +
			strconv.FormatInt(frac/10, 10) + "%"
	default:
		return sign + strconv.FormatInt(whole, 10) + "." +
			fmt.Sprintf("%02d", frac) + "%"
	}
}
