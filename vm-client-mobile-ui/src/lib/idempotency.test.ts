/**
 * A repeated idempotency key does not fail loudly — it silently returns the
 * customer their PREVIOUS order and takes no new one, which is the kind of bug
 * that surfaces as "I ordered and nothing came". Worth a test.
 */
import { test } from "node:test";
import assert from "node:assert/strict";

import { newIdempotencyKey } from "./idempotency";

test("keys are unique even when generated in the same millisecond", () => {
  // Tighter than the request-id test: these are generated in a burst only
  // when something is retrying, which is exactly when a collision would land
  // on the same customer.
  const keys = new Set(Array.from({ length: 10_000 }, newIdempotencyKey));
  assert.equal(keys.size, 10_000);
});

test("keys are recognisable and within the server's length limit", () => {
  const key = newIdempotencyKey();
  // vm-orders-api rejects anything over 200 characters.
  assert.ok(key.length <= 200, `key is ${key.length} characters`);
  assert.match(key, /^mob-order-/);
});
