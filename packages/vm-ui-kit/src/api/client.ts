/**
 * The typed API client shared by all three UIs.
 *
 * Two things every request must carry, and which no call site should have to
 * remember:
 *   - the auth token, as a Bearer header
 *   - an X-Request-Id, so a failure in the browser can be traced through the
 *     gateway and into the Go services (CLAUDE.md rule 8)
 *
 * It also decodes the platform's single error envelope into a typed
 * ApiError, so UI code branches on a stable `code` rather than parsing
 * messages.
 */

/** The header the request id travels in. Matches the Go services. */
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
 * `code` is the stable identifier to branch on. `requestId` is worth showing
 * the user on an error screen — it is the one string that lets support find
 * the exact request in the logs.
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

  /** True when the failure is a network or timeout problem, not a response. */
  get isNetworkError(): boolean {
    return this.status === 0;
  }
}

export interface ApiClientOptions {
  /** Gateway base URL, e.g. http://localhost:8080. */
  baseUrl: string;
  /**
   * Returns the current access token, or null when signed out. A callback
   * rather than a value so the client always reads the freshest token after
   * a refresh, instead of capturing a stale one at construction.
   */
  getAuthToken?: () => string | null | undefined;
  /**
   * Called on any 401, for the app to clear its session and redirect. Kept
   * as a hook so vm-ui-kit stays router-agnostic.
   */
  onUnauthorized?: (error: ApiError) => void;
  /** Overridable for tests. */
  fetchImpl?: typeof fetch;
  /** Per-request timeout. Defaults to 30s. */
  timeoutMs?: number;
}

export type QueryValue = string | number | boolean | null | undefined;

export interface RequestOptions {
  /** Appended as a query string; null/undefined entries are dropped. */
  query?: Record<string, QueryValue>;
  /** Caller-supplied abort signal, combined with the timeout. */
  signal?: AbortSignal;
  /**
   * Sets the Idempotency-Key header. Required by CLAUDE.md rule 6 on any
   * write that moves money or stock.
   */
  idempotencyKey?: string;
  headers?: Record<string, string>;
}

export interface ApiClient {
  get<T>(path: string, options?: RequestOptions): Promise<T>;
  post<T>(path: string, body?: unknown, options?: RequestOptions): Promise<T>;
  put<T>(path: string, body?: unknown, options?: RequestOptions): Promise<T>;
  patch<T>(path: string, body?: unknown, options?: RequestOptions): Promise<T>;
  delete<T>(path: string, options?: RequestOptions): Promise<T>;
}

/**
 * Generates a request id.
 *
 * randomUUID is unavailable outside secure contexts (and localhost counts as
 * one), so a Math.random fallback keeps correlation working everywhere
 * rather than throwing on an insecure origin.
 */
export function newRequestId(): string {
  if (typeof crypto !== "undefined" && typeof crypto.randomUUID === "function") {
    return crypto.randomUUID();
  }
  return `req-${Date.now().toString(16)}-${Math.random().toString(16).slice(2, 10)}`;
}

/**
 * Exported for tests only — not re-exported from the package index. Callers
 * should use the client, which applies auth and request-id headers too.
 */
export function buildUrl(
  baseUrl: string,
  path: string,
  query: Record<string, QueryValue> | undefined,
): string {
  // Joined by string concatenation rather than URL resolution, because
  // resolution silently drops the base path.
  //
  // `new URL("/auth/login", "http://host/api/")` yields "http://host/auth/login":
  // a leading slash makes the reference root-relative, so the "/api" mount
  // point disappears. Every call from every UI 404'd while the same path
  // worked under curl, because curl was given the full URL directly.
  //
  // Concatenating keeps the base path intact. Normalising both sides means a
  // caller can pass "products" or "/products", and a base with or without a
  // trailing slash, without ever producing "//".
  const base = baseUrl.replace(/\/+$/, "");
  const suffix = path.startsWith("/") ? path : `/${path}`;
  const url = new URL(`${base}${suffix}`);

  if (query) {
    for (const [key, value] of Object.entries(query)) {
      if (value !== undefined && value !== null) {
        url.searchParams.set(key, String(value));
      }
    }
  }
  return url.toString();
}

/** Decodes an error response into an ApiError, tolerating a non-JSON body. */
async function toApiError(response: Response, requestId: string | undefined): Promise<ApiError> {
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
    // A gateway timeout or proxy error page is not JSON. The status-derived
    // message above is the best available and is still actionable.
  }

  return new ApiError(response.status, code, message, { details, requestId });
}

export function createApiClient(options: ApiClientOptions): ApiClient {
  const {
    baseUrl,
    getAuthToken,
    onUnauthorized,
    fetchImpl = globalThis.fetch.bind(globalThis),
    timeoutMs = 30_000,
  } = options;

  async function request<T>(
    method: string,
    path: string,
    body: unknown,
    requestOptions: RequestOptions = {},
  ): Promise<T> {
    const requestId = newRequestId();

    const headers = new Headers({
      Accept: "application/json",
      [REQUEST_ID_HEADER]: requestId,
      ...requestOptions.headers,
    });

    const token = getAuthToken?.();
    if (token) {
      headers.set("Authorization", `Bearer ${token}`);
    }
    if (requestOptions.idempotencyKey) {
      headers.set("Idempotency-Key", requestOptions.idempotencyKey);
    }
    if (body !== undefined) {
      headers.set("Content-Type", "application/json");
    }

    // Combine the caller's signal with the timeout, so either can abort.
    const timeoutSignal = AbortSignal.timeout(timeoutMs);
    const signal = requestOptions.signal
      ? AbortSignal.any([requestOptions.signal, timeoutSignal])
      : timeoutSignal;

    let response: Response;
    try {
      response = await fetchImpl(buildUrl(baseUrl, path, requestOptions.query), {
        method,
        headers,
        // Cookies are sent so an httpOnly refresh token can ride along.
        credentials: "include",
        signal,
        ...(body !== undefined && { body: JSON.stringify(body) }),
      });
    } catch (cause) {
      // Offline, DNS failure, CORS rejection, or timeout. Status 0 marks the
      // difference from a real HTTP failure, which callers may want to
      // retry differently.
      const aborted = cause instanceof DOMException && cause.name === "TimeoutError";
      throw new ApiError(
        0,
        aborted ? "REQUEST_TIMEOUT" : "NETWORK_ERROR",
        aborted
          ? "The request took too long. Please check your connection and try again."
          : "Could not reach the server. Please check your connection.",
        { requestId },
      );
    }

    // Prefer the id the server echoed: on a retry through a proxy it is the
    // one that actually appears in the service logs.
    const serverRequestId = response.headers.get(REQUEST_ID_HEADER) ?? requestId;

    if (!response.ok) {
      const error = await toApiError(response, serverRequestId);
      if (response.status === 401) {
        onUnauthorized?.(error);
      }
      throw error;
    }

    // 204 No Content, or any empty body.
    if (response.status === 204 || response.headers.get("Content-Length") === "0") {
      return undefined as T;
    }

    return (await response.json()) as T;
  }

  return {
    get: (path, options) => request("GET", path, undefined, options),
    post: (path, body, options) => request("POST", path, body, options),
    put: (path, body, options) => request("PUT", path, body, options),
    patch: (path, body, options) => request("PATCH", path, body, options),
    delete: (path, options) => request("DELETE", path, undefined, options),
  };
}
