/**
 * Close and reopen a day's availability, against a running seeded stack.
 *
 * Guards a bug that put produce back on sale without anyone asking: the
 * supplier screen submitted every row it had a number for, and the upsert
 * forced status back to 'open', so editing one product reopened every product
 * the supplier had closed that day. Silent, and it sells stock the grower has
 * already committed elsewhere.
 *
 *   node infra/scripts/e2e-availability-close.mjs   (or: make test-e2e-close)
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
let tok=null;
const api=createApiClient({baseUrl:env.VITE_API_BASE_URL,getAuthToken:()=>tok});
let bad=0; const ok=m=>console.log("  ok    "+m);
const chk=(c,m,d="")=>{c?ok(m):(bad++,console.log("  FAIL  "+m+(d?" — "+d:"")));};

tok=(await api.post("/auth/login",{identifier:"greens@vayal.test",password:env.SEED_SUPPLIER_PASSWORD})).tokens.access_token;
ok("signed in as supplier");

const sheet=async()=> (await api.get("/supplier/availability")).products;
let rows=(await sheet()).filter(r=>r.declared);
// Need at least two declared rows: one to close, one to edit.
const a=rows[0], b=rows[1];
chk(Boolean(a&&b),"two declared products to work with");

// Ensure both open to start.
for(const r of [a,b]) if(r.status==="closed") await api.post(`/supplier/availability/${r.availability_id}/reopen`);

await api.post(`/supplier/availability/${a.availability_id}/close`);
let after=(await sheet()).find(r=>r.product_id===a.product_id);
chk(after.status==="closed","closing sets status=closed");
chk(after.sellable_grams===0,"closed reports 0 sellable",`got ${after.sellable_grams}`);
chk(after.remaining_grams===after.total_grams-after.reserved_grams-after.sold_grams,
    "declaration preserved underneath",`remaining=${after.remaining_grams}`);

// THE BUG: editing a DIFFERENT product must not reopen this one.
const bNow=(await sheet()).find(r=>r.product_id===b.product_id);
await api.put("/supplier/availability",{entries:[{product_id:b.product_id,total_grams:bNow.total_grams+1000}]});
after=(await sheet()).find(r=>r.product_id===a.product_id);
chk(after.status==="closed","editing another product did NOT reopen the closed one",`status=${after.status}`);

// Even re-saving the closed product's own quantity must not reopen it.
await api.put("/supplier/availability",{entries:[{product_id:a.product_id,total_grams:after.total_grams}]});
after=(await sheet()).find(r=>r.product_id===a.product_id);
chk(after.status==="closed","saving a quantity did NOT reopen it",`status=${after.status}`);

// Reopening is explicit, and restores the declaration.
await api.post(`/supplier/availability/${after.availability_id}/reopen`);
const back=(await sheet()).find(r=>r.product_id===a.product_id);
chk(back.status==="open","explicit reopen puts it back on sale");
chk(back.sellable_grams===back.remaining_grams,"sellable restored on reopen",
    `sellable=${back.sellable_grams} remaining=${back.remaining_grams}`);
chk(back.total_grams===after.total_grams,"declared total survived close+reopen intact");

console.log(bad===0?"\n  all close/reopen checks passed\n":`\n  ${bad} FAILED\n`);
process.exit(bad?1:0);
