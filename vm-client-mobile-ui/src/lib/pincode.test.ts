/**
 * Binds this app's delivery area to vm-ui-kit's.
 *
 * This app cannot import the kit at runtime (see the note in pincode.ts), so
 * the guarantee is enforced here instead: the test imports the kit's copy from
 * the repository and runs both implementations over the same table. An area
 * widened on the web and not here — or the reverse — fails this suite rather
 * than shipping an app that takes orders the couriers cannot deliver.
 *
 * The kit's pincode.ts has no imports of its own, so tsx loads it cleanly.
 */
import { test } from "node:test";
import assert from "node:assert/strict";

import {
  isServiceablePincode,
  DELIVERY_AREA_LABEL,
  PINCODE_AREA_ERROR,
} from "./pincode";
import {
  isServiceablePincode as kitIsServiceablePincode,
  DELIVERY_AREA_LABEL as KIT_AREA_LABEL,
  PINCODE_AREA_ERROR as KIT_AREA_ERROR,
} from "../../../packages/vm-ui-kit/src/format/pincode";

/** The same cases vm-go-common's serviceable package is written to. */
const CASES: ReadonlyArray<readonly [string, boolean]> = [
  // Bengaluru urban.
  ["560001", true],
  ["560999", true],
  ["561101", false], // Bangalore Rural
  ["562159", false], // Ramanagara
  ["570001", false], // Mysuru

  // Tamil Nadu, both ends of the band and a few inside it.
  ["600001", true], // Chennai
  ["600000", true],
  ["641001", true], // Coimbatore
  ["643001", true], // the Nilgiris
  ["643999", true],
  ["599999", false],
  ["644001", false],
  ["682001", false], // Kerala

  // Inside the band but not actually Tamil Nadu — accepted knowingly.
  ["605001", true], // Puducherry
  ["609602", true], // Karaikal

  // Shape.
  ["56000", false],
  ["5600012", false],
  ["060001", false],
  ["56000a", false],
  ["", false],
  ["   ", false],
  ["  560001  ", true],
  ["560 001", false],
  ["56000١", false],
];

test("the delivery area matches vm-ui-kit's", () => {
  for (const [pin, want] of CASES) {
    assert.equal(isServiceablePincode(pin), want, `isServiceablePincode(${JSON.stringify(pin)})`);
    assert.equal(
      kitIsServiceablePincode(pin),
      want,
      `the kit disagrees about ${JSON.stringify(pin)}`,
    );
  }
});

test("the customer-facing copy matches vm-ui-kit's", () => {
  // A phone that named a different area from the website would be describing
  // a different shop.
  assert.equal(DELIVERY_AREA_LABEL, KIT_AREA_LABEL);
  assert.equal(PINCODE_AREA_ERROR, KIT_AREA_ERROR);
});
