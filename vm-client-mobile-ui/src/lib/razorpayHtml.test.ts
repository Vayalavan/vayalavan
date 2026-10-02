/**
 * The payment page is assembled by string concatenation into a <script> block,
 * and one of the values in it is a customer's own name. That makes escaping a
 * correctness property, not a style one: a name containing `</script>` must
 * not be able to end the block.
 *
 * These tests are pure — buildCheckoutPage takes a string and returns a
 * string, with no WebView involved.
 */
import { strict as assert } from "node:assert";
import { test } from "node:test";

import {
  buildCheckoutPage,
  jsonForScript,
  parseCheckoutMessage,
} from "./razorpayHtml";

const base = {
  keyId: "rzp_test_example",
  razorpayOrderId: "order_ExAmPlE123",
  orderNumber: "VM-260814-0042",
  merchantName: "Vayalavan",
  themeColor: "#2b6446",
  prefill: {},
};

test("jsonForScript escapes characters that could end the script block", () => {
  const encoded = jsonForScript("</script><img src=x onerror=alert(1)>");

  assert.ok(!encoded.includes("</script>"), "the closing tag survived");
  assert.ok(!encoded.includes("<"), "a raw < survived");
  assert.ok(!encoded.includes(">"), "a raw > survived");
  // Still valid JSON, and still the same string once parsed: escaping must not
  // corrupt the value, only its representation.
  assert.equal(
    JSON.parse(encoded),
    "</script><img src=x onerror=alert(1)>",
  );
});

test("jsonForScript escapes the JS line terminators", () => {
  const encoded = jsonForScript("before\u2028after\u2029end");

  assert.ok(!encoded.includes("\u2028"), "U+2028 survived");
  assert.ok(!encoded.includes("\u2029"), "U+2029 survived");
  assert.equal(JSON.parse(encoded), "before\u2028after\u2029end");
});

test("jsonForScript leaves ordinary text alone", () => {
  // The bug this guards against: escaping written with literal separator
  // characters in the source, which silently mangles every space instead.
  assert.equal(JSON.parse(jsonForScript("Priya R")), "Priya R");
  assert.equal(JSON.parse(jsonForScript("VM-260814-0042")), "VM-260814-0042");
});

test("a hostile customer name cannot break out of the page", () => {
  const html = buildCheckoutPage({
    ...base,
    prefill: { name: "</script><script>window.stolen=1</script>" },
  });

  // Exactly two script tags: the checkout.js include and our own block. The
  // hostile text still appears — as an escaped JSON string, which is data the
  // sheet prefills a field with, not markup. What must NOT appear is a third
  // <script>, or a closing tag that ends our block early.
  assert.equal(html.match(/<script/g)?.length, 2);
  assert.equal(html.match(/<\/script>/g)?.length, 2);

  const payload = html.slice(html.indexOf("var options ="));
  const injected = payload.slice(0, payload.indexOf("\n"));
  assert.ok(
    !injected.includes("<") && !injected.includes(">"),
    "raw angle brackets reached the script block",
  );
});

test("the page carries the provider order and key, and no amount", () => {
  const html = buildCheckoutPage(base);

  assert.ok(html.includes("order_ExAmPlE123"));
  assert.ok(html.includes("rzp_test_example"));
  // Amount comes from the provider order. If it ever appears here, the phone
  // has become able to state its own price (CLAUDE.md §6.4).
  assert.ok(!/"amount"/.test(html), "the page names an amount");
  assert.ok(!/"currency"/.test(html), "the page names a currency");
});

test("the sheet is themed from the token passed in, not a literal", () => {
  const html = buildCheckoutPage({ ...base, themeColor: "#123456" });

  assert.ok(html.includes("#123456"));
  assert.ok(!html.includes("#2b6446"), "a hardcoded brand hex leaked in");
});

test("parseCheckoutMessage accepts a well-formed success", () => {
  const message = parseCheckoutMessage(
    JSON.stringify({
      type: "success",
      razorpay_order_id: "order_1",
      razorpay_payment_id: "pay_1",
      razorpay_signature: "sig",
    }),
  );

  assert.deepEqual(message, {
    type: "success",
    razorpay_order_id: "order_1",
    razorpay_payment_id: "pay_1",
    razorpay_signature: "sig",
  });
});

test("parseCheckoutMessage rejects anything else", () => {
  // A bank's own page, or a partial payload, must not be read as a payment.
  assert.equal(parseCheckoutMessage("not json"), null);
  assert.equal(parseCheckoutMessage('"a string"'), null);
  assert.equal(parseCheckoutMessage(JSON.stringify({ type: "other" })), null);
  assert.equal(
    parseCheckoutMessage(JSON.stringify({ type: "success", razorpay_order_id: "o" })),
    null,
    "a success missing its signature was accepted",
  );
});

test("parseCheckoutMessage always gives a failure something to say", () => {
  const message = parseCheckoutMessage(JSON.stringify({ type: "failed" }));

  assert.equal(message?.type, "failed");
  assert.ok(
    message.type === "failed" && message.message.length > 0,
    "an empty failure message would render a blank alert",
  );
});
