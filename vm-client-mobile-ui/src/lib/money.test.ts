/**
 * Money is CLAUDE.md rule 1 and rule 9's first named test target.
 *
 * The same cases the supplier app and the web UIs assert, run here so the
 * three copies of this formatting cannot drift. A customer who sees ₹45.50 on
 * their phone, ₹45.5 on the web and ₹45.50 on the invoice has been given three
 * reasons to distrust all three.
 */
import { test } from "node:test";
import assert from "node:assert/strict";

import { formatGrams, formatPaise } from "./money";

test("formatPaise renders whole and fractional rupees", () => {
  assert.equal(formatPaise(0), "₹0.00");
  assert.equal(formatPaise(5), "₹0.05");
  assert.equal(formatPaise(50), "₹0.50");
  assert.equal(formatPaise(100), "₹1.00");
  assert.equal(formatPaise(4550), "₹45.50");
});

test("formatPaise groups digits the Indian way", () => {
  assert.equal(formatPaise(100000), "₹1,000.00");
  assert.equal(formatPaise(120050), "₹1,200.50");
  // 12,34,567 — pairs above the last three, not the Western 1,234,567.
  assert.equal(formatPaise(123456700), "₹12,34,567.00");
  assert.equal(formatPaise(1000000000), "₹1,00,00,000.00");
});

test("formatPaise keeps the sign in front of the symbol", () => {
  // A refund line, the only place a customer sees one.
  assert.equal(formatPaise(-4550), "-₹45.50");
});

test("formatPaise never loses a paise to floating point", () => {
  // The trap this formatter exists to avoid: `45.50 * 100` is
  // 4549.999999999999, and a total built that way undercharges by a paise
  // per line, forever. Integer arithmetic only (CLAUDE.md rule 1).
  for (const paise of [1, 5, 50, 99, 100, 4550, 13050, 123456, 99999999]) {
    const rupees = Math.floor(paise / 100);
    const remainder = paise % 100;
    assert.match(formatPaise(paise), new RegExp(`\\.${String(remainder).padStart(2, "0")}$`));
    assert.ok(formatPaise(paise).includes(String(rupees % 1000)));
  }
});

test("formatGrams speaks kilos above 1 kg and grams below", () => {
  assert.equal(formatGrams(250), "250 g");
  assert.equal(formatGrams(999), "999 g");
  assert.equal(formatGrams(1000), "1 kg");
  assert.equal(formatGrams(3000), "3 kg");
  assert.equal(formatGrams(1500), "1.50 kg");
  assert.equal(formatGrams(0), "0 g");
});
