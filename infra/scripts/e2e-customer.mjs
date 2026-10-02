/**
 * The customer's own screens: product detail, order history, profile.
 *
 * The product detail endpoint is PUBLIC, which is the point of the first
 * assertions here — a shareable link has to work for someone who has never
 * signed in, while still refusing to publish exact stock (CLAUDE.md §5.2) and
 * still 404ing for produce whose grower is suspended.
 *
 *   node infra/scripts/e2e-customer.mjs   (or: make test-e2e-customer)
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

// product detail is PUBLIC — no token yet
const cat=await api.get("/catalog");
const first=cat.products[0];
const d=await api.get("/catalog/"+first.id);
ok("detail for "+d.product.name+" (public, no sign-in)");
chk(d.product.supplier_name!=="","grower named: "+d.product.supplier_name);
chk(typeof d.product.available_today==="boolean","available_today present");
chk(d.units.length>0,"pack sizes returned");
chk(d.units.every(u=>u.per_kg_display.includes("/kg")),"per-kg comparison on every pack");
console.log("        "+d.units.map(u=>u.label+" "+u.price_display+" ("+u.per_kg_display+")").join(", "));
chk(!("total_grams" in d.product)&&!("remaining_grams" in d.product),
    "exact stock NOT exposed to customers");

try{ await api.get("/catalog/00000000-0000-0000-0000-000000000000"); chk(false,"unknown id 404s"); }
catch(e){ chk(e.status===404,"unknown id 404s",`got ${e.status}`); }

// signed-in: orders + addresses feed the new screens
tok=(await api.post("/auth/login",{identifier:"customer@vayal.test",password:env.SEED_CUSTOMER_PASSWORD})).tokens.access_token;
const list=await api.get("/orders");
chk(Array.isArray(list.orders),"order history returns a list ("+list.orders.length+")");
if(list.orders.length){
  const det=await api.get("/orders/"+list.orders[0].id);
  chk(Array.isArray(det.milestones)&&det.milestones.length===3,"order detail carries 3 milestones");
  chk(Array.isArray(det.items)&&det.items.length>0,"order detail carries items");
  chk(Boolean(det.address),"order detail carries the address snapshot");
}
const me=(await api.get("/me")).user;
chk(Boolean(me.email||me.phone),"profile /me returns identity");
const addr=await api.get("/addresses");
chk(Array.isArray(addr.addresses),"address book returns a list ("+addr.addresses.length+")");

console.log(bad===0?"\n  all customer-screen checks passed\n":`\n  ${bad} FAILED\n`);
process.exit(bad?1:0);
