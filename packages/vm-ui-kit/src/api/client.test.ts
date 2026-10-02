/**
 * URL construction is the single most load-bearing line in the shared client:
 * if it is wrong, every request from all three UIs fails identically, and it
 * fails in a way that curl against the same API will not reproduce.
 *
 * That is exactly what happened. VITE_API_BASE_URL is "http://localhost:8080/api"
 * because the gateway mounts the JSON API under /api, but the original
 * implementation resolved the path with `new URL(path, base)`. A leading slash
 * makes the reference root-relative, so "/api" was discarded and every call
 * went to the gateway root, which answered "No route matches this path."
 *
 * The first case below is that regression. The rest pin the normalisation
 * rules so the fix cannot be "simplified" back into resolution semantics.
 */
import { test } from "node:test";
import assert from "node:assert/strict";
import { buildUrl } from "./client.js";

const MOUNTED = "http://localhost:8080/api";

test("keeps the base path when the API is mounted on a subpath", () => {
  assert.equal(
    buildUrl(MOUNTED, "/auth/login", undefined),
    "http://localhost:8080/api/auth/login",
  );
});

test("keeps the base path for a nested route", () => {
  assert.equal(
    buildUrl(MOUNTED, "/admin/payouts/summary", undefined),
    "http://localhost:8080/api/admin/payouts/summary",
  );
});

test("accepts a path with no leading slash", () => {
  assert.equal(
    buildUrl(MOUNTED, "catalog", undefined),
    "http://localhost:8080/api/catalog",
  );
});

test("does not double the slash when the base has a trailing one", () => {
  assert.equal(
    buildUrl("http://localhost:8080/api/", "/catalog", undefined),
    "http://localhost:8080/api/catalog",
  );
});

test("collapses several trailing slashes on the base", () => {
  assert.equal(
    buildUrl("http://localhost:8080/api///", "/catalog", undefined),
    "http://localhost:8080/api/catalog",
  );
});

test("still works when the API is served from the root", () => {
  assert.equal(
    buildUrl("http://localhost:8080", "/healthz", undefined),
    "http://localhost:8080/healthz",
  );
});

test("appends query parameters", () => {
  assert.equal(
    buildUrl(MOUNTED, "/catalog", { type: "fruit", grade: "A" }),
    "http://localhost:8080/api/catalog?type=fruit&grade=A",
  );
});

test("omits undefined and null query parameters", () => {
  // The catalog page passes `type: type || undefined` to mean "no filter";
  // sending "type=undefined" would filter on the literal string.
  assert.equal(
    buildUrl(MOUNTED, "/catalog", { type: undefined, grade: null, page: 2 }),
    "http://localhost:8080/api/catalog?page=2",
  );
});

test("encodes query values that need it", () => {
  assert.equal(
    buildUrl(MOUNTED, "/orders", { q: "VM-260815-0001 & co" }),
    "http://localhost:8080/api/orders?q=VM-260815-0001+%26+co",
  );
});

test("keeps a deployed base path such as a reverse-proxied prefix", () => {
  // Production may serve the gateway behind /gateway/api rather than /api.
  assert.equal(
    buildUrl("https://vayal.example/gateway/api", "/cart/items", undefined),
    "https://vayal.example/gateway/api/cart/items",
  );
});
