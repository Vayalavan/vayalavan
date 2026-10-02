/**
 * The client is the one piece of this app every screen depends on, and the
 * parts most likely to break are the ones Hermes forced us to rewrite: URL
 * joining, the timeout, and abort handling. Those are what these cover.
 */
import { test } from "node:test";
import assert from "node:assert/strict";

import {
  ApiError,
  REQUEST_ID_HEADER,
  buildUrl,
  createApiClient,
  newRequestId,
} from "./api";

const BASE = "http://gateway.test:8080/api";

/** Builds a fetch stand-in that records what it was called with. */
function stubFetch(
  handler: (url: string, init: RequestInit) => Response | Promise<Response>,
) {
  const calls: { url: string; init: RequestInit }[] = [];
  const impl = (async (input: RequestInfo | URL, init: RequestInit = {}) => {
    const url = String(input);
    calls.push({ url, init });
    return handler(url, init);
  }) as unknown as typeof fetch;
  return { impl, calls };
}

function jsonResponse(body: unknown, status = 200, headers: HeadersInit = {}): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json", ...headers },
  });
}

// ---------------------------------------------------------------------------
// buildUrl
// ---------------------------------------------------------------------------

test("buildUrl keeps the base path instead of resolving it away", () => {
  // The bug this exists to prevent: `new URL("/auth/login", base)` drops the
  // "/api" mount point and every call 404s.
  assert.equal(buildUrl(BASE, "/auth/login", undefined), `${BASE}/auth/login`);
});

test("buildUrl accepts a path with or without a leading slash", () => {
  assert.equal(buildUrl(BASE, "products", undefined), `${BASE}/products`);
  assert.equal(buildUrl(BASE, "/products", undefined), `${BASE}/products`);
});

test("buildUrl never produces a double slash", () => {
  assert.equal(buildUrl(`${BASE}/`, "/products", undefined), `${BASE}/products`);
  assert.equal(buildUrl(`${BASE}///`, "products", undefined), `${BASE}/products`);
});

test("buildUrl drops null and undefined query entries but keeps falsy ones", () => {
  // `offset=0` and `status=""` are real filters. Dropping them because they
  // are falsy would silently page the supplier to the wrong place.
  assert.equal(
    buildUrl(BASE, "/products", {
      search: undefined,
      type: null,
      offset: 0,
      status: "",
      active: false,
    }),
    `${BASE}/products?offset=0&status=&active=false`,
  );
});

test("buildUrl percent-encodes query values", () => {
  assert.equal(
    buildUrl(BASE, "/products", { search: "green & red" }),
    `${BASE}/products?search=green%20%26%20red`,
  );
});

// ---------------------------------------------------------------------------
// Headers
// ---------------------------------------------------------------------------

test("every request carries a request id", async () => {
  const { impl, calls } = stubFetch(() => jsonResponse({ ok: true }));
  const api = createApiClient({
    baseUrl: BASE,
    fetchImpl: impl,
    requestIdFactory: () => "mob-fixed-id",
  });

  await api.get("/me");

  const headers = calls[0]?.init.headers as Record<string, string>;
  assert.equal(headers[REQUEST_ID_HEADER], "mob-fixed-id");
});

test("the bearer token is read per request, not captured once", async () => {
  const { impl, calls } = stubFetch(() => jsonResponse({}));
  let token: string | null = "first";
  const api = createApiClient({
    baseUrl: BASE,
    fetchImpl: impl,
    getAuthToken: () => token,
  });

  await api.get("/me");
  token = "second";
  await api.get("/me");
  token = null;
  await api.get("/me");

  const auth = calls.map((c) => (c.init.headers as Record<string, string>)["Authorization"]);
  assert.deepEqual(auth, ["Bearer first", "Bearer second", undefined]);
});

test("Content-Type is set for a JSON body and omitted for a form body", async () => {
  const { impl, calls } = stubFetch(() => jsonResponse({}));
  const api = createApiClient({ baseUrl: BASE, fetchImpl: impl });

  await api.post("/products", { name: "Tomato" });
  // A multipart body must have its Content-Type written by the platform, or
  // it arrives without the boundary and the server cannot parse it.
  await api.postForm("/imports", new FormData());

  const first = calls[0]?.init.headers as Record<string, string>;
  const second = calls[1]?.init.headers as Record<string, string>;
  assert.equal(first["Content-Type"], "application/json");
  assert.equal(second["Content-Type"], undefined);
});

test("an idempotency key is forwarded when given", async () => {
  const { impl, calls } = stubFetch(() => jsonResponse({}));
  const api = createApiClient({ baseUrl: BASE, fetchImpl: impl });

  await api.post("/orders", {}, { idempotencyKey: "key-1" });

  const headers = calls[0]?.init.headers as Record<string, string>;
  assert.equal(headers["Idempotency-Key"], "key-1");
});

// ---------------------------------------------------------------------------
// Errors
// ---------------------------------------------------------------------------

test("the error envelope is decoded into a typed ApiError", async () => {
  const { impl } = stubFetch(() =>
    jsonResponse(
      {
        error: {
          code: "VALIDATION_FAILED",
          message: "Check the highlighted fields.",
          details: { name: "Name is required." },
        },
      },
      422,
      { [REQUEST_ID_HEADER]: "srv-42" },
    ),
  );
  const api = createApiClient({ baseUrl: BASE, fetchImpl: impl });

  await assert.rejects(
    () => api.post("/products", {}),
    (error: unknown) => {
      assert.ok(error instanceof ApiError);
      assert.equal(error.status, 422);
      assert.equal(error.code, "VALIDATION_FAILED");
      assert.equal(error.message, "Check the highlighted fields.");
      assert.deepEqual(error.details, { name: "Name is required." });
      // The server's id wins over the one we generated: that is the one in
      // the service logs.
      assert.equal(error.requestId, "srv-42");
      return true;
    },
  );
});

test("a non-JSON failure body still produces an actionable error", async () => {
  // What an nginx 502 page looks like from here.
  const { impl } = stubFetch(
    () => new Response("<html>Bad Gateway</html>", { status: 502 }),
  );
  const api = createApiClient({ baseUrl: BASE, fetchImpl: impl });

  await assert.rejects(
    () => api.get("/products"),
    (error: unknown) => {
      assert.ok(error instanceof ApiError);
      assert.equal(error.status, 502);
      assert.equal(error.code, "UNKNOWN_ERROR");
      assert.match(error.message, /502/);
      return true;
    },
  );
});

test("a 401 notifies the unauthorized hook exactly once", async () => {
  const { impl } = stubFetch(() =>
    jsonResponse({ error: { code: "UNAUTHENTICATED", message: "Sign in." } }, 401),
  );
  const seen: ApiError[] = [];
  const api = createApiClient({
    baseUrl: BASE,
    fetchImpl: impl,
    onUnauthorized: (error) => {
      seen.push(error);
    },
  });

  await assert.rejects(() => api.get("/me"));
  assert.equal(seen.length, 1);
  assert.equal(seen[0]?.code, "UNAUTHENTICATED");
});

test("a 401 is retried once with the refreshed token", async () => {
  // The resume-from-background case: the access token expired while the phone
  // was in someone's pocket. One refresh, one retry, and the supplier never
  // sees a sign-in screen.
  let token = "stale";
  const { impl, calls } = stubFetch((_url, init) => {
    const auth = (init.headers as Record<string, string>)["Authorization"];
    return auth === "Bearer fresh"
      ? jsonResponse({ products: [] })
      : jsonResponse({ error: { code: "TOKEN_EXPIRED", message: "Expired." } }, 401);
  });

  const api = createApiClient({
    baseUrl: BASE,
    fetchImpl: impl,
    getAuthToken: () => token,
    onUnauthorized: () => {
      token = "fresh";
      return true;
    },
  });

  assert.deepEqual(await api.get("/products"), { products: [] });
  assert.equal(calls.length, 2);
});

test("a 401 that survives the refresh is thrown rather than retried forever", async () => {
  // The refresh token is revoked too. Retrying without a bound would spin
  // against the gateway from a phone on mobile data.
  const { impl, calls } = stubFetch(() =>
    jsonResponse({ error: { code: "TOKEN_EXPIRED", message: "Expired." } }, 401),
  );
  let refreshAttempts = 0;

  const api = createApiClient({
    baseUrl: BASE,
    fetchImpl: impl,
    onUnauthorized: () => {
      refreshAttempts += 1;
      return true;
    },
  });

  await assert.rejects(() => api.get("/products"), ApiError);
  assert.equal(calls.length, 2, "the original request plus exactly one retry");
  assert.equal(refreshAttempts, 2);
});

test("a 401 is not retried when the hook declines to refresh", async () => {
  const { impl, calls } = stubFetch(() =>
    jsonResponse({ error: { code: "UNAUTHENTICATED", message: "Sign in." } }, 401),
  );
  const api = createApiClient({
    baseUrl: BASE,
    fetchImpl: impl,
    onUnauthorized: () => false,
  });

  await assert.rejects(() => api.get("/me"), ApiError);
  assert.equal(calls.length, 1);
});

test("a 403 does not clear the session", async () => {
  // A supplier reaching another supplier's product is forbidden, not signed
  // out. Treating the two alike would eject them from the app mid-task.
  const { impl } = stubFetch(() =>
    jsonResponse({ error: { code: "FORBIDDEN", message: "Not yours." } }, 403),
  );
  let cleared = false;
  const api = createApiClient({
    baseUrl: BASE,
    fetchImpl: impl,
    onUnauthorized: () => {
      cleared = true;
    },
  });

  await assert.rejects(() => api.get("/products/other"));
  assert.equal(cleared, false);
});

test("an unreachable server becomes a network error, not an unhandled throw", async () => {
  const { impl } = stubFetch(() => {
    throw new TypeError("Network request failed");
  });
  const api = createApiClient({ baseUrl: BASE, fetchImpl: impl });

  await assert.rejects(
    () => api.get("/products"),
    (error: unknown) => {
      assert.ok(error instanceof ApiError);
      assert.equal(error.code, "NETWORK_ERROR");
      assert.equal(error.isNetworkError, true);
      return true;
    },
  );
});

// ---------------------------------------------------------------------------
// Timeout and abort — the Hermes rewrite
// ---------------------------------------------------------------------------

test("a request that outlives the timeout is aborted and reported as a timeout", async () => {
  const { impl } = stubFetch(
    (_url, init) =>
      new Promise<Response>((_resolve, reject) => {
        // Never resolves on its own; only the client's abort ends it.
        init.signal?.addEventListener("abort", () =>
          reject(new DOMException("Aborted", "AbortError")),
        );
      }),
  );
  const api = createApiClient({ baseUrl: BASE, fetchImpl: impl, timeoutMs: 20 });

  await assert.rejects(
    () => api.get("/products"),
    (error: unknown) => {
      assert.ok(error instanceof ApiError);
      assert.equal(error.code, "REQUEST_TIMEOUT");
      return true;
    },
  );
});

test("a caller's abort is reported as a cancellation, not a timeout", async () => {
  // The difference the UI acts on: a timeout gets an error banner and a retry
  // button; a screen the supplier navigated away from gets silence.
  const { impl } = stubFetch(
    (_url, init) =>
      new Promise<Response>((_resolve, reject) => {
        init.signal?.addEventListener("abort", () =>
          reject(new DOMException("Aborted", "AbortError")),
        );
      }),
  );
  const api = createApiClient({ baseUrl: BASE, fetchImpl: impl, timeoutMs: 5_000 });

  const controller = new AbortController();
  const pending = api.get("/products", { signal: controller.signal });
  controller.abort();

  await assert.rejects(pending, (error: unknown) => {
    assert.ok(error instanceof ApiError);
    assert.equal(error.code, "REQUEST_CANCELLED");
    return true;
  });
});

test("a successful request clears its timer rather than firing late", async () => {
  const { impl } = stubFetch(() => jsonResponse({ ok: true }));
  const api = createApiClient({ baseUrl: BASE, fetchImpl: impl, timeoutMs: 30 });

  assert.deepEqual(await api.get("/me"), { ok: true });
  // If the timer were left running, the process would stay alive for it and
  // node:test would report the suite as hanging.
  await new Promise((resolve) => setTimeout(resolve, 50));
});

// ---------------------------------------------------------------------------
// Bodies
// ---------------------------------------------------------------------------

test("a 204 resolves to undefined rather than throwing on an empty body", async () => {
  const { impl } = stubFetch(() => new Response(null, { status: 204 }));
  const api = createApiClient({ baseUrl: BASE, fetchImpl: impl });

  assert.equal(await api.delete("/addresses/1"), undefined);
});

test("a 200 with an empty body resolves to undefined", async () => {
  // vm-orders-api's close/reopen endpoints answer 200 with nothing.
  const { impl } = stubFetch(() => new Response("", { status: 200 }));
  const api = createApiClient({ baseUrl: BASE, fetchImpl: impl });

  assert.equal(await api.post("/supplier/availability/1/close"), undefined);
});

test("request ids are unique", () => {
  const ids = new Set(Array.from({ length: 500 }, newRequestId));
  assert.equal(ids.size, 500);
});
