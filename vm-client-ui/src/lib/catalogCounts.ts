/**
 * Copy for the catalogue's count line.
 *
 * Pure functions in their own module, with no React import, so they are
 * testable without a Vite environment — same reason as countdown.ts.
 */

/**
 * The line under the produce grid.
 *
 * Counts what a customer can BUY, not how many cards are on screen. The grid
 * carries sold-out cards now — produce that a grower listed this morning and
 * has since run out of stays visible, greyed out, rather than disappearing —
 * so "9 items available today" printed under nine greyed-out cards would be a
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
