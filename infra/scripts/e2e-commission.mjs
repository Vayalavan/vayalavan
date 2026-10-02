/**
 * Per-supplier commission.
 *
 * A grower can be on a negotiated rate instead of the platform default. Three
 * things have to agree or a farmer is mispaid: what the admin saved, what the
 * payout row was written with, and what the supplier's own screen quotes back
 * to them. This asserts all three against the same order.
 *
 * The awkward case it guards is 0%: a real arrangement (a grower we take
 * nothing from) that must survive the round trip distinctly from "not set",
 * which means the platform default. A plain integer field would collapse the
 * two and silently start charging someone.
 *
 *   node infra/scripts/e2e-commission.mjs   (or: make test-e2e-commission)
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
const platformBps=Number(env.SUPPLIER_COMMISSION_BPS);

tok=await login("admin@vayal.test",env.SEED_ADMIN_PASSWORD);
const sups=(await api.get("/admin/suppliers",{query:{status:"approved",limit:10}})).suppliers;
const todayCat=await api.get("/catalog");
const sellingIDs=new Set(todayCat.products.filter(p=>p.any_unit_purchasable).map(p=>p.supplier_id));
const target=sups.find(s=>sellingIDs.has(s.id))??sups[0];
ok(`platform default ${platformBps/100}%; targeting ${target.business_name}`);
chk(target.commission_bps===null,"starts on the platform default (null)",`got ${target.commission_bps}`);

const base={business_name:target.business_name,contact_name:target.contact_name,
  phone:target.phone,email:target.email,city:target.city??"",state:target.state??""};

// set a negotiated rate
let up=await api.patch(`/admin/suppliers/${target.id}`,{...base,commission_bps:150});
chk(up.commission_bps===150,"1.5% saved",`got ${up.commission_bps}`);

// zero must survive as zero, not be read as "unset"
up=await api.patch(`/admin/suppliers/${target.id}`,{...base,commission_bps:0});
chk(up.commission_bps===0,"0% is stored as 0, not treated as unset",`got ${up.commission_bps}`);

// null clears back to the default
up=await api.patch(`/admin/suppliers/${target.id}`,{...base,commission_bps:null});
chk(up.commission_bps===null,"null clears the override");

// out of range refused
for(const bad_ of [-1,10001]){
  let err=null; try{ await api.patch(`/admin/suppliers/${target.id}`,{...base,commission_bps:bad_}); }catch(e){ err=e; }
  chk(err!==null,`refused commission_bps=${bad_}`,err?"":"accepted");
}

// ---- the payout must use the negotiated rate ----
await api.patch(`/admin/suppliers/${target.id}`,{...base,commission_bps:1000}); // 10%
ok("set "+target.business_name+" to 10%");

tok=await login("customer@vayal.test",env.SEED_CUSTOMER_PASSWORD);
for(const it of (await api.get("/cart")).items) await api.delete("/cart/items/"+it.id);
const cat=await api.get("/catalog");
const prod=cat.products.find(p=>p.supplier_id===target.id&&p.any_unit_purchasable);
if(!prod){
  tok=await login("admin@vayal.test",env.SEED_ADMIN_PASSWORD);
  await api.patch("/admin/suppliers/"+target.id,{...base,commission_bps:null});
  console.log("  SKIP  that supplier has nothing on sale today (rate restored)");
  process.exit(0);
}
const u=prod.units.find(x=>x.purchasable);
await api.post("/cart/items",{product_unit_id:u.id,qty:1});
const addr=(await api.get("/addresses")).addresses.find(a=>a.is_default);
const o=await api.post("/orders",{address_id:addr.id},{idempotencyKey:crypto.randomUUID()});
tok=await login("admin@vayal.test",env.SEED_ADMIN_PASSWORD);
await api.post(`/admin/orders/${o.id}/record-payment`,{reference:"CM-"+crypto.randomUUID().slice(0,8),method:"upi"});

const det=await api.get("/admin/orders/"+o.id);
const gross=det.supplier_subtotals.reduce((s,x)=>s+x.amount_paise,0);
const payout=det.payouts.reduce((s,x)=>s+x.amount_paise,0);
const at10=gross-Math.floor((gross*1000+5000)/10000);
const atDefault=gross-Math.floor((gross*platformBps+5000)/10000);
chk(payout===at10,"payout used the supplier's 10%, not the platform rate",
    `got ${payout}; 10%=${at10}, default=${atDefault}`);
chk(payout!==atDefault,"and it genuinely differs from the default");

// ---- the supplier's own screen quotes their rate ----
tok=await login(target.email,env.SEED_SUPPLIER_PASSWORD);
const sales=await api.get("/supplier/sales");
chk(sales.charges.commission_rate==="10%","supplier sees their own 10%",sales.charges.commission_rate);
ok(`  sold ${sales.charges.gross_display} − ${sales.charges.commission_display} = ${sales.charges.net_display}`);

// restore
tok=await login("admin@vayal.test",env.SEED_ADMIN_PASSWORD);
await api.patch(`/admin/suppliers/${target.id}`,{...base,commission_bps:null});
ok("restored to the platform default");
console.log(bad===0?"\n  per-supplier commission verified\n":`\n  ${bad} FAILED\n`);
process.exit(bad?1:0);
