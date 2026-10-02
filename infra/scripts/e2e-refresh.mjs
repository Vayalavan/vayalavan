/**
 * Session survival across reloads.
 *
 * Refresh tokens rotate, and presenting a rotated token is normally treated as
 * theft. But a rotation whose response never reached the browser looks exactly
 * the same, and treating that as theft signed people out on every reload — the
 * bug this guards. A short grace window distinguishes the two.
 *
 * e2e-theft.mjs asserts the other half: that detection still fires outside the
 * window. Both must pass, or the control is either useless or harmful.
 *
 *   node infra/scripts/e2e-refresh.mjs   (or: make test-e2e-refresh)
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
const api=createApiClient({baseUrl:env.VITE_API_BASE_URL,getAuthToken:()=>null});
let bad=0; const ok=m=>console.log("  ok    "+m);
const chk=(c,m,d="")=>{c?ok(m):(bad++,console.log("  FAIL  "+m+(d?" — "+d:"")));};

const login=await api.post("/auth/login",{identifier:"customer@vayal.test",password:env.SEED_CUSTOMER_PASSWORD});
let refresh=login.tokens.refresh_token;
ok("signed in");

// Ten sequential reloads. Each rotates; each must succeed.
for(let i=1;i<=10;i++){
  try{
    const r=await api.post("/auth/refresh",{refresh_token:refresh});
    refresh=r.tokens.refresh_token;
  }catch(e){ chk(false,`reload ${i} kept the session`,`${e.status} ${e.code}`); break; }
}
chk(true,"10 sequential reloads all kept the session");

// The real bug: the SAME token presented twice, as StrictMode did.
const before=refresh;
const first=await api.post("/auth/refresh",{refresh_token:before});
let second=null, err=null;
try{ second=await api.post("/auth/refresh",{refresh_token:before}); }catch(e){ err=e; }
chk(second!==null,"replaying a just-rotated token still works (grace window)",
    err?`${err.status} ${err.code}`:"");

// And the session must still be alive afterwards — the old bug revoked everything.
try{
  await api.post("/auth/refresh",{refresh_token:(second??first).tokens.refresh_token});
  ok("session survived the replay — not revoked as theft");
}catch(e){ chk(false,"session survived the replay",`${e.status} ${e.code}`); }

console.log(bad===0?"\n  refresh checks passed\n":`\n  ${bad} FAILED\n`);
process.exit(bad?1:0);
