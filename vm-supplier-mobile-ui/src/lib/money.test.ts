/**
 * Money is CLAUDE.md rule 1 and rule 9's first named test target.
 *
 * The cases below are the ones that actually break: half-paise inputs, Indian
 * digit grouping, and the float trap — 45.50 has no exact float64
 * representation, so anything routed through parseFloat can land on 4549 paise
 * and undercharge a customer by a paise per unit, forever.
 */
import { test } from "node:test";
import assert from "node:assert/strict";

import {
  formatGrams,
  formatPaise,
  gramsToKg,
  kgToGrams,
  parseRupeesToPaise,
  previewRupees,
} from "./money";

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
  assert.equal(formatPaise(-4550), "-₹45.50");
});

test("parseRupeesToPaise is exact where a float is not", () => {
  // parseFloat("45.50") * 100 is 4549.999999999999 — this is the whole reason
  // the parser is hand-rolled.
  assert.equal(parseRupeesToPaise("45.50"), 4550);
  assert.equal(parseRupeesToPaise("0.07"), 7);
  assert.equal(parseRupeesToPaise("1.10"), 110);
  assert.equal(parseRupeesToPaise("8.20"), 820);
  assert.equal(parseRupeesToPaise("1234.56"), 123456);
});

test("parseRupeesToPaise pads a one-digit fraction", () => {
  assert.equal(parseRupeesToPaise("45.5"), 4550);
  assert.equal(parseRupeesToPaise(".5"), 50);
});

test("parseRupeesToPaise accepts the symbol, separators and whitespace", () => {
  assert.equal(parseRupeesToPaise("₹1,200.50"), 120050);
  assert.equal(parseRupeesToPaise("  130  "), 13000);
  assert.equal(parseRupeesToPaise("+45"), 4500);
});

test("parseRupeesToPaise refuses anything it would have to guess at", () => {
  for (const input of ["", "   ", "abc", "1.", "1.234", "1e3", "4 5", "₹", "-", "1.2.3", "."]) {
    assert.equal(parseRupeesToPaise(input), null, `expected ${JSON.stringify(input)} to be rejected`);
  }
});

test("previewRupees stays quiet until the amount is complete", () => {
  assert.equal(previewRupees(""), null);
  assert.equal(previewRupees("13"), "₹13.00");
  // Mid-keystroke: the supplier has typed the dot but not the paise yet.
  assert.equal(previewRupees("13."), null);
  assert.equal(previewRupees("13.5"), "₹13.50");
});

test("a price survives a round trip through the form", () => {
  // The edit screen shows (price_paise / 100).toFixed(2) and sends the string
  // back. Anything lost in that loop is money lost on every edit.
  for (const paise of [1, 5, 50, 99, 100, 4550, 13050, 123456, 99999999]) {
    const shown = (paise / 100).toFixed(2);
    assert.equal(parseRupeesToPaise(shown), paise, `round trip failed at ${paise} paise`);
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

test("kgToGrams converts what a grower types", () => {
  assert.equal(kgToGrams("40"), 40000);
  assert.equal(kgToGrams("12.5"), 12500);
  assert.equal(kgToGrams("0.25"), 250);
  assert.equal(kgToGrams("0"), 0);
  assert.equal(kgToGrams(" 5 "), 5000);
  // Rounded rather than truncated.
  assert.equal(kgToGrams("12.3456"), 12346);
});

test("kgToGrams refuses input that is not a quantity", () => {
  for (const input of ["", "   ", "abc", "-5", ".", "1,5", "5kg"]) {
    assert.equal(kgToGrams(input), null, `expected ${JSON.stringify(input)} to be rejected`);
  }
});

test("a declaration survives a round trip through the availability box", () => {
  // The sheet seeds each box with gramsToKg(total_grams); saving reads it back
  // with kgToGrams. A drift here would re-declare a different quantity than
  // the supplier is looking at.
  for (const grams of [0, 250, 1000, 12500, 40000, 12346, 999_000]) {
    assert.equal(kgToGrams(gramsToKg(grams)), grams, `round trip failed at ${grams} g`);
  }
});

test("gramsToKg does not print a trail of zeroes", () => {
  assert.equal(gramsToKg(40000), "40");
  assert.equal(gramsToKg(12500), "12.5");
  assert.equal(gramsToKg(250), "0.25");
  assert.equal(gramsToKg(0), "0");
});
