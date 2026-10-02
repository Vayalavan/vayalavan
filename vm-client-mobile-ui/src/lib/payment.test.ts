/**
 * Binds this app's payment copy and phases to the web storefront's.
 *
 * A customer can place an order on the phone and open the same order in a
 * browser while the webhook is still in flight. If the two apps word that
 * moment differently — one saying the payment is confirmed, the other still
 * asking for money — the customer has no way to tell which is true.
 *
 * The two packages cannot share code (CLAUDE.md §2: the mobile apps are
 * outside the npm workspaces), so this is the same arrangement `tokens.test.ts`
 * uses for the palette: the duplicate is allowed, and a test asserts the copies
 * agree. Change the wording in one and this suite fails rather than shipping a
 * contradiction.
 *
 * Read as source text rather than imported: vm-client-ui's module pulls in
 * React and `import.meta.env`, neither of which loads in plain Node.
 */
import { test } from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";

import { PAYMENT_COPY, paymentCopy, type PaymentPhase } from "./paymentCopy";

/** Resolved from this file, so the suite passes from any working directory. */
const WEB_PAYMENT_PATH = path.resolve(
  __dirname,
  "../../../vm-client-ui/src/lib/payment.ts",
);

const webSource = fs.readFileSync(WEB_PAYMENT_PATH, "utf8");

/**
 * Pulls the string literal a key is assigned in the web module's PAYMENT_COPY.
 * Handles both the single-line entries and the concatenated multi-line one.
 */
function webCopyFor(key: string): string {
  const block = webSource.slice(webSource.indexOf("PAYMENT_COPY"));
  const start = block.indexOf(`${key}:`);
  assert.notEqual(start, -1, `vm-client-ui's PAYMENT_COPY has no "${key}"`);

  // From the key to the next entry (or the closing brace).
  const rest = block.slice(start + key.length + 1);
  const end = rest.search(/,\n(?:\s{2}\w+:|};)/);
  const raw = rest.slice(0, end === -1 ? rest.length : end);

  // Join the concatenated pieces of a wrapped entry into one string.
  const pieces = raw.match(/"(?:[^"\\]|\\.)*"/g) ?? [];
  assert.notEqual(pieces.length, 0, `could not read the web copy for "${key}"`);
  return pieces.map((piece) => JSON.parse(piece) as string).join("");
}

test("every payment phase this app can be in has copy or is deliberately silent", () => {
  const phases: PaymentPhase[] = [
    "idle",
    "opening",
    "confirming",
    "paid",
    "slow",
    "error",
  ];

  for (const phase of phases) {
    const copy = paymentCopy(phase);
    if (phase === "idle" || phase === "error") {
      // `idle` has nothing to say and `error` carries its own message; a line
      // here would override the reason the payment actually failed.
      assert.equal(copy, null, `${phase} should have no canned copy`);
    } else {
      assert.ok(
        copy !== null && copy.length > 0,
        `${phase} would render a blank status line`,
      );
    }
  }
});

test("the copy matches vm-client-ui word for word", () => {
  for (const [phase, copy] of Object.entries(PAYMENT_COPY)) {
    assert.equal(
      copy,
      webCopyFor(phase),
      `"${phase}" is worded differently in the app and the web storefront`,
    );
  }
});

test("both apps know the same set of phases", () => {
  // A phase added to one and not the other is the same class of bug as
  // divergent wording: one surface would handle a state the other drops on
  // the floor.
  const webPhases = new Set(
    (webSource
      .slice(
        webSource.indexOf("export type PaymentPhase"),
        webSource.indexOf('| "error";') + '| "error";'.length,
      )
      .match(/"(\w+)"/g) ?? []
    ).map((quoted) => quoted.replace(/"/g, "")),
  );

  assert.deepEqual(
    webPhases,
    new Set(["idle", "opening", "confirming", "paid", "slow", "error"]),
  );
});

test("the confirmed message never claims more than the webhook has said", () => {
  // The one wording rule that is a correctness property, not a style choice:
  // "slow" means the money was captured but our order has NOT moved yet, so it
  // must not be worded as a completed order (CLAUDE.md §6.4 — the webhook is
  // the source of truth).
  assert.match(PAYMENT_COPY.slow, /went through/);
  assert.doesNotMatch(PAYMENT_COPY.slow, /confirmed/);
  assert.match(PAYMENT_COPY.paid, /confirmed/);
});
