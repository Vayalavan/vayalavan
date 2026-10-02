import assert from "node:assert/strict";
import { test } from "node:test";

import { formatKg, peakOf, sharePct, shortDate } from "./analytics";

test("shortDate reformats a server date without shifting it", () => {
  // The server sends dates already in IST. This must only ever relabel them —
  // parsing to a Date and back would reintroduce the device's timezone, which
  // is the bug CLAUDE.md rule 2 exists to prevent.
  assert.equal(shortDate("2026-08-18"), "18 Aug");
  assert.equal(shortDate("2026-01-01"), "1 Jan");
  assert.equal(shortDate("2026-12-31"), "31 Dec");
});

test("shortDate leaves anything unparseable alone", () => {
  assert.equal(shortDate(""), "");
  assert.equal(shortDate("not-a-date"), "not-a-date");
});

test("sharePct never prints a non-zero amount as 0%", () => {
  assert.equal(sharePct(0, 100), "0%");
  assert.equal(sharePct(1, 1000), "<1%");
  assert.equal(sharePct(50, 100), "50%");
  assert.equal(sharePct(2, 3), "67%");
  // No orders at all: not a division, and not NaN%.
  assert.equal(sharePct(0, 0), "0%");
});

test("peakOf never returns zero, so no chart divides by it", () => {
  assert.equal(peakOf([]), 1);
  assert.equal(peakOf([0, 0]), 1);
  assert.equal(peakOf([12, 900, 40]), 900);
});

test("formatKg reads the way a grower weighs", () => {
  assert.equal(formatKg(250), "250 g");
  assert.equal(formatKg(1000), "1 kg");
  assert.equal(formatKg(1500), "1.5 kg");
  assert.equal(formatKg(42000), "42 kg");
});
