/**
 * The Razorpay webhook route.
 *
 * CLAUDE.md §6.4 step 4: the signature is an HMAC over the RAW request body.
 * express.json() would parse and discard those exact bytes, and re-serialising
 * the object changes key order and whitespace — so the signature would never
 * verify again.
 *
 * This route is therefore mounted BEFORE the JSON parser and forwards the
 * bytes to vm-orders-api verbatim. Choosing to proxy rather than point
 * Razorpay straight at orders-api keeps CLAUDE.md rule 5 intact: the gateway
 * stays the only publicly reachable service.
 */
import express, { Router, type Request, type Response } from "express";
import type { Config } from "./config.js";
import { AppError } from "./errors.js";
import { REQUEST_ID_HEADER } from "./logger.js";

/**
 * Razorpay payloads are a few KB. This ceiling stops an unbounded body being
 * buffered by an endpoint that, by design, anyone on the internet can POST to.
 */
const MAX_WEBHOOK_BODY = "256kb";

/** Bounds the upstream call so a slow orders-api cannot pin a gateway worker. */
const UPSTREAM_TIMEOUT_MS = 15_000;

export function razorpayWebhookRoute(config: Config): Router {
  const router = Router();

  router.post(
    "/webhooks/razorpay",
    // type: () => true accepts whatever content type Razorpay sends, and
    // hands us a Buffer rather than a parsed object.
    express.raw({ type: () => true, limit: MAX_WEBHOOK_BODY }),
    async (req: Request, res: Response, next) => {
      try {
        const rawBody = req.body as Buffer;
        if (!Buffer.isBuffer(rawBody) || rawBody.length === 0) {
          throw AppError.badRequest("Empty webhook body.");
        }

        // Forwarded verbatim. The signature and event id headers are the whole
        // point of this route; orders-api verifies and dedupes on them.
        const headers = new Headers({
          "Content-Type": req.header("content-type") ?? "application/json",
          "X-Internal-Token": config.INTERNAL_SERVICE_TOKEN,
          [REQUEST_ID_HEADER]: req.requestId,
        });
        for (const header of ["x-razorpay-signature", "x-razorpay-event-id"]) {
          const value = req.header(header);
          if (value) headers.set(header, value);
        }

        const upstream = await fetch(
          new URL("/webhooks/razorpay", config.ORDERS_API_URL),
          {
            method: "POST",
            headers,
            // The Buffer, untouched — no JSON.parse, no re-stringify.
            body: rawBody,
            signal: AbortSignal.timeout(UPSTREAM_TIMEOUT_MS),
          },
        );

        const text = await upstream.text();

        // Log the outcome: a run of signature failures is worth alerting on,
        // and this is the only place that sees them at the edge.
        if (upstream.status >= 400) {
          req.log.warn(
            {
              status: upstream.status,
              event_id: req.header("x-razorpay-event-id"),
              body_bytes: rawBody.length,
            },
            "razorpay webhook rejected upstream",
          );
        }

        res.status(upstream.status);
        if (!text) {
          res.end();
          return;
        }
        res.type("application/json").send(text);
      } catch (err) {
        next(err);
      }
    },
  );

  return router;
}
