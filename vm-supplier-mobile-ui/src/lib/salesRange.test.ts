/**
 * Binds this app's sales-period list to the web's.
 *
 * The same supplier checks their phone in the field and their laptop in the
 * evening. If one offers "Past 3 days" and the other does not — or worse, if
 * one sends a key vm-orders-api rejects — the two screens quietly answer
 * different questions about the same money.
 *
 * This app is outside the npm workspaces and cannot import @vayal/ui-kit
 * (React DOM), which is exactly the case CLAUDE.md §2 says to cover with a
 * test rather than leave to drift. The web list is read as SOURCE TEXT, not
 * imported, for the same reason.
 */
import { test } from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";

import { RANGES, TREND_RANGES, isISODate, rangeQueryFor } from "./salesRange";

const WEB_PICKER = path.resolve(
  __dirname,
  "../../../packages/vm-ui-kit/src/components/RangePicker.tsx",
);

/** The keys in SALES_RANGES, in order: ["all", ...DEFAULT_RANGES]. */
function webSalesKeys(): string[] {
  const source = fs.readFileSync(WEB_PICKER, "utf8");
  const block = source.slice(source.indexOf("export const DEFAULT_RANGES"));
  const defaults = block.slice(0, block.indexOf("];"));
  const keys = [...defaults.matchAll(/\["([a-z0-9]+)",/g)].map((match) => match[1]!);
  assert.ok(keys.length > 0, "found no range keys in the web RangePicker");
  // SALES_RANGES is all time followed by the defaults.
  return ["all", ...keys];
}

test("the sales periods match the web picker, in the same order", () => {
  assert.deepEqual(
    RANGES.map(([key]) => key),
    webSalesKeys(),
  );
});

test("every period key is one vm-orders-api accepts", () => {
  // resolveRange in vm-orders-api/internal/api/daterange.go.
  const accepted = new Set(["all", "today", "2d", "3d", "7d", "1m", "custom"]);
  for (const [key] of RANGES) {
    assert.ok(accepted.has(key), `${key} is not a period the API accepts`);
  }
});

test("isISODate accepts real calendar days and rejects the rest", () => {
  for (const good of ["2026-08-17", "2026-01-01", "2024-02-29"]) {
    assert.equal(isISODate(good), true, good);
  }
  for (const bad of [
    "",
    "17-08-2026",
    "2026-8-17",
    "2026-13-01",
    // The one a regex alone lets through: 2026 is not a leap year.
    "2026-02-30",
    "not a date",
  ]) {
    assert.equal(isISODate(bad), false, bad);
  }
});

test("a half-typed custom range asks for nothing", () => {
  assert.equal(rangeQueryFor("custom", "", ""), null);
  assert.equal(rangeQueryFor("custom", "2026-08-01", ""), null);
  assert.equal(rangeQueryFor("custom", "2026-08-01", "2026-08-"), null);
  // Backwards is not a range either — the server would refuse it.
  assert.equal(rangeQueryFor("custom", "2026-08-17", "2026-08-01"), null);
});

test("a complete range is sent as the API expects it", () => {
  assert.deepEqual(rangeQueryFor("7d", "", ""), { range: "7d" });
  // Stale custom dates must not ride along with a preset.
  assert.deepEqual(rangeQueryFor("today", "2026-08-01", "2026-08-02"), {
    range: "today",
  });
  assert.deepEqual(rangeQueryFor("custom", "2026-08-01", "2026-08-17"), {
    range: "custom",
    from: "2026-08-01",
    to: "2026-08-17",
  });
});

test("TREND_RANGES omits all-time, which /supplier/analytics refuses", () => {
  // The insights endpoint resolves its window with a resolver that rejects
  // `all`. Offering the chip anyway put "range must be one of: today, 2d, 3d,
  // 7d, 1m, custom." in front of a supplier who tapped the first option.
  assert.ok(!TREND_RANGES.some(([key]) => key === "all"));
  // Everything else the sales screen offers is still there, in the same order.
  assert.deepEqual(
    TREND_RANGES.map(([key]) => key),
    RANGES.filter(([key]) => key !== "all").map(([key]) => key),
  );
});
