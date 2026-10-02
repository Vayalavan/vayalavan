/**
 * Refresh-token theft detection.
 *
 * The counterpart to e2e-refresh.mjs. That one proves a legitimate client is
 * not signed out by a lost response; this one proves an actually-stolen token
 * still revokes every session. Relaxing reuse detection without this test would
 * quietly turn a security control into decoration.
 *
 *   node infra/scripts/e2e-theft.mjs   (or: make test-e2e-theft)
 */
import { readFileSync } from "node:fs";
import { execSync } from "node:child_process";
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
const psql=(sql)=>execSync(`docker exec vayal-postgres psql -U ${env.POSTGRES_USER} -d vayal -At -c "${sql.replace(/"/g,'\\"')}"`).toString().trim();

const login=await api.post("/auth/login",{identifier:"customer@vayal.test",password:env.SEED_CUSTOMER_PASSWORD});
const userId=login.user.id;
const stolen=login.tokens.refresh_token;

// Legitimate client rotates once.
const rotated=await api.post("/auth/refresh",{refresh_token:stolen});
ok("token rotated normally");

// Age the revocation past the 30s grace window, simulating a token stolen
// from a log or a backup rather than a lost response.
psql(`UPDATE profile.refresh_tokens SET revoked_at = now() - interval '5 minutes' WHERE user_id = '${userId}' AND revoked_at IS NOT NULL`);

let err=null;
try{ await api.post("/auth/refresh",{refresh_token:stolen}); }catch(e){ err=e; }
chk(err!==null,"replay OUTSIDE the grace window is refused",err?"":"it was accepted");

// And the whole family must be revoked — that is what makes theft detection
// worth having.
const live=Number(psql(`SELECT count(*) FROM profile.refresh_tokens WHERE user_id='${userId}' AND revoked_at IS NULL`));
chk(live===0,"every session revoked after detected theft",`${live} still live`);

let stillWorks=true;
try{ await api.post("/auth/refresh",{refresh_token:rotated.tokens.refresh_token}); }catch{ stillWorks=false; }
chk(!stillWorks,"the legitimate client's own token was revoked too (correct)");

console.log(bad===0?"\n  theft detection intact\n":`\n  ${bad} FAILED\n`);
process.exit(bad?1:0);
