/**
 * Structured JSON logging for vm-gateway-api.
 *
 * CLAUDE.md rule 8: structured JSON logs carrying a request_id. The gateway
 * is where that id is minted for most traffic — it then travels to the Go
 * services in the X-Request-Id header, so one customer action is one
 * traceable id across all four services.
 */
import pino from "pino";
import { SERVICE_NAME } from "./config.js";

/** The header the request id travels in, between gateway and services. */
export const REQUEST_ID_HEADER = "x-request-id";

export type Logger = pino.Logger;

/**
 * Builds the root logger.
 *
 * JSON in every environment, including development: a log format that
 * differs between local and production is a format whose parsing bugs are
 * only discovered in production.
 */
export function createLogger(level: string): Logger {
  return pino({
    level,
    base: { service: SERVICE_NAME },
    // Emit "level":"info" rather than pino's default numeric level, matching
    // what slog produces in the Go services so one log query covers both.
    formatters: {
      level: (label) => ({ level: label }),
    },
    timestamp: pino.stdTimeFunctions.isoTime,
    redact: {
      // Belt and braces: these must never be logged even if a future handler
      // dumps a whole request object.
      paths: [
        "req.headers.authorization",
        "req.headers.cookie",
        'req.headers["x-internal-token"]',
        'req.headers["x-razorpay-signature"]',
      ],
      censor: "[redacted]",
    },
  });
}
