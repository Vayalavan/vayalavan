/**
 * The commercial model: who pays what, and what we keep.
 *
 * Supplier commission is DEDUCTED from a grower's payout while the platform
 * fee is ADDED to the customer's total. They are the same percentage on
 * opposite sides of one order, which makes them easy to conflate and expensive
 * to get wrong — so this asserts the payout row is net of commission, that the
 * dashboard breakdown sums to its own total across all four revenue sources
 * (platform fee, commission, delivery margin, markup), and that the grower's
 * screen shows
 * gross, commission and net consistently.
 *
 *   node infra/scripts/e2e-rates.mjs   (or: make test-e2e-rates)
 */
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

const ROOT = join(dirname(fileURLToPath(import.meta.url)), "..", "..");
const { createApiClient } = await import(
  join(ROOT, "packages", "vm-ui-kit", "dist", "index.js")
);

const env=Object.fromEntries(readFileSync(join(ROOT, ".env"),"utf8").split("\n")
  .filter(l=>l.includes("=")&&!l.trimStart().startsWith("#"))
  .map(l=>{const i=l.indexOf("=");return [l.slice(0,i).trim(),l.slice(i+1).trim()];}));
let tok=null; const api=createApiClient({baseUrl:env.VITE_API_BASE_URL,getAuthToken:()=>tok});
let bad=0; const ok=m=>console.log("  ok    "+m);
const chk=(c,m,d="")=>{c?ok(m):(bad++,console.log("  FAIL  "+m+(d?" — "+d:"")));};
const login=async(i,p)=>(await api.post("/auth/login",{identifier:i,password:p})).tokens.access_token;

// place + pay an order
tok=await login("customer@vayal.test",env.SEED_CUSTOMER_PASSWORD);
for(const it of (await api.get("/cart")).items) await api.delete("/cart/items/"+it.id);
const cat=await api.get("/catalog");
const p=cat.products.find(x=>x.any_unit_purchasable); const u=p.units.find(x=>x.purchasable);
await api.post("/cart/items",{product_unit_id:u.id,qty:1});
const cart=await api.get("/cart");
const addr=(await api.get("/addresses")).addresses.find(a=>a.is_default);
const order=await api.post("/orders",{address_id:addr.id},{idempotencyKey:crypto.randomUUID()});
ok(`order ${order.order_number} subtotal ${cart.subtotal_display}`);

tok=await login("admin@vayal.test",env.SEED_ADMIN_PASSWORD);
await api.post(`/admin/orders/${order.id}/record-payment`,{reference:"R-"+crypto.randomUUID().slice(0,8),method:"upi"});
ok("payment recorded");

// supplier payout must be net of the 3% commission
const det=await api.get("/admin/orders/"+order.id);
const supplierGross=det.supplier_subtotals.reduce((s,x)=>s+x.amount_paise,0);
const payoutTotal=det.payouts.reduce((s,x)=>s+x.amount_paise,0);
const bps=Number(env.SUPPLIER_COMMISSION_BPS);
const expected=supplierGross-Math.floor((supplierGross*bps+5000)/10000);
chk(payoutTotal===expected,`payout is net of ${bps/100}% commission`,
    `got ${payoutTotal}, want ${expected} (gross ${supplierGross})`);

// dashboard earnings
const dash=await api.get("/admin/dashboard");
const b=dash.earnings_breakdown;
ok(`earnings today ${dash.earnings_today}`);
console.log(`        platform ${b.platform_fee} (${b.platform_fee_rate}) + commission ${b.supplier_commission} (${b.supplier_commission_default_rate}) + delivery ${b.delivery_margin} (${b.delivery_margin_per_order} x ${b.paid_orders}) + markup ${b.markup}`);
// FOUR sources, not three. Markup — what we add to growers' prices — is the
// newest, and a breakdown that omits one source while the total includes it is
// exactly the kind of quiet discrepancy this file exists to catch.
const sum=b.platform_fee_paise+b.supplier_commission_paise+b.delivery_margin_paise+b.markup_paise;
chk(sum===dash.earnings_today_paise,"breakdown sums to the total",`${sum} vs ${dash.earnings_today_paise}`);
chk(b.delivery_margin_paise===Number(env.DELIVERY_MARGIN_PAISE)*b.paid_orders,
    "delivery margin is flat per paid order");

// supplier sees the deduction
tok=await login("greens@vayal.test",env.SEED_SUPPLIER_PASSWORD);
let sales=await api.get("/supplier/sales");
if(sales.orders.length===0){ tok=await login("farm@vayal.test",env.SEED_SUPPLIER_PASSWORD); sales=await api.get("/supplier/sales"); }
const c=sales.charges;
ok(`supplier sees: sold ${c.gross_display} − commission ${c.commission_display} (${c.commission_rate}) = ${c.net_display}`);
chk(c.gross_paise-c.commission_paise===c.net_paise,"gross − commission = net");
chk(c.commission_rate==="3%","rate shown as a percentage",c.commission_rate);

console.log(bad===0?"\n  all rate checks passed\n":`\n  ${bad} FAILED\n`);
process.exit(bad?1:0);
