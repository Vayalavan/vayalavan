/**
  * The cutoff countdown.
 *
 * The visible clock must show seconds at every magnitude: it already ticked
 * once a second, but with hours left only the minutes ever redrew, so it read
 * as a static label rather than time running out.
 *
 * The spoken version must NOT, or a screen reader would announce the banner
 * once a second for eleven hours.
 */
import { test } from "node:test";
import assert from "node:assert/strict";
import { formatRemaining, announceRemaining } from "../lib/countdown.js";

test("shows seconds even when hours remain", () => {
  // 11h 32m 18s — the case that looked frozen on screen.
  assert.equal(formatRemaining(11 * 3600 + 32 * 60 + 18), "11h 32m 18s");
});

test("pads minutes and seconds under an hour boundary", () => {
  assert.equal(formatRemaining(2 * 3600 + 5 * 60 + 3), "2h 05m 03s");
});

test("drops the hour once it is gone", () => {
  assert.equal(formatRemaining(45 * 60 + 9), "45m 09s");
});

test("drops the minute in the final stretch", () => {
  assert.equal(formatRemaining(47), "47s");
});

test("reads zero at the cutoff", () => {
  assert.equal(formatRemaining(0), "0s");
});

test("keeps a zero minute between hours and seconds", () => {
  // "2h 05s" would read as two hours and five seconds.
  assert.equal(formatRemaining(2 * 3600 + 5), "2h 00m 05s");
});

test("every second changes the rendered string", () => {
  // The actual regression: two adjacent seconds must not render identically,
  // or the counter looks stopped.
  const base = 11 * 3600 + 32 * 60;
  assert.notEqual(formatRemaining(base), formatRemaining(base + 1));
});

test("the spoken version is coarse, not per-second", () => {
  const base = 11 * 3600 + 32 * 60;
  assert.equal(announceRemaining(base), announceRemaining(base + 1));
});

test("spoken: hours", () => {
  assert.equal(announceRemaining(11 * 3600 + 32 * 60), "About 12 hours left");
});

test("spoken: an hour and change", () => {
  assert.equal(announceRemaining(3600 + 20 * 60), "About 1 hour and 20 minutes left");
});

test("spoken: minutes", () => {
  assert.equal(announceRemaining(9 * 60), "About 9 minutes left");
});

test("spoken: singular minute", () => {
  assert.equal(announceRemaining(60), "About 1 minute left");
});

test("spoken: the last stretch", () => {
  assert.equal(announceRemaining(20), "Less than a minute left");
  assert.equal(announceRemaining(0), "Less than a minute left");
});
