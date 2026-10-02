/**
 * The delivery area: which PIN codes this shop will accept an order for.
 *
 * The same rule as vm-go-common's `serviceable` package, which is the one that
 * actually enforces it — this copy exists so a customer is told at the field
 * instead of after a round trip. Never the only check: the UI can be skipped.
 *
 * Each mobile app carries its own copy with a test asserting it agrees.
 */

/**
 * Bengaluru is 560xxx — the urban district only. 561xxx and 562xxx are
 * Bangalore Rural and Ramanagara, which are a different courier problem and
 * are deliberately out.
 */
const BENGALURU_PREFIX = 560;

/**
 * Tamil Nadu is 600xxx (Chennai) through 643xxx (the Nilgiris), the whole
 * state.
 *
 * Known imprecision: 605xxx (Puducherry) and 609xxx (Karaikal) sit inside the
 * band but are not Tamil Nadu. They are accepted — excluding them means holes
 * in the range, and a courier who reaches Cuddalore reaches Puducherry.
 */
const TAMIL_NADU_FIRST = 600;
const TAMIL_NADU_LAST = 643;

/** Names the area in customer-facing copy — a hint and a rejection agree. */
export const DELIVERY_AREA_LABEL = "Bengaluru and Tamil Nadu";

/**
 * The field-level message for an address outside the area.
 *
 * A FRAGMENT, matching vm-go-common's `PincodeFieldError` word for word: the
 * web form prints it after the field's label ("PIN code must be in …"), and a
 * server rejection of the same address has to read identically or it looks
 * like a second, different problem.
 */
export const PINCODE_AREA_ERROR = `must be in ${DELIVERY_AREA_LABEL} — we do not deliver elsewhere yet`;

/**
 * Whether we deliver to a PIN code.
 *
 * Shape is checked here too, so a caller that has not already validated six
 * digits cannot let "56000" through on a prefix comparison.
 */
export function isServiceablePincode(pincode: string): boolean {
  const pin = pincode.trim();

  // Six digits, and never a leading zero — not a real Indian PIN code.
  if (!/^[1-9][0-9]{5}$/.test(pin)) return false;

  const first3 = Number(pin.slice(0, 3));

  return (
    first3 === BENGALURU_PREFIX ||
    (first3 >= TAMIL_NADU_FIRST && first3 <= TAMIL_NADU_LAST)
  );
}
