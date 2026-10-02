/**
 * End-to-end cart and checkout, against a running seeded stack.
 *
 * JavaScript rather than Python like its siblings, and deliberately so: it
 * drives the SAME built @vayal/ui-kit client the three UIs use, pointed at the
 * same VITE_API_BASE_URL they are served with.
 *
 * That is not incidental. A bug in the client's URL building sent every UI
 * request to the gateway root while the Python suites — which build full URLs
 * themselves — passed. Every screen was broken and every test was green. This
 * script closes that gap: if the shared client cannot reach the API, this
 * fails.
 *
 * Also covers the idempotent double-submit of POST /orders (CLAUDE.md rule 6),
 * which until now had only ever been checked by hand.
 *
 *   node infra/scripts/e2e-cart.mjs      (or: make test-e2e-cart)
 */
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

const ROOT = join(dirname(fileURLToPath(import.meta.url)), "..", "..");

const { createApiClient } = await import(
  join(ROOT, "packages", "vm-ui-kit", "dist", "index.js")
);

/** Parsed, never sourced — values contain spaces, `&` and `<`. */
function loadEnv() {
  return Object.fromEntries(
    readFileSync(join(ROOT, ".env"), "utf8")
      .split("\n")
      .filter((line) => line.includes("=") && !line.trimStart().startsWith("#"))
      .map((line) => {
        const i = line.indexOf("=");
        return [line.slice(0, i).trim(), line.slice(i + 1).trim()];
      }),
  );
}

let failures = 0;
const pass = (m) => console.log(`  ok    ${m}`);
const info = (m) => console.log(`        ${m}`);
function check(condition, message, detail = "") {
  if (condition) pass(message);
  else {
    failures += 1;
    console.log(`  FAIL  ${message}${detail ? ` — ${detail}` : ""}`);
  }
}

const env = loadEnv();
let token = null;
const api = createApiClient({
  baseUrl: env.VITE_API_BASE_URL,
  getAuthToken: () => token,
});

console.log(`\nbase url: ${env.VITE_API_BASE_URL}\n`);

const login = await api.post("/auth/login", {
  identifier: env.SEED_CUSTOMER_EMAIL ?? "customer@vayal.test",
  password: env.SEED_CUSTOMER_PASSWORD,
});
token = login.tokens.access_token;
pass(`signed in as ${login.user.role}`);

// A cart left over from a previous run would make the totals below unreadable.
for (const item of (await api.get("/cart")).items) {
  await api.delete(`/cart/items/${item.id}`);
}

const catalog = await api.get("/catalog");
const product = catalog.products.find((p) => p.any_unit_purchasable);
if (!product) {
  console.log("\n  SKIP  nothing is purchasable today — seed availability first\n");
  process.exit(0);
}
const unit = product.units.find((u) => u.purchasable);

await api.post("/cart/items", { product_unit_id: unit.id, qty: 1 });
pass(`added ${product.name} ${unit.label} at ${unit.price_display}`);

let cart = await api.get("/cart");
check(cart.items.length === 1, "cart has one line");

await api.patch(`/cart/items/${cart.items[0].id}`, { qty: 2 });
cart = await api.get("/cart");
check(cart.items[0].qty === 2, "quantity updated to 2");
info(
  `${cart.subtotal_display} + ${cart.platform_fee_display} fee + ` +
    `${cart.delivery_fee_display} delivery = ${cart.total_display}`,
);

// CLAUDE.md §6.2, recomputed independently in integer paise. Floating point
// would defeat the point of checking.
const bps = Number(env.PLATFORM_FEE_BPS ?? 300);
const expectedFee = Math.floor((cart.subtotal_paise * bps + 5000) / 10000);
const expectedTotal = cart.subtotal_paise + expectedFee + cart.delivery_fee_paise;

check(
  cart.items[0].line_total_paise === cart.items[0].unit_price_paise * 2,
  "line total is unit price times quantity",
);
check(
  cart.platform_fee_paise === expectedFee,
  `platform fee is half-up ${bps} bps`,
  `got ${cart.platform_fee_paise}, want ${expectedFee}`,
);
check(
  cart.total_paise === expectedTotal,
  "total equals subtotal + fee + delivery",
  `got ${cart.total_paise}, want ${expectedTotal}`,
);
check(cart.checkoutable, "cart reports itself checkoutable");

const addresses = (await api.get("/addresses")).addresses;
const address = addresses.find((a) => a.is_default) ?? addresses[0];
check(Boolean(address), "customer has a delivery address");

// The same key twice is exactly what a double-tap or a retry-after-timeout
// looks like from the server's side.
const idempotencyKey = crypto.randomUUID();
const first = await api.post("/orders", { address_id: address.id }, { idempotencyKey });
pass(`placed ${first.order_number} (${first.status})`);
check(
  first.status === "pending_payment",
  "order rests in pending_payment (Razorpay Checkout is deferred)",
  `got ${first.status}`,
);

const second = await api.post("/orders", { address_id: address.id }, { idempotencyKey });
check(
  second.id === first.id,
  "replaying the idempotency key returns the same order",
  `got ${second.order_number}, want ${first.order_number}`,
);

const emptied = await api.get("/cart");
check(emptied.items.length === 0, "cart is empty after checkout");

const detail = await api.get(`/orders/${first.id}`);
check(
  Array.isArray(detail.milestones) && detail.milestones.length === 3,
  "timeline has exactly three milestones",
);
check(detail.milestones?.[0]?.completed === true, "'Order received' is complete");
for (const m of detail.milestones ?? []) {
  info(`${m.completed ? "[x]" : "[ ]"} ${String(m.name).padEnd(16)} ${m.at}`);
}

console.log(
  failures === 0
    ? "\ncart e2e: all checks passed\n"
    : `\ncart e2e: ${failures} check(s) FAILED\n`,
);
process.exit(failures === 0 ? 0 : 1);
