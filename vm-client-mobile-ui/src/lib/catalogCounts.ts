/**
 * Copy for the catalogue's count line.
 *
 * Pure functions in their own module, with no React Native import, so they are
 * testable under `node --test` — same reason as countdown.ts.
 *
 * A copy of vm-client-ui/src/lib/catalogCounts.ts, word for word. The two must
 * say the same thing about the same catalogue: a customer who checks the app
 * and then the website should not be told a different number of items is on
 * sale.
 */

/**
 * The line under the produce list.
 *
 * Counts what a customer can BUY, not how many cards are on screen. The list
 * carries sold-out cards — produce that a grower listed this morning and has
 * since run out of stays visible, greyed out, rather than disappearing — so
 * "9 items available today" printed under nine greyed-out cards would be a
 * flat contradiction of the picture above it.
 *
 * The sold-out tail is named separately rather than folded into the total,
 * because it is the answer to the question a customer actually has when they
 * see a grey card: yes, we had it, and no, you cannot have it today.
 */
export function summariseCatalogCounts(sellable: number, total: number): string {
  // Clamped: the two counts come from separate queries and a sale landing
  // between them could otherwise print "-1 sold out".
  const soldOut = Math.max(0, total - sellable);

  if (sellable <= 0) {
    if (soldOut === 0) return "Nothing is listed for today yet.";
    return soldOut === 1
      ? "The one item listed today has sold out."
      : "Everything listed today has sold out.";
  }

  const available = `${sellable} item${sellable === 1 ? "" : "s"} available today`;
  return soldOut === 0 ? `${available}.` : `${available}, ${soldOut} sold out.`;
}
