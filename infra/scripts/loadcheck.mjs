/**
 * A load check, not a load test.
 *
 * The question it answers is narrow and honest: does the catalogue — the one
 * endpoint every visitor hits, uncached — hold up under concurrent reads on
 * this machine, and where does latency go? It is not a capacity plan; that
 * needs production-shaped data and a machine that is not also running the
 * database, three UIs and a browser.
 *
 *   node infra/scripts/loadcheck.mjs [concurrency] [seconds]
 */
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

const ROOT = join(dirname(fileURLToPath(import.meta.url)), "..", "..");
const env = Object.fromEntries(
  readFileSync(join(ROOT, ".env"), "utf8").split("\n")
    .filter((l) => l.includes("=") && !l.trimStart().startsWith("#"))
    .map((l) => { const i = l.indexOf("="); return [l.slice(0, i).trim(), l.slice(i + 1).trim()]; }),
);

const CONCURRENCY = Number(process.argv[2] ?? 20);
const SECONDS = Number(process.argv[3] ?? 10);
const URL_UNDER_TEST = `${env.VITE_API_BASE_URL}/catalog`;

const latencies = [];
let ok = 0, rateLimited = 0, failed = 0;

// The gateway rate-limits per IP, and a load check from one machine is one IP.
// Counted separately rather than as failures: being limited is the limiter
// working, not the service breaking.
async function worker(deadline) {
  while (Date.now() < deadline) {
    const started = performance.now();
    try {
      const res = await fetch(URL_UNDER_TEST);
      const ms = performance.now() - started;
      await res.text();
      if (res.status === 200) { ok++; latencies.push(ms); }
      else if (res.status === 429) rateLimited++;
      else failed++;
    } catch { failed++; }
  }
}

function percentile(sorted, p) {
  if (!sorted.length) return 0;
  const index = Math.min(sorted.length - 1, Math.floor((p / 100) * sorted.length));
  return sorted[index];
}

console.log(`\n  ${CONCURRENCY} concurrent readers, ${SECONDS}s, against ${URL_UNDER_TEST}\n`);

const deadline = Date.now() + SECONDS * 1000;
const startedAt = Date.now();
await Promise.all(Array.from({ length: CONCURRENCY }, () => worker(deadline)));
const elapsed = (Date.now() - startedAt) / 1000;

latencies.sort((a, b) => a - b);
const mean = latencies.reduce((s, v) => s + v, 0) / (latencies.length || 1);

console.log(`  requests     ${ok} ok, ${rateLimited} rate-limited, ${failed} failed`);
console.log(`  throughput   ${(ok / elapsed).toFixed(1)} req/s sustained`);
console.log(`  latency      mean ${mean.toFixed(0)}ms`);
console.log(`               p50  ${percentile(latencies, 50).toFixed(0)}ms`);
console.log(`               p95  ${percentile(latencies, 95).toFixed(0)}ms`);
console.log(`               p99  ${percentile(latencies, 99).toFixed(0)}ms`);
console.log(`               max  ${(latencies.at(-1) ?? 0).toFixed(0)}ms`);

if (failed > 0) {
  console.log(`\n  ${failed} request(s) FAILED outright — investigate before launch.`);
  process.exit(1);
}
console.log("\n  no failures.\n");
