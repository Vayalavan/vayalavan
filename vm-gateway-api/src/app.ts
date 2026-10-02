/**
 * Express application assembly.
 *
 * Kept separate from index.ts so the app can be constructed in a test
 * without binding a port.
 */
import express, { type Express } from "express";
import helmet from "helmet";
import type { Config } from "./config.js";
import type { Logger } from "./logger.js";
import { requestContext, accessLog, cors, rateLimit } from "./middleware.js";
import { errorHandler, notFoundHandler } from "./errors.js";
import { healthRoutes } from "./health.js";
import { apiRoutes } from "./routes.js";
import { importUploadRoute } from "./imports.js";
import { razorpayWebhookRoute } from "./webhook.js";

/**
 * Body size ceiling. Generous for JSON (the largest legitimate payload is a
 * supplier application) and far below anything that could exhaust memory.
 * Image and CSV uploads never pass through here — they go directly to object
 * storage via presigned URLs (CLAUDE.md §6.6).
 */
const MAX_BODY_SIZE = "64kb";

export function createApp(config: Config, logger: Logger): Express {
  const app = express();

  // Behind a load balancer, req.ip must come from X-Forwarded-For or every
  // client shares the balancer's IP and rate limiting collapses into one
  // global bucket. "loopback" trusts only a local proxy — tighten to the
  // real proxy hop count when the deployment topology is known.
  app.set("trust proxy", "loopback");
  app.disable("x-powered-by");

  // Security headers. CSP is disabled here because this process serves JSON
  // only — the three UIs are separate origins and set their own policy; a CSP
  // on an API response protects nothing and confuses debugging.
  app.use(
    helmet({
      contentSecurityPolicy: false,
      // API responses are not framed; DENY is the safe default.
      frameguard: { action: "deny" },
      // Do not leak the full URL of an internal page to third parties.
      referrerPolicy: { policy: "no-referrer" },
    }),
  );

  // Order matters: request id first so every later log line is correlated,
  // then access logging, then policy.
  app.use(requestContext(logger));
  app.use(accessLog());
  app.use(cors(config));
  app.use(rateLimit(config));

  // Health routes mount before the body parser: probes send no body, and
  // this keeps them cheap.
  app.use(healthRoutes(config));

  // NOTE: the Razorpay webhook route (CLAUDE.md §6.4) must NOT use this JSON
  // parser. Its signature is computed over the raw, unparsed body, and
  // re-serialising parsed JSON changes the bytes and breaks verification.
  // Mount it with express.raw({ type: "application/json" }) above this line,
  // or route the webhook directly to vm-orders-api.
  // BOTH of these must be mounted BEFORE express.json().
  //
  // The CSV upload is multipart — the JSON parser would consume the stream and
  // leave the body empty. The Razorpay webhook is signed over its RAW bytes,
  // and parsing then re-serialising would break the signature permanently.
  app.use(importUploadRoute(config));
  app.use(razorpayWebhookRoute(config));

  app.use(express.json({ limit: MAX_BODY_SIZE }));

  app.use("/api", apiRoutes(config));

  app.use(notFoundHandler);
  app.use(errorHandler(logger));

  return app;
}
