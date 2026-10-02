/**
 * Rupee and weight display helpers.
 *
 * Money crosses the wire as integer paise — CLAUDE.md rule 1 — and every
 * customer-facing amount also arrives pre-formatted by the server as a
 * `*_display` string. Prefer the server's string wherever there is one: it is
 * the same value the confirmation email and the admin console show, and a
 * total the phone computed for itself is a total that can disagree with the
 * one being charged.
 *
 * These exist for the places where there is no server string to render — a
 * per-line subtotal derived while a quantity edit is still in flight, and pack
 * weights. Nothing here ever produces a number for the API to consume.
 *
 * The formatting logic is a copy of vm-supplier-mobile-ui/src/lib/money.ts,
 * which is itself a copy of the web `money.ts`; money.test.ts here runs the
 * same cases, so a phone and a laptop can never disagree about what
 * "₹1,200.50" means. The supplier app's rupee-INPUT parsing is deliberately
 * absent: a customer never types a price.
 */

/**
 * Groups digits the Indian way: last three, then pairs.
 * 1234567 -> "12,34,567", not "1,234,567".
 */
function groupIndian(digits: string): string {
  if (digits.length <= 3) return digits;

  const tail = digits.slice(-3);
  let head = digits.slice(0, -3);
  const groups: string[] = [];

  while (head.length > 2) {
    groups.unshift(head.slice(-2));
    head = head.slice(0, -2);
  }
  if (head) groups.unshift(head);

  return `${groups.join(",")},${tail}`;
}

/** Formats integer paise for display, e.g. 120050 -> "₹1,200.50". */
export function formatPaise(paise: number): string {
  const negative = paise < 0;
  const absolute = Math.abs(paise);
  const rupees = Math.floor(absolute / 100);
  const remainder = absolute % 100;

  return `${negative ? "-" : ""}₹${groupIndian(String(rupees))}.${String(remainder).padStart(2, "0")}`;
}

/** Renders grams the way a pack is described: 3000 -> "3 kg". */
export function formatGrams(grams: number): string {
  if (grams >= 1000 && grams % 1000 === 0) return `${grams / 1000} kg`;
  if (grams >= 1000) return `${(grams / 1000).toFixed(2)} kg`;
  return `${grams} g`;
}
