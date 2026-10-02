// Package serviceable holds the delivery area: which PIN codes this shop will
// accept an order for.
//
// The rule lives here rather than as a copy per service because two services
// apply it at different moments and must not disagree. vm-profile-api refuses
// to SAVE an address outside the area; vm-orders-api refuses to CHECK OUT to
// one, which also catches an address saved before the area was narrowed. A
// customer who could store an address but not order to it — or worse, the
// other way round — would have found that out at the payment screen.
//
// The web UIs apply the same rule in @vayal/ui-kit's `isServiceablePincode`,
// and each mobile app carries its own copy with a test asserting it agrees.
package serviceable

import "strings"

// The area we deliver to today.
//
// Bengaluru is 560xxx — the urban district only. 561xxx and 562xxx are
// Bangalore Rural and Ramanagara, which are a different courier problem and
// are deliberately out.
//
// Tamil Nadu is 600xxx (Chennai) through 643xxx (the Nilgiris), which is the
// whole state.
//
// Known imprecision: 605xxx (Puducherry) and 609xxx (Karaikal) sit inside the
// Tamil Nadu band but are not Tamil Nadu. They are accepted. Excluding them
// means listing holes in the range, and a courier who reaches Cuddalore
// reaches Puducherry — so this errs towards delivering rather than towards
// turning away an address we could in fact serve.
const (
	bengaluruPrefix = 560
	tamilNaduFirst  = 600
	tamilNaduLast   = 643
)

// AreaLabel names the area in customer-facing copy, so a form hint and a
// rejection message cannot describe different shops.
const AreaLabel = "Bengaluru and Tamil Nadu"

// PincodeFieldError is the field-level message for an address outside the
// area, phrased as the fragment the validation envelope expects.
const PincodeFieldError = "must be in " + AreaLabel + " — we do not deliver elsewhere yet"

// Pincode reports whether we deliver to a PIN code.
//
// Shape is checked here too: a caller that has already validated six digits
// loses nothing, and one that has not cannot accidentally let "56000" through
// on a prefix comparison.
func Pincode(value string) bool {
	pin := strings.TrimSpace(value)
	if len(pin) != 6 {
		return false
	}
	for _, r := range pin {
		if r < '0' || r > '9' {
			return false
		}
	}
	// Leading zero is not a real Indian PIN code, and the first digit is the
	// postal zone.
	if pin[0] == '0' {
		return false
	}

	// Digits only, six of them — the conversion cannot overflow or fail.
	first3 := int(pin[0]-'0')*100 + int(pin[1]-'0')*10 + int(pin[2]-'0')

	if first3 == bengaluruPrefix {
		return true
	}
	return first3 >= tamilNaduFirst && first3 <= tamilNaduLast
}
