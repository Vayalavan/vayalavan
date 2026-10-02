import { test } from "node:test";
import assert from "node:assert/strict";

import { chargeRuleText, dayOfMonth, displayDate, formatRupees, rupeesToPaise, weekdayOf } from "./walletFormat.js";

test("rupeesToPaise parses without floating point", () => {
  assert.equal(rupeesToPaise("500"), 50_000);
  assert.equal(rupeesToPaise("10.10"), 1_010);
  assert.equal(rupeesToPaise("10.5"), 1_050);
  assert.equal(rupeesToPaise("1,000"), 100_000);
  assert.equal(rupeesToPaise("₹250"), 25_000);
  assert.equal(rupeesToPaise(" 0.01 "), 1);
});

test("rupeesToPaise refuses what is not an amount", () => {
  for (const bad of ["", "abc", "10.123", "-5", "1e3", "5."]) {
    assert.equal(rupeesToPaise(bad), null, bad);
  }
});

test("formatRupees groups the Indian way and keeps paise only when there are some", () => {
  assert.equal(formatRupees(10_000), "₹100");
  assert.equal(formatRupees(12_345_600), "₹1,23,456");
  assert.equal(formatRupees(1_050), "₹10.50");
});

test("chargeRuleText reads the cutoff and the lead", () => {
  assert.equal(
    chargeRuleText({ cutoff_hour_ist: 16, charge_lead_minutes: 30 }),
    "3:30 pm, two days before each delivery",
  );
  assert.equal(
    chargeRuleText({ cutoff_hour_ist: 12, charge_lead_minutes: 60 }),
    "11:00 am, two days before each delivery",
  );
});

test("date helpers read the calendar date, not the browser's zone", () => {
  assert.equal(dayOfMonth("2026-10-31"), 31);
  assert.equal(weekdayOf("2026-10-05"), 1); // a Monday
  assert.equal(weekdayOf("2026-10-04"), 0); // a Sunday
});

test("displayDate formats a calendar date", () => {
  assert.equal(displayDate("2026-09-29"), "29 Sept 2026".replace("Sept", new Intl.DateTimeFormat("en-IN", { month: "short", timeZone: "UTC" }).format(new Date(Date.UTC(2026, 8, 29)))));
});
