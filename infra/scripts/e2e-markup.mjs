/**
 * Admin markup.
 *
 * A grower sets a price; we add a PERCENTAGE of it on top; the customer pays
 * the sum. The rate is one number per product and scales with each pack, so
 * 30% is ₹300 on a ₹1,000 pack and ₹600 on a ₹2,000 one. Four things have to hold, and every one of them is a way to
 * lose money quietly if it does not:
 *
 *   1. the storefront charges supplier price + markup
 *   2. the grower is paid on THEIR price — never on ours
 *   3. the grower's own screens never show the markup, in any form
 *   4. the markup lands in admin earnings, once, as markup revenue
 *
 * (2) is the expensive one. Payouts are summed from order lines, and the line
 * total is what the customer paid — so without subtracting the markup, every
 * supplier is handed our margin and the books still balance.
 *
 *   node infra/scripts/e2e-markup.mjs   (or: make test-e2e-markup)
 */
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

const ROOT = join(dirname(fileURLToPath(import.meta.url)), "..", "..");
const { createApiClient } = await import(
  join(ROOT, "packages", "vm-ui-kit", "dist", "index.js")
);

const env = Object.fromEntries(readFileSync(join(ROOT, ".env"), "utf8").split("\n")
  .filter(l => l.includes("=") && !l.trimStart().startsWith("#"))
  .map(l => { const i = l.indexOf("="); return [l.slice(0, i).trim(), l.slice(i + 1).trim()]; }));

let tok = null;
const api = createApiClient({ baseUrl: env.VITE_API_BASE_URL, getAuthToken: () => tok });
let bad = 0;
const ok = m => console.log("  ok    " + m);
const chk = (c, m, d = "") => { c ? ok(m) : (bad++, console.log("  FAIL  " + m + (d ? " — " + d : ""))); };
const login = async (i, p) => (await api.post("/auth/login", { identifier: i, password: p })).tokens.access_token;

// 30%, the rate in the brief: ₹300 on a ₹1,000 pack and ₹600 on a ₹2,000 one.
const MARKUP_BPS = 3000;
const markupOn = paise => Math.floor((paise * MARKUP_BPS + 5000) / 10000);

// ---- find something on sale today -----------------------------------------

tok = await login("customer@vayal.test", env.SEED_CUSTOMER_PASSWORD);
const before = await api.get("/catalog");
const card = before.products.find(p => p.any_unit_purchasable);
if (!card) {
  console.log("  SKIP  nothing is on sale today");
  process.exit(0);
}
const unitBefore = card.units.find(u => u.purchasable);
ok(`targeting ${card.name} — ${unitBefore.label} at ${unitBefore.price_display}`);

// ---- the admin sets a markup ----------------------------------------------

tok = await login("admin@vayal.test", env.SEED_ADMIN_PASSWORD);
const adminBefore = (await api.get("/admin/products", { query: { limit: 100 } }))
  .products.find(p => p.id === card.id);
chk(adminBefore !== undefined, "the product is on the admin products screen");
chk(adminBefore.supplier_name !== "", "and names the grower it belongs to",
    `got "${adminBefore?.supplier_name}"`);

const originalBPS = adminBefore.markup_bps;
const supplierPricePaise = adminBefore.units.find(u => u.id === unitBefore.id).supplier_price_paise;

const saved = await api.put(`/admin/products/${card.id}/markup`,
                            { markup_bps: MARKUP_BPS });
chk(saved.markup_bps === MARKUP_BPS, `markup saved as ${MARKUP_BPS} bps`,
    `got ${saved.markup_bps}`);
chk(saved.markup_display === "30%", "and reads back as a percentage",
    saved.markup_display);

const savedUnit = saved.units.find(u => u.id === unitBefore.id);
const savedMarkup = markupOn(savedUnit.supplier_price_paise);
chk(savedUnit.customer_price_paise === savedUnit.supplier_price_paise + savedMarkup,
    "admin screen shows supplier price + markup as the customer price",
    `${savedUnit.supplier_price_paise} + ${savedMarkup} != ${savedUnit.customer_price_paise}`);

// PROPORTIONAL, not flat: a pack at twice the price takes twice the markup.
// This is the assertion that fails if anyone reverts to a fixed amount.
for (const u of saved.units) {
  chk(u.markup_paise === markupOn(u.supplier_price_paise),
      `${u.label}: ${u.supplier_price_display} + 30% = ${u.customer_price_display}`,
      `markup ${u.markup_paise}, want ${markupOn(u.supplier_price_paise)}`);
}
const priced = saved.units.filter(u => u.supplier_price_paise > 0);
if (priced.length > 1) {
  const [a, b] = [priced[0], priced[priced.length - 1]];
  chk(a.markup_paise !== b.markup_paise || a.supplier_price_paise === b.supplier_price_paise,
      "packs at different prices carry different markups",
      `${a.label} ${a.markup_paise} vs ${b.label} ${b.markup_paise}`);
}

// A bad rate must be refused rather than stored as something else.
for (const badValue of [-100, 10001, 12.5]) {
  let err = null;
  try { await api.put(`/admin/products/${card.id}/markup`, { markup_bps: badValue }); }
  catch (e) { err = e; }
  chk(err !== null, `refused markup_bps=${badValue}`, err ? "" : "accepted");
}

// ---- the storefront charges the marked-up price ----------------------------

tok = await login("customer@vayal.test", env.SEED_CUSTOMER_PASSWORD);
const after = await api.get("/catalog");
const cardAfter = after.products.find(p => p.id === card.id);
const unitAfter = cardAfter.units.find(u => u.id === unitBefore.id);

const packMarkup = markupOn(supplierPricePaise);
chk(unitAfter.price_paise === supplierPricePaise + packMarkup,
    "storefront price is supplier price + markup",
    `got ${unitAfter.price_paise}, want ${supplierPricePaise + packMarkup}`);
chk(JSON.stringify(cardAfter).includes("markup") === false,
    "and the storefront never mentions the markup at all");

const detail = await api.get(`/catalog/${card.id}`);
chk(detail.units.find(u => u.id === unitBefore.id).price_paise === unitAfter.price_paise,
    "the product page quotes the same price as the card");

// ---- buy one and check where the money went --------------------------------

for (const it of (await api.get("/cart")).items) await api.delete("/cart/items/" + it.id);
await api.post("/cart/items", { product_unit_id: unitAfter.id, qty: 2 });
const cart = await api.get("/cart");
const line = cart.items[0];
chk(line.unit_price_paise === supplierPricePaise + packMarkup,
    "the cart prices the line at the marked-up price");

const addr = (await api.get("/addresses")).addresses.find(a => a.is_default);
const order = await api.post("/orders", { address_id: addr.id },
                             { idempotencyKey: crypto.randomUUID() });
ok(`placed ${order.order_number} for ${order.total_display}`);

tok = await login("admin@vayal.test", env.SEED_ADMIN_PASSWORD);
await api.post(`/admin/orders/${order.id}/record-payment`,
               { reference: "MK-" + crypto.randomUUID().slice(0, 8), method: "upi" });

const detailed = await api.get("/admin/orders/" + order.id);
const supplierGross = detailed.supplier_subtotals.reduce((s, x) => s + x.amount_paise, 0);
const payout = detailed.payouts.reduce((s, x) => s + x.amount_paise, 0);
const orderMarkup = packMarkup * 2; // two packs

// The grower's share must be the customer subtotal MINUS our markup.
chk(supplierGross === order.subtotal_paise - orderMarkup,
    "the grower's share excludes the markup",
    `gross ${supplierGross}, subtotal ${order.subtotal_paise}, markup ${orderMarkup}`);
chk(payout < supplierGross, "and commission is then deducted from that",
    `payout ${payout}, gross ${supplierGross}`);
chk(payout < order.subtotal_paise - orderMarkup + 1,
    "the payout never includes a paise of markup");

// ---- the grower sees their own prices, and no markup ------------------------

const supplierId = detailed.supplier_subtotals[0].supplier_id;
const supplier = (await api.get("/admin/suppliers", { query: { limit: 100 } }))
  .suppliers.find(s => s.id === supplierId);

tok = await login(supplier.email, env.SEED_SUPPLIER_PASSWORD);
const sales = await api.get("/supplier/sales", { query: { range: "today" } });
const serialised = JSON.stringify(sales);
chk(!serialised.includes("markup"), "the supplier's sales screen never mentions markup");

// The supplier's line carries a formatted unit price and a line total in
// paise — no unit_price_paise — so both are checked in the terms they arrive
// in. This is the assertion that would catch a payout computed on our price.
const soldOrder = sales.orders.find(o => o.order_number === order.order_number);
if (soldOrder) {
  const soldLine = soldOrder.items.find(i => i.product_name === card.name);
  chk(soldLine !== undefined, "the sale appears on the supplier's screen");

  if (soldLine) {
    chk(soldLine.line_total_paise === supplierPricePaise * 2,
        "the supplier's line total is THEIR price times quantity",
        `got ${soldLine.line_total_paise}, want ${supplierPricePaise * 2}`);
    chk(soldLine.line_total_paise !== (supplierPricePaise + packMarkup) * 2,
        "and is not the marked-up total the customer paid");
  }

  chk(soldOrder.amount_paise === supplierPricePaise * 2,
      "the order's amount on their screen excludes the markup",
      `got ${soldOrder.amount_paise}, want ${supplierPricePaise * 2}`);
}

const products = await api.get("/products", { query: { limit: 100 } });
chk(!JSON.stringify(products).includes("markup"),
    "and the supplier's own product list carries no markup field");

// ---- the markup shows up in admin earnings ---------------------------------

tok = await login("admin@vayal.test", env.SEED_ADMIN_PASSWORD);
const dash = await api.get("/admin/dashboard", { query: { range: "today" } });
chk(dash.earnings_breakdown.markup_paise >= orderMarkup,
    "markup revenue reaches the dashboard",
    `got ${dash.earnings_breakdown.markup_paise}, expected at least ${orderMarkup}`);

const b = dash.earnings_breakdown;
const sum = b.platform_fee_paise + b.supplier_commission_paise +
            b.delivery_margin_paise + b.markup_paise;
chk(sum === dash.earnings_today_paise,
    "and total earnings is exactly its four parts",
    `parts ${sum} != total ${dash.earnings_today_paise}`);

// ---- put it back -----------------------------------------------------------

await api.put(`/admin/products/${card.id}/markup`, { markup_bps: originalBPS });
ok(`restored ${card.name} to ${originalBPS / 100}%`);

console.log(bad === 0 ? "\n  markup verified end to end\n" : `\n  ${bad} FAILED\n`);
process.exit(bad ? 1 : 0);
