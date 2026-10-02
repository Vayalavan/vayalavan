/**
 * Binds this app's rarity thresholds to vm-ui-kit's.
 *
 * This app cannot import the kit at runtime (see the note in harvest.ts), so
 * the guarantee is enforced here: the test imports the kit's copy from the
 * repository and runs both over the same table. A threshold moved on the web
 * and not here would draw the same crate gold in a browser and silver on a
 * phone.
 *
 * The kit's harvest.ts has no imports of its own, so tsx loads it cleanly.
 */
import { test } from "node:test";
import assert from "node:assert/strict";

import { harvestTier, harvestTierLabel, harvestShareSentence } from "./harvest";
import {
  harvestTier as kitHarvestTier,
  harvestTierLabel as kitHarvestTierLabel,
  harvestShareSentence as kitHarvestShareSentence,
  type HarvestTier,
} from "../../../packages/vm-ui-kit/src/format/harvest";

const CASES: ReadonlyArray<readonly [number | null | undefined, HarvestTier | null]> = [
  // Rare: at or below a tenth of the field.
  [1, "rare"],
  [3, "rare"],
  [10, "rare"],
  // Uncommon: the middle band, both edges.
  [11, "uncommon"],
  [30, "uncommon"],
  // Common: most of the harvest.
  [31, "common"],
  [55, "common"],
  [100, "common"],

  // Nothing to say. A grower who has not estimated their split must not be
  // given a tier they never claimed.
  [null, null],
  [undefined, null],
  // Not shares a harvest can have. The server rejects them on the way in;
  // these are what the clients do if one ever arrives anyway.
  [0, null],
  [-5, null],
  [Number.NaN, null],
  [Number.POSITIVE_INFINITY, null],
];

test("the rarity thresholds match vm-ui-kit's", () => {
  for (const [pct, want] of CASES) {
    assert.equal(harvestTier(pct), want, `harvestTier(${String(pct)})`);
    assert.equal(kitHarvestTier(pct), want, `the kit disagrees about ${String(pct)}`);
  }
});

test("the tier words match vm-ui-kit's", () => {
  // A grade that reads "Rare" in a browser and "Limited" on a phone is two
  // shops describing one crate.
  for (const tier of ["rare", "uncommon", "common"] as const) {
    assert.equal(harvestTierLabel(tier), kitHarvestTierLabel(tier));
  }
});

test("the share sentence matches vm-ui-kit's", () => {
  for (const [pct, tier] of [
    [3, "rare"],
    [20, "uncommon"],
    [55, "common"],
  ] as const) {
    assert.equal(harvestShareSentence(pct, tier), kitHarvestShareSentence(pct, tier));
  }
});
