/**
 * The wallet helpers here must agree with the web's, input for input — the
 * same amount typed on a phone and in a browser must top up the same paise.
 */
import { test } from "node:test";
import assert from "node:assert/strict";
import path from "node:path";
import { pathToFileURL } from "node:url";

import * as mobile from "./walletFormat";

const WEB_PATH = path.resolve(__dirname, "../../../vm-client-ui/src/lib/walletFormat.ts");

async function web(): Promise<typeof mobile> {
  return (await import(pathToFileURL(WEB_PATH).href)) as typeof mobile;
}

test("rupeesToPaise agrees with the web", async () => {
  const w = await web();
  for (const input of ["500", "10.10", "10.5", "1,000", "₹250", " 0.01 ", "", "abc", "10.123", "-5", "5."]) {
    assert.equal(mobile.rupeesToPaise(input), w.rupeesToPaise(input), input);
  }
  assert.equal(mobile.rupeesToPaise("10.10"), 1_010);
});

test("formatting agrees with the web", async () => {
  const w = await web();
  for (const paise of [0, 1, 1_050, 10_000, 12_345_600]) {
    assert.equal(mobile.formatRupees(paise), w.formatRupees(paise));
  }
  for (const iso of ["2026-09-29", "2028-02-29", "2027-01-01"]) {
    assert.equal(mobile.displayDate(iso), w.displayDate(iso));
    assert.equal(mobile.weekdayOf(iso), w.weekdayOf(iso));
    assert.equal(mobile.dayOfMonth(iso), w.dayOfMonth(iso));
  }
  const options = { cutoff_hour_ist: 16, charge_lead_minutes: 30 };
  assert.equal(mobile.chargeRuleText(options), w.chargeRuleText(options));
  assert.equal(mobile.chargeRuleText(options), "3:30 pm, two days before each delivery");
});
