import assert from "node:assert/strict";
import { test } from "node:test";

import { gradeLabel, gradedName } from "./grade";

/**
 * The same cases @vayal/ui-kit's grade.ts and vm-go-common's produce package
 * are written to. This app cannot import the kit, so the agreement is asserted
 * here instead: a rule that hid "STD" on the web and printed it on a phone
 * would put a grade on screen that the customer was never offered.
 */
test("gradeLabel hides the two cases that are not a choice", () => {
  const cases: ReadonlyArray<[string | null | undefined, string]> = [
    ["XL2", "XL2"],
    ["  M2  ", "M2"],
    ["", ""],
    ["   ", ""],
    [null, ""],
    [undefined, ""],
    ["STD", ""],
    ["std", ""],
    [" Std ", ""],
    // Not the default, merely starting with it: a grower could name a grade
    // "STD2" and it would be theirs to see.
    ["STD2", "STD2"],
  ];
  for (const [input, want] of cases) {
    assert.equal(gradeLabel(input), want, `gradeLabel(${JSON.stringify(input)})`);
  }
});

test("gradedName appends the grade only when there is one", () => {
  assert.equal(gradedName("Pomegranate", "XL2"), "Pomegranate (XL2)");
  assert.equal(gradedName("Pomegranate", null), "Pomegranate");
  assert.equal(gradedName("Pomegranate", "STD"), "Pomegranate");
  assert.equal(gradedName("Tomato", " M "), "Tomato (M)");
});
