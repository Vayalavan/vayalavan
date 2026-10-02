import { test } from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";

import { distinctProducts, MAX_THUMBNAILS, type ThumbnailItem } from "./orderThumbnails";

const WEB_COMPONENT = path.resolve(
  __dirname,
  "../../../vm-client-ui/src/components/OrderThumbnails.tsx",
);

function item(name: string, id?: string): ThumbnailItem {
  return id === undefined ? { product_name: name } : { product_name: name, product_id: id };
}

// Two pack sizes of the same produce is one product bought twice. Counting it
// twice would show the same photograph twice AND make "+2" a lie.
test("two pack sizes of one product count once", () => {
  const products = distinctProducts([
    item("Tomato", "p1"),
    item("Tomato", "p1"),
    item("Okra", "p2"),
  ]);
  assert.deepEqual(
    products.map((p) => p.product_name),
    ["Tomato", "Okra"],
  );
});

// Distinct produce that happens to arrive without ids must not collapse into
// one just because a field is missing.
test("different products without ids stay separate", () => {
  const products = distinctProducts([item("Tomato"), item("Okra"), item("Tomato")]);
  assert.deepEqual(
    products.map((p) => p.product_name),
    ["Tomato", "Okra"],
  );
});

test("order is preserved", () => {
  const products = distinctProducts([item("C", "3"), item("A", "1"), item("B", "2")]);
  assert.deepEqual(
    products.map((p) => p.product_name),
    ["C", "A", "B"],
  );
});

test("an order with no items shows no strip", () => {
  assert.deepEqual(distinctProducts([]), []);
});

// The overflow count is what is HIDDEN, not the total: five products show four
// photographs and "+1".
test("the overflow count is the remainder after the shown thumbnails", () => {
  const five = ["a", "b", "c", "d", "e"].map((id) => item(id.toUpperCase(), id));
  const products = distinctProducts(five);
  const shown = products.slice(0, MAX_THUMBNAILS);

  assert.equal(shown.length, 4);
  assert.equal(products.length - shown.length, 1);

  // And the cap only applies past it: four products show four and no chip.
  const four = distinctProducts(five.slice(0, 4));
  assert.equal(four.length - four.slice(0, MAX_THUMBNAILS).length, 0);
});

// The two storefronts must agree about where the strip stops. A phone showing
// "+1" beside a laptop showing "+2" for the same order is the kind of small
// disagreement that makes a customer doubt the rest of the figures.
test("the web card caps at the same number", () => {
  const source = fs.readFileSync(WEB_COMPONENT, "utf8");
  const match = source.match(/const MAX_THUMBNAILS = (\d+);/);
  assert.ok(match, "no MAX_THUMBNAILS found in the web OrderThumbnails");
  assert.equal(Number(match[1]), MAX_THUMBNAILS);
});
