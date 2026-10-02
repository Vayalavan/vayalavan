/**
 * Display helpers. All money crosses the wire as integer paise
 * (CLAUDE.md rule 1); these only render.
 */

/** Indian digit grouping: last three, then pairs. 1234567 -> "12,34,567". */
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

export function formatPaise(paise: number): string {
  const negative = paise < 0;
  const abs = Math.abs(paise);
  return `${negative ? "-" : ""}₹${groupIndian(String(Math.floor(abs / 100)))}.${String(
    abs % 100,
  ).padStart(2, "0")}`;
}

/** "14 Aug 2026" from an ISO date or timestamp. */
export function formatDate(value: string): string {
  const d = new Date(value);
  return Number.isNaN(d.getTime())
    ? value
    : d.toLocaleDateString("en-IN", { day: "2-digit", month: "short", year: "numeric" });
}
