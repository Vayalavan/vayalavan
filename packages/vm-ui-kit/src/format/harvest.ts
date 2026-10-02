/**
 * How rare a GRADE is, from the share of the harvest it represents.
 *
 * A grower's crates are not equal piles: a season of pomegranate might run 55%
 * M, 30% L, 12% XL and 3% XL2, where the XL2 exists because a handful of fruit
 * on each tree sized up. The size selector used to draw all four identically,
 * so nothing on the page said which one was the pick of the field.
 *
 * Three tiers, not five. A customer choosing between crates of fruit can hold
 * "there is barely any of this", "there is some" and "this is most of it" in
 * their head; a finer scale reads as a spreadsheet and makes each step mean
 * less.
 *
 * The thresholds are a display rule and live only in the clients — nothing on
 * the server names a tier. vm-client-mobile-ui carries its own copy with a
 * test asserting it agrees, exactly as `gradedName` does.
 */

export type HarvestTier = "rare" | "uncommon" | "common";

/**
 * At or below this share of the harvest, a grade is RARE.
 *
 * A tenth of the field is the point where "we have a bit of this" becomes "ask
 * early or it is gone" — and it is comfortably above the 3-5% a top grade
 * usually runs at, so the tier does not hinge on a grower's estimate being
 * precise to the percent. It never will be.
 */
const RARE_MAX_PCT = 10;

/** Above RARE_MAX_PCT and at or below this, a grade is UNCOMMON. */
const UNCOMMON_MAX_PCT = 30;

/**
 * The tier for a harvest share, or null when there is nothing to say.
 *
 * Null in, null out: a grower who has not estimated their split is the normal
 * starting state, and inventing a tier for them would put a claim on the page
 * that nobody made. Zero and negatives are treated the same way — they are not
 * shares a harvest can have, and the server rejects them on the way in.
 */
export function harvestTier(pct: number | null | undefined): HarvestTier | null {
  if (pct === null || pct === undefined) return null;
  if (!Number.isFinite(pct) || pct <= 0) return null;
  if (pct <= RARE_MAX_PCT) return "rare";
  if (pct <= UNCOMMON_MAX_PCT) return "uncommon";
  return "common";
}

/**
 * The short word for a tier, for a badge on a chip.
 *
 * Only the rare tier gets one on screen: a badge on every grade is a badge on
 * none. The other two are still named here because a screen reader announces
 * what a colour cannot.
 */
export function harvestTierLabel(tier: HarvestTier): string {
  switch (tier) {
    case "rare":
      return "Rare";
    case "uncommon":
      return "Limited";
    case "common":
      return "Plentiful";
  }
}

/**
 * The sentence under the selector, naming the share in the grower's terms.
 *
 * "About" because it is an estimate of how trees size up over a season, not a
 * measurement of today's crates — printing "3%" flat would claim a precision
 * the number does not have.
 */
export function harvestShareSentence(pct: number, tier: HarvestTier): string {
  switch (tier) {
    case "rare":
      return `About ${pct}% of this grower's harvest — the pick of the field.`;
    case "uncommon":
      return `About ${pct}% of this grower's harvest.`;
    case "common":
      return `About ${pct}% of this grower's harvest — the size there is most of.`;
  }
}
