/**
 * The sales screen's reporting periods, and the rules for turning a choice
 * into a query.
 *
 * Kept out of the component so `salesRange.test.ts` can load it under plain
 * Node: importing anything that pulls in react-native fails the test runner,
 * which is why every test in this app targets a module like this one.
 */
/** Period keys vm-orders-api accepts, in the order a grower reaches for them. */
export const RANGES: ReadonlyArray<readonly [key: string, label: string]> = [
  ["all", "All time"],
  ["today", "Today"],
  ["2d", "2 days"],
  ["3d", "3 days"],
  ["7d", "7 days"],
  ["1m", "Month"],
  ["custom", "Custom"],
];

/**
 * The periods the INSIGHTS screen may offer.
 *
 * The same list without "All time", and not a cosmetic difference:
 * /supplier/analytics resolves its window with a resolver that refuses `all`
 * — a trend line over a grower's entire history would compress a useful week
 * into three pixels — and answers `range=all` with a 400. Offering the chip
 * anyway is how the screen ended up showing "range must be one of…" to a
 * supplier who simply tapped the first option.
 *
 * Derived from RANGES rather than retyped, so a period added for sales cannot
 * silently go missing here.
 */
export const TREND_RANGES: ReadonlyArray<readonly [key: string, label: string]> =
  RANGES.filter(([key]) => key !== "all");

/** "2026-08-17" and a real calendar day, not merely four digits and dashes. */
export function isISODate(value: string): boolean {
  if (!/^\d{4}-\d{2}-\d{2}$/.test(value)) return false;
  const parsed = new Date(`${value}T00:00:00Z`);
  return !Number.isNaN(parsed.getTime()) && parsed.toISOString().slice(0, 10) === value;
}

/**
 * What to send, or null while a custom range is still half-typed.
 *
 * Sending half a range would be refused by the server, so the screen keeps
 * showing the last good result until the second date is valid.
 */
export function rangeQueryFor(
  range: string,
  from: string,
  to: string,
): Record<string, string> | null {
  if (range !== "custom") return { range };
  if (!isISODate(from) || !isISODate(to)) return null;
  if (from > to) return null; // ISO dates compare correctly as strings.
  return { range, from, to };
}
