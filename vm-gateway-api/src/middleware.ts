/**
 * The gateway's standard middleware chain: request id, access logging, CORS,
 * and rate limiting.
 */
import { randomUUID } from "node:crypto";
import type { Request, Response, NextFunction, RequestHandler } from "express";
import type { Config } from "./config.js";
import { REQUEST_ID_HEADER, type Logger } from "./logger.js";
import { AppError } from "./errors.js";

declare global {
  // eslint-disable-next-line @typescript-eslint/no-namespace
  namespace Express {
    interface Request {
      /** Correlation id for this request, echoed to the client. */
      requestId: string;
      /** Request-scoped logger, pre-tagged with requestId. */
      log: Logger;
    }
  }
}

/**
 * Adopts the inbound X-Request-Id or mints one, then attaches a
 * request-scoped child logger.
 *
 * Echoing the id back lets a user paste it from a failed request straight
 * into a support ticket.
 */
export function requestContext(logger: Logger): RequestHandler {
  return (req: Request, res: Response, next: NextFunction) => {
    const inbound = req.header(REQUEST_ID_HEADER);
    const requestId = inbound && inbound.trim().length > 0 ? inbound.trim() : randomUUID();

    req.requestId = requestId;
    req.log = logger.child({ request_id: requestId });
    res.setHeader(REQUEST_ID_HEADER, requestId);

    next();
  };
}

/** Paths exempt from access logging noise and from rate limiting. */
const HEALTH_PATHS = new Set(["/healthz", "/readyz"]);

/** Emits one structured line per completed request. */
export function accessLog(): RequestHandler {
  return (req: Request, res: Response, next: NextFunction) => {
    const startedAt = process.hrtime.bigint();

    res.on("finish", () => {
      const durationMs = Number(process.hrtime.bigint() - startedAt) / 1_000_000;
      const payload = {
        method: req.method,
        path: req.originalUrl,
        status: res.statusCode,
        duration_ms: Math.round(durationMs),
        remote_addr: req.ip,
      };

      // Health probes are logged at debug: an orchestrator polling every few
      // seconds would otherwise bury real traffic.
      if (HEALTH_PATHS.has(req.path)) {
        req.log.debug(payload, "request completed");
      } else if (res.statusCode >= 500) {
        req.log.error(payload, "request completed");
      } else if (res.statusCode >= 400) {
        req.log.warn(payload, "request completed");
      } else {
        req.log.info(payload, "request completed");
      }
    });

    next();
  };
}

/**
 * Allow-list CORS.
 *
 * Written by hand rather than pulled from the `cors` package because the
 * policy is three known origins and reflecting an arbitrary Origin header is
 * exactly the mistake a permissive default makes.
 */
export function cors(config: Config): RequestHandler {
  const allowed = new Set(config.CORS_ALLOWED_ORIGINS);

  return (req: Request, res: Response, next: NextFunction) => {
    const origin = req.header("origin");

    if (origin && allowed.has(origin)) {
      res.setHeader("Access-Control-Allow-Origin", origin);
      res.setHeader("Access-Control-Allow-Credentials", "true");
      // Tell caches the response varies by Origin, or a CDN could serve one
      // origin's allow header to another.
      res.setHeader("Vary", "Origin");
      res.setHeader("Access-Control-Allow-Methods", "GET,POST,PATCH,PUT,DELETE,OPTIONS");
      res.setHeader(
        "Access-Control-Allow-Headers",
        `Content-Type, Authorization, Idempotency-Key, ${REQUEST_ID_HEADER}`,
      );
      res.setHeader("Access-Control-Max-Age", "600");
    }

    if (req.method === "OPTIONS") {
      res.status(204).end();
      return;
    }

    next();
  };
}

/**
 * Strict rate limit for credential endpoints: 5 attempts per 15 minutes,
 * keyed on IP **and** the identifier being tried.
 *
 * Keying on both matters in each direction. IP alone would let one attacker
 * behind a shared NAT exhaust the budget for a whole office, and would let a
 * botnet spray one account from thousands of addresses. Identifier alone
 * would let an attacker lock a known victim out by deliberately failing
 * logins. Together, an attacker must burn a fresh IP for every five guesses
 * at a given account.
 *
 * Only failures count: a legitimate user with a working password is never
 * locked out by their own successful logins.
 */
export function credentialRateLimit(config: Config): RequestHandler {
  const WINDOW_MS = 15 * 60 * 1000;
  const MAX_ATTEMPTS = 5;

  const attempts = new Map<string, { count: number; resetAt: number }>();

  const sweep = setInterval(() => {
    const now = Date.now();
    for (const [key, entry] of attempts) {
      if (entry.resetAt <= now) attempts.delete(key);
    }
  }, WINDOW_MS);
  sweep.unref();

  // The identifier differs by endpoint: login sends `identifier`, register and
  // supplier application send `email`.
  const identifierOf = (body: unknown): string => {
    if (typeof body !== "object" || body === null) return "-";
    const record = body as Record<string, unknown>;
    for (const field of ["identifier", "email", "phone", "token"]) {
      const value = record[field];
      if (typeof value === "string" && value.trim() !== "") {
        return value.trim().toLowerCase();
      }
    }
    return "-";
  };

  return (req: Request, res: Response, next: NextFunction) => {
    const key = `${req.ip ?? "unknown"}|${identifierOf(req.body)}`;
    const now = Date.now();
    const entry = attempts.get(key);

    if (entry && entry.resetAt > now && entry.count >= MAX_ATTEMPTS) {
      const retryAfter = Math.ceil((entry.resetAt - now) / 1000);
      res.setHeader("Retry-After", String(retryAfter));
      req.log.warn(
        { path: req.originalUrl, retry_after_s: retryAfter },
        "credential rate limit exceeded",
      );
      next(
        AppError.tooManyRequests(
          "Too many attempts. Please wait a few minutes and try again.",
        ),
      );
      return;
    }

    // Count the attempt only once the outcome is known, so successes do not
    // consume the budget.
    res.on("finish", () => {
      if (res.statusCode < 400 || res.statusCode === 429) return;

      const current = attempts.get(key);
      if (!current || current.resetAt <= Date.now()) {
        attempts.set(key, { count: 1, resetAt: Date.now() + WINDOW_MS });
      } else {
        current.count += 1;
      }
    });

    void config;
    next();
  };
}

/**
 * Fixed-window in-memory rate limiter.
 *
 * In-memory is correct for a single instance and wrong the moment the
 * gateway is replicated — each replica would enforce its own window. Swap
 * for a Redis-backed counter before scaling out. Adequate now, and cheaper
 * than a dependency the foundation does not yet need.
 */
export function rateLimit(config: Config): RequestHandler {
  const hits = new Map<string, { count: number; resetAt: number }>();

  // Bounded sweep so the map cannot grow without limit from one-off IPs.
  const sweep = setInterval(() => {
    const now = Date.now();
    for (const [key, entry] of hits) {
      if (entry.resetAt <= now) hits.delete(key);
    }
  }, config.RATE_LIMIT_WINDOW_MS);
  // Do not hold the event loop open at shutdown.
  sweep.unref();

  return (req: Request, _res: Response, next: NextFunction) => {
    if (HEALTH_PATHS.has(req.path)) {
      next();
      return;
    }

    const key = req.ip ?? "unknown";
    const now = Date.now();
    const entry = hits.get(key);

    if (!entry || entry.resetAt <= now) {
      hits.set(key, { count: 1, resetAt: now + config.RATE_LIMIT_WINDOW_MS });
      next();
      return;
    }

    entry.count += 1;
    if (entry.count > config.RATE_LIMIT_MAX) {
      next(AppError.tooManyRequests());
      return;
    }

    next();
  };
}
