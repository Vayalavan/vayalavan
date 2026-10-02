/**
 * The one route that cannot go through the JSON body parser.
 *
 * CSV import is multipart/form-data. express.json() would leave req.body
 * empty and consume the stream, so this is mounted BEFORE the JSON parser and
 * forwards the raw bytes — boundary and all — to vm-catalog-api untouched.
 */
import { Router, type Request, type Response } from "express";
import express from "express";
import type { Config } from "./config.js";
import { AppError } from "./errors.js";
import { requireAuth, requireRole, Role } from "./auth.js";
import { REQUEST_ID_HEADER } from "./logger.js";

/**
 * Ceiling on the raw multipart body: the 5 MB CSV limit plus headroom for the
 * multipart envelope. vm-catalog-api enforces the real limit on the file
 * itself; this only stops an unbounded body reaching us.
 */
const MAX_UPLOAD = "6mb";

/** A slow uploader should not hold a gateway worker indefinitely. */
const UPLOAD_TIMEOUT_MS = 60_000;

export function importUploadRoute(config: Config): Router {
  const router = Router();

  router.post(
    "/api/imports",
    requireAuth(config),
    requireRole(Role.Supplier),
    // Buffered rather than streamed: a streaming body needs duplex:"half" and
    // loses the ability to retry, for a payload capped at 5 MB. Not worth it.
    express.raw({ type: () => true, limit: MAX_UPLOAD }),
    async (req: Request, res: Response, next) => {
      try {
        const contentType = req.header("content-type");
        if (!contentType?.includes("multipart/form-data")) {
          throw AppError.badRequest(
            "Upload must be multipart/form-data with a 'file' field.",
          );
        }

        const headers = new Headers({
          // Forwarded verbatim: it carries the multipart boundary, and a
          // regenerated one would not match the body.
          "Content-Type": contentType,
          "X-Internal-Token": config.INTERNAL_SERVICE_TOKEN,
          [REQUEST_ID_HEADER]: req.requestId,
        });
        if (req.identity) {
          headers.set("X-User-Id", req.identity.userId);
          headers.set("X-User-Role", req.identity.role);
          if (req.identity.supplierId) {
            headers.set("X-Supplier-Id", req.identity.supplierId);
          }
        }

        const upstream = await fetch(new URL("/imports/", config.CATALOG_API_URL), {
          method: "POST",
          headers,
          body: req.body as Buffer,
          signal: AbortSignal.timeout(UPLOAD_TIMEOUT_MS),
        });

        const text = await upstream.text();
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
