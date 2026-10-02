/**
 * Rupee and weight display helpers.
 *
 * Money crosses the wire as integer paise (or a rupee STRING on the way in) —
 * CLAUDE.md rule 1. Nothing here ever produces a number for the API to
 * consume; these functions exist purely to render, and to keep what the
 * supplier is typing legible while they type it.
 *
 * A copy of vm-supplier-ui/src/lib/money.ts, character for character in its
 * logic. It is duplicated rather than shared for the same reason the theme is
 * (see theme/tokens.ts), and money.test.ts here runs the same cases the web
 * app does — if the two ever disagree about what "45.50" means, a supplier
 * sees one price on their phone and a different one on their laptop.
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

/**
 * Live preview for a rupee input as the supplier types.
 *
 * Returns null while the value is not yet a complete amount, so the UI can
 * stay quiet rather than flashing "₹0.00" at someone mid-keystroke.
 */
export function previewRupees(raw: string): string | null {
  const paise = parseRupeesToPaise(raw);
  return paise === null ? null : formatPaise(paise);
}

/**
 * Parses a rupee string to integer paise, mirroring money.ParseRupees in Go.
 *
 * Deliberately does NOT use parseFloat: 45.50 is not exactly representable as
 * a float64, and a UI that disagrees with the server about a price by one
 * paise is worse than one that refuses to guess. Returns null when the input
 * is not a valid amount.
 */
export function parseRupeesToPaise(raw: string): number | null {
  let value = raw.trim().replace(/^₹/, "").replace(/,/g, "").trim();
  if (value === "") return null;

  let negative = false;
  if (value.startsWith("-")) {
    negative = true;
    value = value.slice(1);
  } else if (value.startsWith("+")) {
    value = value.slice(1);
  }
  if (value === "") return null;

  const dot = value.indexOf(".");
  let whole = value;
  let fraction = "";

  if (dot >= 0) {
    whole = value.slice(0, dot);
    fraction = value.slice(dot + 1);
    // "1." is malformed; padding it to "1.00" would invent a price.
    if (fraction === "" || fraction.length > 2) return null;
  }
  if (whole === "") whole = "0";

  // Explicit digit check: Number() would happily accept "1e3" and " 5 ".
  if (!/^\d+$/.test(whole)) return null;
  if (fraction !== "" && !/^\d+$/.test(fraction)) return null;

  const paise = Number(whole) * 100 + Number(fraction.padEnd(2, "0"));
  return negative ? -paise : paise;
}

/** Renders grams as the supplier thinks of them: 3000 -> "3 kg". */
export function formatGrams(grams: number): string {
  if (grams >= 1000 && grams % 1000 === 0) return `${grams / 1000} kg`;
  if (grams >= 1000) return `${(grams / 1000).toFixed(2)} kg`;
  return `${grams} g`;
}

/**
 * Kilograms as typed into the availability box, converted to the grams the
 * wire carries. Returns null while the value is not a usable number.
 *
 * Availability is tracked in grams at the product level (CLAUDE.md §5.2), but
 * a grower thinks in kilos, so the box takes kilos and this is the only place
 * the conversion happens.
 */
export function kgToGrams(value: string): number | null {
  const trimmed = value.trim();
  if (trimmed === "") return null;
  // A bare "." and an empty string both pass /^\d*\.?\d*$/, so the digit check
  // below is what actually rejects them.
  if (!/^\d*\.?\d*$/.test(trimmed)) return null;
  if (!/\d/.test(trimmed)) return null;

  const kg = Number(trimmed);
  if (!Number.isFinite(kg) || kg < 0) return null;
  // Rounded, not truncated: 12.3456 kg is 12346 g, and a supplier typing a
  // stray digit should not silently lose a gram.
  return Math.round(kg * 1000);
}

/** Grams back into the kilogram box, without a trail of zeroes. */
export function gramsToKg(grams: number): string {
  if (grams === 0) return "0";
  return String(Number((grams / 1000).toFixed(3)));
}
