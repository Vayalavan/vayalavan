import assert from "node:assert/strict";
import { test } from "node:test";

import { summariseCatalogCounts } from "./catalogCounts";

/**
 * The grid carries sold-out cards, so the count line has to distinguish what
 * is on screen from what can be bought. Every case here is a sentence a
 * customer will actually read under the produce.
 */
test("summariseCatalogCounts", () => {
  const cases: ReadonlyArray<[number, number, string]> = [
    // sellable, total, expected
    [12, 12, "12 items available today."],
    [1, 1, "1 item available today."],
    [9, 12, "9 items available today, 3 sold out."],
    [1, 2, "1 item available today, 1 sold out."],
    [0, 5, "Everything listed today has sold out."],
    [0, 1, "The one item listed today has sold out."],
    [0, 0, "Nothing is listed for today yet."],
    // The two counts come from separate queries; a sale landing between them
    // must not produce "-1 sold out".
    [5, 4, "5 items available today."],
  ];

  for (const [sellable, total, want] of cases) {
    assert.equal(
      summariseCatalogCounts(sellable, total),
      want,
      `summariseCatalogCounts(${sellable}, ${total})`,
    );
  }
});
