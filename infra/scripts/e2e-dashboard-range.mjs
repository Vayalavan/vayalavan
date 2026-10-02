/**
 * The admin dashboard's reporting period.
 *
 * Two properties matter beyond "the dropdown works". First, a client sends a
 * period NAME and never its own idea of today, so a laptop with a wrong clock
 * cannot shift the window (CLAUDE.md rule 2). Second, the queue-depth figures —
 * awaiting processing, outstanding settlement — must NOT follow the range:
 * they describe right now, and scoping them to last month would report a
 * backlog that has since cleared.
 *
 *   node infra/scripts/e2e-dashboard-range.mjs  (or: make test-e2e-range)
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
tok=(await api.post("/auth/login",{identifier:"admin@vayal.test",password:env.SEED_ADMIN_PASSWORD})).tokens.access_token;

// default with NO parameter must be today
const def=await api.get("/admin/dashboard");
chk(def.range.key==="today"&&def.range.days===1,"default with no parameter is today",
    `${def.range.key} / ${def.range.days}d`);
ok(`  today: ${def.orders_today} orders, GMV ${def.gmv_today}, earnings ${def.earnings_today}`);

for(const [key,days] of [["today",1],["2d",2],["7d",7],["1m",30]]){
  const d=await api.get("/admin/dashboard",{query:{range:key}});
  chk(d.range.days===days,`${d.range.label} spans ${days} day(s)`,`got ${d.range.days}`);
}

// figures must be monotonic: a longer window cannot contain fewer orders
const t=await api.get("/admin/dashboard",{query:{range:"today"}});
const m=await api.get("/admin/dashboard",{query:{range:"1m"}});
chk(m.orders_today>=t.orders_today,"a month contains at least as many orders as today",
    `${m.orders_today} vs ${t.orders_today}`);
chk(m.earnings_today_paise>=t.earnings_today_paise,"and at least as much earnings");

// queue depths must NOT follow the range — they are 'right now', not history
chk(m.awaiting_processing===t.awaiting_processing,
    "awaiting-processing is a live queue, unaffected by the range");
chk(m.outstanding_to_suppliers===t.outstanding_to_suppliers,
    "outstanding settlement is unaffected by the range");

// custom
const c=await api.get("/admin/dashboard",{query:{range:"custom",from:"2026-08-01",to:"2026-08-15"}});
chk(c.range.days===15,"custom range is inclusive of both ends",`got ${c.range.days}`);
ok(`  custom label: ${c.range.label}`);

for(const [q,why] of [
  [{range:"nonsense"},"unknown preset"],
  [{range:"custom"},"custom with no dates"],
  [{range:"custom",from:"2026-08-15",to:"2026-08-01"},"end before start"],
  [{range:"custom",from:"1990-01-01",to:"2026-08-15"},"absurdly long range"],
]){
  let err=null;
  try{ await api.get("/admin/dashboard",{query:q}); }catch(e){ err=e; }
  chk(err!==null&&err.status===400,`refused: ${why}`,err?`${err.status}`:"accepted");
}
console.log(bad===0?"\n  all range checks passed\n":`\n  ${bad} FAILED\n`);
process.exit(bad?1:0);
