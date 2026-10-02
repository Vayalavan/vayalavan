/**
 * The 4pm auto-processing job.
 *
 * An order's processing_at is stamped at placement as 4pm IST of its processing
 * date (CLAUDE.md §6.1), and the processor moves it to `processed` once that
 * instant has passed — with no admin action at all. This is the only guard on
 * that behaviour: it is a background job, so nothing else notices if it stops
 * working, and the first sign would be a customer asking where their food is.
 *
 * "4pm arriving" is simulated by moving the order's own cutoff into the past,
 * rather than by waiting for an actual afternoon.
 *
 *   node infra/scripts/e2e-processor.mjs   (or: make test-e2e-processor)
 */
import { readFileSync } from "node:fs";
import { execSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

const ROOT = join(dirname(fileURLToPath(import.meta.url)), "..", "..");
const { createApiClient } = await import(
  join(ROOT, "packages", "vm-ui-kit", "dist", "index.js")
);

/** Parsed, never sourced — .env holds values with spaces, `&` and `<`. */
const env = Object.fromEntries(
  readFileSync(join(ROOT, ".env"), "utf8")
    .split("\n")
    .filter((l) => l.includes("=") && !l.trimStart().startsWith("#"))
    .map((l) => {
      const i = l.indexOf("=");
      return [l.slice(0, i).trim(), l.slice(i + 1).trim()];
    }),
);

let token = null;
const api = createApiClient({
  baseUrl: env.VITE_API_BASE_URL,
  getAuthToken: () => token,
});

let failures = 0;
const ok = (m) => console.log(`  ok    ${m}`);
const info = (m) => console.log(`        ${m}`);
function check(condition, message, detail = "") {
  if (condition) ok(message);
  else {
    failures += 1;
    console.log(`  FAIL  ${message}${detail ? ` — ${detail}` : ""}`);
  }
}

const psql = (sql) =>
  execSync(
    `docker exec vayal-postgres psql -U ${env.POSTGRES_USER} -d ${env.POSTGRES_DB ?? "vayal"} ` +
      `-At -c "${sql.replace(/"/g, '\\"')}"`,
  )
    .toString()
    .trim();

const login = async (identifier, password) =>
  (await api.post("/auth/login", { identifier, password })).tokens.access_token;

// The processor's own poll interval decides how long we may have to wait.
const intervalSeconds = Number(env.ORDER_PROCESSOR_INTERVAL_SECONDS ?? 60);
// Generous headroom: a tick can land just before the cutoff is moved.
const waitSeconds = intervalSeconds * 2 + 15;

console.log(
  `\n  processor interval ${intervalSeconds}s, so waiting up to ${waitSeconds}s\n`,
);

// --- place an order and pay it, leaving it in `paid` ------------------------
token = await login("customer@vayal.test", env.SEED_CUSTOMER_PASSWORD);
for (const item of (await api.get("/cart")).items) {
  await api.delete(`/cart/items/${item.id}`);
}

const catalog = await api.get("/catalog");
const product = catalog.products.find((p) => p.any_unit_purchasable);
if (!product) {
  console.log("\n  SKIP  nothing purchasable today — seed availability first\n");
  process.exit(0);
}
const unit = product.units.find((u) => u.purchasable);

await api.post("/cart/items", { product_unit_id: unit.id, qty: 1 });
const address = (await api.get("/addresses")).addresses.find((a) => a.is_default);
const order = await api.post(
  "/orders",
  { address_id: address.id },
  { idempotencyKey: crypto.randomUUID() },
);

token = await login("admin@vayal.test", env.SEED_ADMIN_PASSWORD);
await api.post(`/admin/orders/${order.id}/record-payment`, {
  reference: `PROC-${crypto.randomUUID().slice(0, 8)}`,
  method: "upi",
});
ok(`${order.order_number} is paid, awaiting its cutoff`);

// --- it must NOT be processed while its cutoff is in the future -------------
const before = psql(
  `SELECT status FROM orders.orders WHERE id = '${order.id}'`,
);
check(before === "paid", "still paid — its cutoff has not passed", `got ${before}`);

// --- simulate 4pm arriving --------------------------------------------------
psql(
  `UPDATE orders.orders SET processing_at = now() - interval '1 minute' ` +
    `WHERE id = '${order.id}'`,
);
info("cutoff moved into the past (4pm has 'arrived')");

let status = before;
for (let elapsed = 0; elapsed < waitSeconds; elapsed += 1) {
  await new Promise((resolve) => setTimeout(resolve, 1000));
  status = psql(`SELECT status FROM orders.orders WHERE id = '${order.id}'`);
  if (status === "processed") {
    info(`picked up after ~${elapsed + 1}s`);
    break;
  }
}

check(
  status === "processed",
  "the job processed it with no admin action",
  `still ${status} after ${waitSeconds}s`,
);

// --- and it stops there -----------------------------------------------------
//
// The processor's only job is paid -> processed. Dispatch means a parcel
// physically left with a courier, which no job can observe, so a later tick
// must not carry the order any further.
await new Promise((resolve) => setTimeout(resolve, 2000));
const settled = psql(`SELECT status FROM orders.orders WHERE id = '${order.id}'`);
check(
  settled === "processed",
  "it stays processed — the job never dispatches",
  `moved on to ${settled}`,
);

console.log(
  failures === 0
    ? "\n  4pm auto-processing verified\n"
    : `\n  ${failures} check(s) FAILED\n`,
);
process.exit(failures === 0 ? 0 : 1);
