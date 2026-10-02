/**
 * The timeline is the only place a customer learns when their food arrives,
 * and the dates are computed in Asia/Kolkata (CLAUDE.md rule 2). Formatting
 * them through the device's timezone would shift the visible date for anyone
 * outside IST — a customer in London would be told the wrong delivery day.
 *
 * These tests run under whatever TZ the machine has, which is the point: the
 * expected values never change.
 */
import { test } from "node:test";
import assert from "node:assert/strict";
import { formatMilestoneAt } from "./OrderTimeline.js";

test("formats a placement timestamp with the time of day", () => {
  assert.equal(formatMilestoneAt("2026-08-15T03:28:59+05:30"), "15 Aug 2026, 3:28 am");
});

test("formats the 4pm processing cutoff", () => {
  assert.equal(formatMilestoneAt("2026-08-15T16:00:00+05:30"), "15 Aug 2026, 4:00 pm");
});

test("formats the delivery day with no invented time", () => {
  assert.equal(formatMilestoneAt("2026-08-16"), "16 Aug 2026");
});

test("converts a UTC timestamp into IST rather than showing it raw", () => {
  // 10:30 UTC is 16:00 IST — the same instant as the cutoff above.
  assert.equal(formatMilestoneAt("2026-08-15T10:30:00Z"), "15 Aug 2026, 4:00 pm");
});

test("a bare date does not slip to the previous day", () => {
  // "2026-08-16" parsed as UTC midnight is still the 15th anywhere behind
  // UTC. Anchoring to +05:30 is what keeps this on the 16th.
  assert.equal(formatMilestoneAt("2026-08-16"), "16 Aug 2026");
});

test("a late-evening IST timestamp keeps its own date", () => {
  // 23:45 IST is 18:15 UTC the same day; a naive UTC render would say the
  // 15th at 6:15 pm, losing both the date's meaning and the hour.
  assert.equal(formatMilestoneAt("2026-08-15T23:45:00+05:30"), "15 Aug 2026, 11:45 pm");
});

test("an instant just before IST midnight does not roll forward", () => {
  assert.equal(formatMilestoneAt("2026-08-15T18:29:59Z"), "15 Aug 2026, 11:59 pm");
});

test("an instant just after IST midnight rolls to the next day", () => {
  assert.equal(formatMilestoneAt("2026-08-15T18:30:00Z"), "16 Aug 2026, 12:00 am");
});

test("falls back to the server string instead of showing 'Invalid Date'", () => {
  assert.equal(formatMilestoneAt("not-a-date"), "not-a-date");
  assert.equal(formatMilestoneAt(""), "");
});
