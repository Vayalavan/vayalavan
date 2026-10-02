/**
 * Forwarding to internal services.
 *
 * Every upstream call carries three things, and forgetting any one of them is
 * a bug this module exists to make impossible:
 *
 *   - INTERNAL_SERVICE_TOKEN, proving the call came from the gateway
 *   - X-Request-Id, so one customer action is traceable across services
 *   - the verified identity, as X-User-Id / X-User-Role
 */
import type { Request, Response } from "express";
import type { Config } from "./config.js";
import { AppError } from "./errors.js";
import { REQUEST_ID_HEADER } from "./logger.js";

export type UpstreamName = "profile" | "catalog" | "orders" | "analytics";

/** Bounds an upstream call so a hung service cannot pin a gateway worker. */
const UPSTREAM_TIMEOUT_MS = 10_000;

function baseUrlFor(config: Config, upstream: UpstreamName): string {
  switch (upstream) {
    case "profile":
      return config.PROFILE_API_URL;
    case "catalog":
      return config.CATALOG_API_URL;
    case "orders":
      return config.ORDERS_API_URL;
    case "analytics":
      return config.ANALYTICS_API_URL;
  }
}

export interface ForwardOptions {
  upstream: UpstreamName;
  /** Path on the internal service, e.g. "/auth/login". */
  path: string;
  method: string;
  /** Parsed and validated body. Omitted for GET/DELETE. */
  body?: unknown;
  /** Query string to append, taken from the inbound request when needed. */
  query?: Record<string, string | undefined>;
  /**
   * Non-JSON response types this route is allowed to relay, e.g. "text/csv"
   * for the CSV template download.
   *
   * An explicit opt-in per route, not a global relaxation: the default of
   * JSON-only is what stops an upstream HTML error page — which can name
   * internal hosts — reaching a browser.
   */
  allowContentTypes?: string[];
}

/**
 * Forwards a request to an internal service and relays the response.
 *
 * The upstream's status and JSON body are passed through unchanged, because
 * both sides already speak the same error envelope (CLAUDE.md rule 8) — so a
 * validation failure raised in Go reaches the browser intact.
 */
export async function forward(
  req: Request,
  res: Response,
  config: Config,
  options: ForwardOptions,
): Promise<void> {
  const url = new URL(options.path, baseUrlFor(config, options.upstream));

  if (options.query) {
    for (const [key, value] of Object.entries(options.query)) {
      if (value !== undefined && value !== "") url.searchParams.set(key, value);
    }
  }

  const headers = new Headers({
    Accept: "application/json",
    "X-Internal-Token": config.INTERNAL_SERVICE_TOKEN,
    [REQUEST_ID_HEADER]: req.requestId,
  });

  // Only ever set from a verified token — never copied from the inbound
  // request, which would let a client assert its own role.
  if (req.identity) {
    headers.set("X-User-Id", req.identity.userId);
    headers.set("X-User-Role", req.identity.role);
    if (req.identity.supplierId) {
      headers.set("X-Supplier-Id", req.identity.supplierId);
    }
  }

  // Idempotency keys belong to the client and must survive the hop
  // (CLAUDE.md rule 6).
  const idempotencyKey = req.header("idempotency-key");
  if (idempotencyKey) headers.set("Idempotency-Key", idempotencyKey);

  // The upstream logs the real client's agent rather than "node".
  const userAgent = req.header("user-agent");
  if (userAgent) headers.set("User-Agent", userAgent);

  if (options.body !== undefined) headers.set("Content-Type", "application/json");

  let upstreamResponse: globalThis.Response;
  try {
    upstreamResponse = await fetch(url, {
      method: options.method,
      headers,
      signal: AbortSignal.timeout(UPSTREAM_TIMEOUT_MS),
      ...(options.body !== undefined && { body: JSON.stringify(options.body) }),
    });
  } catch (cause) {
    req.log.error(
      { err: cause, upstream: options.upstream, path: options.path },
      "upstream request failed",
    );
    // 503, not 500: the gateway is fine, a dependency is not — and the client
    // may reasonably retry.
    throw AppError.unavailable(
      "That service is temporarily unavailable. Please try again shortly.",
    );
  }

  res.status(upstreamResponse.status);

  // 204 and friends carry no body.
  if (upstreamResponse.status === 204 || upstreamResponse.headers.get("content-length") === "0") {
    res.end();
    return;
  }

  const text = await upstreamResponse.text();
  if (!text) {
    res.end();
    return;
  }

  const contentType = upstreamResponse.headers.get("content-type") ?? "";
  if (contentType.includes("application/json")) {
    res.type("application/json").send(text);
    return;
  }

  // Routes that legitimately return something else opt in by name.
  const allowed = options.allowContentTypes ?? [];
  if (allowed.some((type) => contentType.includes(type))) {
    res.type(contentType);
    // Preserve the filename the upstream chose for the download.
    const disposition = upstreamResponse.headers.get("content-disposition");
    if (disposition) res.setHeader("Content-Disposition", disposition);
    res.send(text);
    return;
  }

  // A non-JSON body from an internal service means something is wrong with
  // it, not with the client's request. Do not relay it — it could be an HTML
  // error page naming internal hosts.
  req.log.error(
    { upstream: options.upstream, status: upstreamResponse.status, contentType },
    "upstream returned a non-JSON response",
  );
  throw AppError.unavailable("That service returned an unexpected response.");
}
