/**
 * The API client for the customer app.
 *
 * A copy of vm-supplier-mobile-ui/src/lib/api.ts, minus the multipart and raw
 * download methods — this app uploads no files and downloads none, and a
 * method with no caller is a method nobody notices is broken. Everything else
 * is identical, because the reasons below are properties of the RUNTIME, not
 * of which app is running on it.
 *
 * A deliberate re-implementation of `@vayal/ui-kit`'s client rather than a
 * reuse of it. The contract is identical — Bearer token, an `X-Request-Id` on
 * every request (CLAUDE.md rule 8), and the platform's single error envelope
 * decoded into a typed ApiError — but three of the browser primitives that
 * client is built on do not exist in Hermes, React Native's engine:
 *
 *   - `AbortSignal.timeout()` and `AbortSignal.any()` — no timeout, no way to
 *     combine a caller's signal with one. Rebuilt on AbortController below.
 *   - `crypto.randomUUID()` — undefined, so request ids need their own source.
 *   - `credentials: "include"` — accepted and ignored; React Native has no
 *     cookie jar shared with a browser. The refresh token travels in the body,
 *     which is what vm-profile-api expects anyway.
 *
 * Importing the web client and discovering this at runtime would mean every
 * request hanging forever with no timeout, on a customer's phone, mid-order.
 *
 * This module is deliberately free of React Native imports so it can be tested
 * under `node --test`.
 */

/** The header the request id travels in. Matches the gateway and Go services. */
export const REQUEST_ID_HEADER = "X-Request-Id";

/** The error envelope every service returns (CLAUDE.md rule 8). */
export interface ApiErrorEnvelope {
  error: {
    code: string;
    message: string;
    details?: Record<string, unknown>;
  };
}

/**
 * A failed API call.
 *
 * `code` is the stable identifier to branch on; messages are for humans and
 * may be reworded. `requestId` is the one string support can use to find the
 * exact request in the service logs, so error screens show it.
 */
export class ApiError extends Error {
  readonly status: number;
  readonly code: string;
  readonly details: Record<string, unknown> | undefined;
  readonly requestId: string | undefined;

  constructor(
    status: number,
    code: string,
    message: string,
    options: {
      details?: Record<string, unknown> | undefined;
      requestId?: string | undefined;
    } = {},
  ) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.code = code;
    this.details = options.details;
    this.requestId = options.requestId;
  }

  /** True when nothing came back at all — offline, DNS, or a timeout. */
  get isNetworkError(): boolean {
    return this.status === 0;
  }
}

export type QueryValue = string | number | boolean | null | undefined;

export interface RequestOptions {
  /** Appended as a query string; null/undefined entries are dropped. */
  query?: Record<string, QueryValue>;
  /** Caller-supplied abort signal, combined with the timeout. */
  signal?: AbortSignal;
  /** Sets the Idempotency-Key header (CLAUDE.md rule 6). */
  idempotencyKey?: string;
  headers?: Record<string, string>;
  /** Overrides the client's default timeout for this one call. */
  timeoutMs?: number;
  /**
   * Suppresses the refresh-and-retry hook for this request.
   *
   * Required on the auth endpoints themselves. Without it, a 401 from
   * `/auth/refresh` asks the app to refresh, which is the call that just
   * failed — and because refreshes are single-flight, the retry would await
   * the very promise it is running inside. That is a deadlock, not a loop: the
   * app hangs with a spinner and no error, forever.
   */
  skipReauth?: boolean;
}

export interface ApiClient {
  get<T>(path: string, options?: RequestOptions): Promise<T>;
  post<T>(path: string, body?: unknown, options?: RequestOptions): Promise<T>;
  put<T>(path: string, body?: unknown, options?: RequestOptions): Promise<T>;
  patch<T>(path: string, body?: unknown, options?: RequestOptions): Promise<T>;
  delete<T>(path: string, options?: RequestOptions): Promise<T>;
}

export interface ApiClientOptions {
  /** Gateway base URL, e.g. http://192.168.1.20:8080/api. */
  baseUrl: string;
  /**
   * Returns the current access token, or null when signed out. A callback so
   * the client always reads the freshest token after a refresh instead of
   * capturing a stale one at construction.
   */
  getAuthToken?: () => string | null | undefined;
  /**
   * Called on any 401.
   *
   * Resolve `true` to say "I have obtained a fresh access token, send it
   * again" and the request is retried EXACTLY once; resolve anything else and
   * the 401 is thrown to the caller.
   *
   * This hook does not exist in the web client, and the difference is the
   * platform. A browser tab is reloaded, which re-runs the refresh flow. A
   * phone app is backgrounded for hours and resumed — with a 15-minute access
   * token, every single resume would otherwise dump the customer back on the
   * sign-in screen — with a full cart behind it. Retrying once, and only once,
   * keeps a failing refresh from turning into a loop.
   */
  onUnauthorized?: (error: ApiError) => boolean | void | Promise<boolean | void>;
  /** Overridable for tests. */
  fetchImpl?: typeof fetch;
  /**
   * Per-request timeout. 30s by default.
   *
   * Long by web standards, on purpose: a phone drops to 3G without warning,
   * and an order that would have been placed in 12 seconds must not be
   * cancelled at 8 — leaving the customer unsure whether it went through.
   */
  timeoutMs?: number;
  /** Overridable for tests, so assertions can pin the request id. */
  requestIdFactory?: () => string;
}

/**
 * Generates a request id.
 *
 * Not a UUID and not trying to be. `crypto.randomUUID` does not exist in
 * Hermes, and pulling in a polyfill for a value whose only job is to correlate
 * one client request with one server log line is not worth the bytes. Time
 * plus randomness is unique enough for that, and the prefix makes an id that
 * originated on a phone obvious in a log search.
 */
export function newRequestId(): string {
  const time = Date.now().toString(36);
  const random = Math.random().toString(36).slice(2, 10);
  return `mob-${time}-${random}`;
}

/**
 * Joins a base URL and a path.
 *
 * Concatenation, not URL resolution, and for a reason worth not rediscovering:
 * `new URL("/auth/login", "http://host/api/")` yields "http://host/auth/login",
 * because a leading slash makes the reference root-relative and the "/api"
 * mount point disappears. Normalising both sides means a caller can pass
 * "products" or "/products", with or without a trailing slash on the base,
 * and never produce "//".
 *
 * Exported for tests.
 */
export function buildUrl(
  baseUrl: string,
  path: string,
  query: Record<string, QueryValue> | undefined,
): string {
  const base = baseUrl.replace(/\/+$/, "");
  const suffix = path.startsWith("/") ? path : `/${path}`;

  const pairs: string[] = [];
  if (query) {
    for (const [key, value] of Object.entries(query)) {
      if (value === undefined || value === null) continue;
      pairs.push(`${encodeURIComponent(key)}=${encodeURIComponent(String(value))}`);
    }
  }

  return pairs.length > 0 ? `${base}${suffix}?${pairs.join("&")}` : `${base}${suffix}`;
}

/** Decodes an error response into an ApiError, tolerating a non-JSON body. */
async function toApiError(
  response: Response,
  requestId: string | undefined,
): Promise<ApiError> {
  let code = "UNKNOWN_ERROR";
  let message = `Request failed with status ${response.status}.`;
  let details: Record<string, unknown> | undefined;

  try {
    const body = (await response.json()) as Partial<ApiErrorEnvelope>;
    if (body.error) {
      code = body.error.code ?? code;
      message = body.error.message ?? message;
      details = body.error.details;
    }
  } catch {
    // A proxy error page or a gateway timeout is not JSON. The status-derived
    // message above is the best available and is still actionable.
  }

  return new ApiError(response.status, code, message, { details, requestId });
}

/**
 * Runs `fetch` under a deadline.
 *
 * The replacement for `AbortSignal.timeout` + `AbortSignal.any`. Returns which
 * of the two aborts fired, because the caller has to tell "the network gave
 * up" (report it, offer a retry) from "the screen was closed" (say nothing).
 */
async function fetchWithDeadline(
  fetchImpl: typeof fetch,
  url: string,
  init: RequestInit,
  timeoutMs: number,
  callerSignal: AbortSignal | undefined,
): Promise<{ response: Response } | { timedOut: boolean }> {
  const controller = new AbortController();
  let timedOut = false;

  const timer = setTimeout(() => {
    timedOut = true;
    controller.abort();
  }, timeoutMs);

  const forwardAbort = () => controller.abort();
  if (callerSignal) {
    if (callerSignal.aborted) forwardAbort();
    else callerSignal.addEventListener("abort", forwardAbort);
  }

  try {
    const response = await fetchImpl(url, { ...init, signal: controller.signal });
    return { response };
  } catch {
    // Every failure path lands here: offline, DNS, TLS, our timeout, or the
    // caller's abort. Which one it was is `timedOut` plus the caller's signal.
    return { timedOut };
  } finally {
    clearTimeout(timer);
    callerSignal?.removeEventListener("abort", forwardAbort);
  }
}

export function createApiClient(options: ApiClientOptions): ApiClient {
  const {
    baseUrl,
    getAuthToken,
    onUnauthorized,
    fetchImpl = ((...args) => fetch(...args)) as typeof fetch,
    timeoutMs: defaultTimeoutMs = 30_000,
    requestIdFactory = newRequestId,
  } = options;

  function buildHeaders(
    requestId: string,
    requestOptions: RequestOptions,
    jsonBody: boolean,
  ): Record<string, string> {
    const headers: Record<string, string> = {
      Accept: "application/json",
      [REQUEST_ID_HEADER]: requestId,
      ...requestOptions.headers,
    };

    const token = getAuthToken?.();
    if (token) headers["Authorization"] = `Bearer ${token}`;
    if (requestOptions.idempotencyKey) {
      headers["Idempotency-Key"] = requestOptions.idempotencyKey;
    }
    if (jsonBody) headers["Content-Type"] = "application/json";

    return headers;
  }

  async function send(
    method: string,
    path: string,
    init: { body?: BodyInit; json: boolean },
    requestOptions: RequestOptions,
    /** False on the retry after a refresh, so a 401 can only be retried once. */
    mayRetryAfterRefresh = true,
  ): Promise<Response> {
    const requestId = requestIdFactory();
    const url = buildUrl(baseUrl, path, requestOptions.query);

    const outcome = await fetchWithDeadline(
      fetchImpl,
      url,
      {
        method,
        headers: buildHeaders(requestId, requestOptions, init.json),
        ...(init.body !== undefined && { body: init.body }),
      },
      requestOptions.timeoutMs ?? defaultTimeoutMs,
      requestOptions.signal,
    );

    if (!("response" in outcome)) {
      if (requestOptions.signal?.aborted && !outcome.timedOut) {
        // The screen went away. Nothing to report to anyone.
        throw new ApiError(0, "REQUEST_CANCELLED", "The request was cancelled.", {
          requestId,
        });
      }
      throw new ApiError(
        0,
        outcome.timedOut ? "REQUEST_TIMEOUT" : "NETWORK_ERROR",
        outcome.timedOut
          ? "That took too long. Check your signal and try again."
          : "Could not reach Vayalavan. Check your internet connection.",
        { requestId },
      );
    }

    const { response } = outcome;
    // Prefer the id the server echoed: behind a proxy that is the one that
    // actually appears in the service logs.
    const serverRequestId = response.headers.get(REQUEST_ID_HEADER) ?? requestId;

    if (!response.ok) {
      const error = await toApiError(response, serverRequestId);

      if (response.status === 401 && !requestOptions.skipReauth) {
        const refreshed = await onUnauthorized?.(error);
        if (refreshed === true && mayRetryAfterRefresh) {
          // A form body is a one-shot stream in some fetch implementations, so
          // the retry reuses the same `init` object rather than rebuilding it.
          // Headers ARE rebuilt, which is the point: getAuthToken now returns
          // the new token.
          return send(method, path, init, requestOptions, false);
        }
      }

      throw error;
    }

    return response;
  }

  async function json<T>(response: Response): Promise<T> {
    // 204 No Content, or any empty body.
    if (response.status === 204 || response.headers.get("Content-Length") === "0") {
      return undefined as T;
    }
    const text = await response.text();
    if (text === "") return undefined as T;
    return JSON.parse(text) as T;
  }

  async function request<T>(
    method: string,
    path: string,
    body: unknown,
    requestOptions: RequestOptions = {},
  ): Promise<T> {
    const response = await send(
      method,
      path,
      body === undefined
        ? { json: false }
        : { body: JSON.stringify(body), json: true },
      requestOptions,
    );
    return json<T>(response);
  }

  return {
    get: (path, opts) => request("GET", path, undefined, opts),
    post: (path, body, opts) => request("POST", path, body, opts),
    put: (path, body, opts) => request("PUT", path, body, opts),
    patch: (path, body, opts) => request("PATCH", path, body, opts),
    delete: (path, opts) => request("DELETE", path, undefined, opts),
  };
}
