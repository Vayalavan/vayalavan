/**
 * These run under whatever TZ the machine has, which is the whole point: the
 * expected values never change. Set TZ=America/Los_Angeles and they still
 * pass — that is what "business days are IST, never the device's zone" means
 * in practice (CLAUDE.md rule 2).
 *
 * The cutoff rows from CLAUDE.md §6.1 are reproduced here deliberately. The
 * server computes those timestamps; this asserts the phone renders them back
 * as the same wall-clock moment the server meant.
 */
import { test } from "node:test";
import assert from "node:assert/strict";

import {
  formatISTDate,
  formatISTDateTime,
  formatISTTime,
  istTodayISO,
  toISTParts,
} from "./datetime";

test("an IST timestamp keeps its own date and time", () => {
  assert.equal(formatISTDateTime("2026-08-15T03:28:59+05:30"), "15 Aug 2026, 3:28 am");
});

test("the 4pm cutoff renders as 4:00 pm", () => {
  assert.equal(formatISTDateTime("2026-06-15T16:00:00+05:30"), "15 Jun 2026, 4:00 pm");
});

test("a UTC timestamp is converted to IST, not shown raw", () => {
  // 10:30 UTC is 16:00 IST — the same instant as the cutoff above. This is
  // CLAUDE.md §6.1's last table row: UTC input must be converted, not
  // compared naively.
  assert.equal(formatISTDateTime("2026-06-15T10:30:00Z"), "15 Jun 2026, 4:00 pm");
});

test("a late-evening IST timestamp does not roll back to the previous day", () => {
  // 23:45 IST is 18:15 UTC the same day. Rendering the UTC fields would say
  // "6:15 pm", losing both the hour and the meaning of the date.
  assert.equal(formatISTDateTime("2026-06-15T23:45:00+05:30"), "15 Jun 2026, 11:45 pm");
});

test("an early-morning IST timestamp does not roll forward from the previous UTC day", () => {
  // 00:30 IST on the 16th is 19:00 UTC on the 15th — the mirror of the case
  // above, and the one that would show a customer yesterday's date.
  assert.equal(formatISTDateTime("2026-06-16T00:30:00+05:30"), "16 Jun 2026, 12:30 am");
});

test("midnight and noon are 12, not 0", () => {
  assert.equal(formatISTTime("2026-06-15T00:00:00+05:30"), "12:00 am");
  assert.equal(formatISTTime("2026-06-15T12:00:00+05:30"), "12:00 pm");
  assert.equal(formatISTTime("2026-06-15T12:01:00+05:30"), "12:01 pm");
  assert.equal(formatISTTime("2026-06-15T13:05:00+05:30"), "1:05 pm");
});

test("a bare date does not slip to the previous day", () => {
  // new Date("2026-06-16") is UTC midnight, which is the 15th in any zone
  // behind UTC. Anchoring to IST is what keeps this on the 16th.
  assert.equal(formatISTDate("2026-06-16"), "16 Jun 2026");
  assert.equal(formatISTDate("2026-01-01"), "1 Jan 2026");
  assert.equal(formatISTDate("2026-12-31"), "31 Dec 2026");
});

test("a bare date has no invented time of day", () => {
  assert.equal(formatISTDateTime("2026-06-16"), "16 Jun 2026, 12:00 am");
  // The delivery day is a DATE. formatISTDate is what screens must use for it.
  assert.equal(formatISTDate("2026-06-16"), "16 Jun 2026");
});

test("the timeline dates from CLAUDE.md §6.1 render as written", () => {
  const cases: [placedAt: string, processingAt: string, deliveryDay: string, expected: string][] = [
    ["2026-06-15T09:00:00+05:30", "2026-06-15T16:00:00+05:30", "2026-06-16", "2026-06-17"],
    ["2026-06-15T15:59:59+05:30", "2026-06-15T16:00:00+05:30", "2026-06-16", "2026-06-17"],
    ["2026-06-15T16:00:00+05:30", "2026-06-16T16:00:00+05:30", "2026-06-17", "2026-06-18"],
    ["2026-06-15T23:45:00+05:30", "2026-06-16T16:00:00+05:30", "2026-06-17", "2026-06-18"],
  ];

  const rendered = cases.map(([placed, processing, delivery, expected]) => [
    formatISTDateTime(placed),
    formatISTDateTime(processing),
    formatISTDate(delivery),
    formatISTDate(expected),
  ]);

  assert.deepEqual(rendered, [
    ["15 Jun 2026, 9:00 am", "15 Jun 2026, 4:00 pm", "16 Jun 2026", "17 Jun 2026"],
    ["15 Jun 2026, 3:59 pm", "15 Jun 2026, 4:00 pm", "16 Jun 2026", "17 Jun 2026"],
    ["15 Jun 2026, 4:00 pm", "16 Jun 2026, 4:00 pm", "17 Jun 2026", "18 Jun 2026"],
    ["15 Jun 2026, 11:45 pm", "16 Jun 2026, 4:00 pm", "17 Jun 2026", "18 Jun 2026"],
  ]);
});

test("an unparseable value is shown as the server sent it", () => {
  // Wrong-looking is recoverable; "Invalid Date" on a customer's screen is
  // alarming and tells them nothing.
  assert.equal(formatISTDateTime("not a date"), "not a date");
  assert.equal(formatISTDate(""), "");
  assert.equal(toISTParts("2026-13-01"), null);
});

test("toISTParts reports the wall clock, not the epoch", () => {
  assert.deepEqual(toISTParts("2026-06-15T10:30:00Z"), {
    year: 2026,
    month: 6,
    day: 15,
    hour: 16,
    minute: 0,
    second: 0,
  });
});

test("istTodayISO reads the IST calendar day, not the device's", () => {
  // 19:30 UTC on the 15th is 01:00 IST on the 16th. A phone in London would
  // otherwise call this the 15th and label the sheet with yesterday.
  assert.equal(istTodayISO(new Date("2026-06-15T19:30:00Z")), "2026-06-16");
  assert.equal(istTodayISO(new Date("2026-06-15T18:29:00Z")), "2026-06-15");
  assert.equal(istTodayISO(new Date("2026-01-01T00:00:00Z")), "2026-01-01");
  // 31 Dec 20:00 UTC is 1 Jan IST — the year rolls too.
  assert.equal(istTodayISO(new Date("2025-12-31T20:00:00Z")), "2026-01-01");
});
