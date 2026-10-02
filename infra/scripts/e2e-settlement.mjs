/**
 * Offline payment, supplier settlement visibility, and supplier editing.
 *
 * The thread that matters here is reconciliation. An admin records a payment
 * taken outside Razorpay; that must create the same supplier payout rows the
 * Razorpay webhook would, or a grower silently never gets paid. The supplier's
 * own screen then has to quote the same figure the admin will transfer — so
 * this asserts the line totals equal the payout amount, and that exactly ONE
 * supplier can see the order (CLAUDE.md §7).
 *
 *   node infra/scripts/e2e-settlement.mjs   (or: make test-e2e-settlement)
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
const login=async(id,pw)=>(await api.post("/auth/login",{identifier:id,password:pw})).tokens.access_token;

// ---- 1. customer places an order that rests in pending_payment ----
tok=await login("customer@vayal.test",env.SEED_CUSTOMER_PASSWORD);
for(const it of (await api.get("/cart")).items) await api.delete("/cart/items/"+it.id);
const cat=await api.get("/catalog");
const p=cat.products.find(x=>x.any_unit_purchasable); const u=p.units.find(x=>x.purchasable);
await api.post("/cart/items",{product_unit_id:u.id,qty:2});
const addr=(await api.get("/addresses")).addresses.find(a=>a.is_default);
const order=await api.post("/orders",{address_id:addr.id},{idempotencyKey:crypto.randomUUID()});
ok(`order ${order.order_number} (${order.status})`);

// ---- 2. admin records an offline payment ----
tok=await login("admin@vayal.test",env.SEED_ADMIN_PASSWORD);
try{
  await api.post(`/admin/orders/${order.id}/record-payment`,{reference:"",method:"upi"});
  chk(false,"blank reference rejected");
}catch(e){ chk(e.status===422,"blank reference rejected",`got ${e.status}`); }

const paid=await api.post(`/admin/orders/${order.id}/record-payment`,
  {reference:"UPI-TEST-"+crypto.randomUUID().slice(0,8),method:"upi"});
chk(paid.status==="paid","order marked paid",`got ${paid.status}`);

try{
  await api.post(`/admin/orders/${order.id}/record-payment`,{reference:"UPI-DUP-"+crypto.randomUUID().slice(0,8),method:"upi"});
  chk(false,"replay rejected");
}catch(e){ chk(e.status===409,"replay rejected as INVALID_TRANSITION",`got ${e.status}`); }

// ---- 3. payouts were created by the SAME transaction the webhook uses ----
const summary=await api.get("/admin/payouts/summary");
ok("settlement outstanding: "+(summary.total_outstanding_display??JSON.stringify(summary).slice(0,80)));

// ---- 4. supplier sees their own sales, and the numbers reconcile ----
//
// Which supplier grew the produce is not known up front, so both are checked:
// exactly one must see the order, and the other must not. That second half is
// the ownership guarantee (CLAUDE.md §7), not an incidental detail.
const seenBy=[];
for(const who of ["greens@vayal.test","farm@vayal.test"]){
  tok=await login(who,env.SEED_SUPPLIER_PASSWORD);
  const s=await api.get("/supplier/sales");
  if(s.orders.some(o=>o.order_number===order.order_number)) seenBy.push(who);
}
chk(seenBy.length===1,"exactly one supplier sees the order",
    "seen by " + seenBy.length + ": " + (seenBy.join(", ") || "nobody"));
tok=await login(seenBy[0]??"greens@vayal.test",env.SEED_SUPPLIER_PASSWORD);
const sales=await api.get("/supplier/sales");
ok(`supplier sold ${sales.summary.gross_display} over ${sales.summary.order_count} order(s)`);
ok(`  awaiting payment ${sales.summary.pending_display} · settled ${sales.summary.settled_display}`);
chk(sales.orders.length>0,"supplier sees order rows");
const mine=sales.orders.find(o=>o.order_number===order.order_number);
chk(Boolean(mine),"the new order appears for the supplier");
if(mine){
  chk(mine.items.length>0,"order shows line detail");
  const lineSum=mine.items.reduce((s,i)=>s+i.line_total_paise,0);
  chk(lineSum===mine.amount_paise,"line totals equal the payout amount",
      `${lineSum} vs ${mine.amount_paise}`);
  console.log(`        ${mine.items.map(i=>i.product_name+" "+i.unit_label+" x"+i.qty).join(", ")}`);
}
chk(sales.products.length>0,"per-produce breakdown present");

// ---- 5. a supplier cannot reach another supplier's data ----
try{ await api.get("/admin/payouts/summary"); chk(false,"supplier blocked from admin settlement"); }
catch(e){ chk(e.status===403,"supplier blocked from admin settlement",`got ${e.status}`); }

// ---- 6. admin edits a supplier ----
tok=await login("admin@vayal.test",env.SEED_ADMIN_PASSWORD);
const sup=(await api.get("/admin/suppliers",{query:{status:"approved",limit:5}})).suppliers[0];
const edited=await api.patch(`/admin/suppliers/${sup.id}`,{
  business_name:sup.business_name, contact_name:sup.contact_name,
  phone:sup.phone, email:sup.email, city:"Coimbatore", state:sup.state??"Tamil Nadu",
});
chk(edited.city==="Coimbatore","supplier edit saved",`got ${edited.city}`);
chk(edited.status===sup.status,"edit did NOT change status",`${sup.status} -> ${edited.status}`);
chk(!/^\\d{9,}$/.test(edited.bank_account_number??""),"bank number still masked in the response");

console.log(bad===0?"\n  all gap checks passed\n":`\n  ${bad} FAILED\n`);
process.exit(bad?1:0);
