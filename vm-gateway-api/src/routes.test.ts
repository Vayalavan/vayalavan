/**
 * Guards the query allow-list.
 *
 * The gateway forwards a NAMED set of query parameters per route, which is the
 * right default — an open proxy would let a client reach upstream parameters no
 * UI is meant to use. The failure mode is that a parameter left off the list is
 * dropped in SILENCE: the upstream falls back to its default and returns a
 * perfectly valid answer to a question nobody asked. That is exactly how the
 * supplier sales filter shipped doing nothing — `range` was missing, so every
 * period came back as all time, with a 200 and plausible figures.
 *
 * Read as source text rather than by starting the app: the alternative is a
 * live server, a real JWT and a stub upstream to observe one URL. This asserts
 * the one thing that broke, and fails when a filter is added to a screen but
 * not to the route behind it.
 */
import { test } from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const ROUTES = path.resolve(
  path.dirname(fileURLToPath(import.meta.url)),
  "./routes.ts",
);

/**
 * The `query: { … }` block of the forward for a given upstream path, as text.
 */
function queryBlockFor(upstreamPath: string): string {
  const source = fs.readFileSync(ROUTES, "utf8");
  const at = source.indexOf(`path: "${upstreamPath}"`);
  assert.notEqual(at, -1, `no forward found for ${upstreamPath}`);

  const from = source.indexOf("query: {", at);
  assert.notEqual(from, -1, `${upstreamPath} forwards no query parameters`);
  const to = source.indexOf("},", from);
  return source.slice(from, to);
}

/** Every parameter a reporting screen sends, and the API acts on. */
const REPORTING_PARAMS = ["range", "from", "to"];

test("the supplier sales route forwards the whole date filter", () => {
  const block = queryBlockFor("/supplier/sales");
  for (const param of [...REPORTING_PARAMS, "limit", "offset"]) {
    assert.match(
      block,
      new RegExp(`\\b${param}:`),
      `/supplier/sales drops "${param}" — the screen sends it and the upstream ` +
        `silently falls back to its default`,
    );
  }
});

test("every reporting route forwards the period, not just the dates", () => {
  // A route that forwards from/to but not range applies the wrong period
  // rather than failing, which is the bug this whole file exists for.
  const source = fs.readFileSync(ROUTES, "utf8");
  for (const [, upstreamPath] of source.matchAll(/path: "(\/[^"]*)"/g)) {
    const at = source.indexOf(`path: "${upstreamPath}"`);
    const from = source.indexOf("query: {", at);
    if (from === -1) continue;
    const block = source.slice(from, source.indexOf("},", from));
    if (!/\bfrom:/.test(block)) continue;

    assert.match(
      block,
      /\brange:/,
      `${upstreamPath} forwards custom dates but not "range", so a custom ` +
        `period resolves to the upstream default`,
    );
  }
});
