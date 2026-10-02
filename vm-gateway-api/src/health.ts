/**
 * Liveness and readiness probes (CLAUDE.md rule 8).
 */
import { Router, type Request, type Response } from "express";
import type { Config } from "./config.js";
import { SERVICE_NAME } from "./config.js";

/** Bounds each downstream probe so a hung service cannot hang our readiness. */
const PROBE_TIMEOUT_MS = 2_000;

interface HealthResponse {
  status: string;
  service: string;
  checks?: Record<string, string>;
}

/** Probes one downstream service's /healthz. */
async function probe(name: string, baseUrl: string): Promise<[string, boolean]> {
  try {
    const response = await fetch(new URL("/healthz", baseUrl), {
      signal: AbortSignal.timeout(PROBE_TIMEOUT_MS),
    });
    return [name, response.ok];
  } catch {
    // Unreachable, DNS failure, or timed out — all mean "not ready".
    return [name, false];
  }
}

export function healthRoutes(config: Config): Router {
  const router = Router();

  /**
   * Liveness. Deliberately checks nothing downstream.
   *
   * Liveness answers "should this process be restarted?", and restarting the
   * gateway does not fix a Postgres outage. Wiring dependency checks in here
   * turns one service blip into a platform-wide restart loop.
   */
  router.get("/healthz", (_req: Request, res: Response) => {
    const body: HealthResponse = { status: "ok", service: SERVICE_NAME };
    res.status(200).json(body);
  });

  /**
   * Readiness. The gateway can only serve traffic if the services it proxies
   * to are reachable, so all three are probed in parallel.
   */
  router.get("/readyz", async (_req: Request, res: Response) => {
    const results = await Promise.all([
      probe("profile_api", config.PROFILE_API_URL),
      probe("catalog_api", config.CATALOG_API_URL),
      probe("orders_api", config.ORDERS_API_URL),
    ]);

    const checks: Record<string, string> = {};
    let ready = true;
    for (const [name, ok] of results) {
      checks[name] = ok ? "ok" : "failed";
      if (!ok) ready = false;
    }

    const body: HealthResponse = {
      status: ready ? "ok" : "unavailable",
      service: SERVICE_NAME,
      checks,
    };
    res.status(ready ? 200 : 503).json(body);
  });

  return router;
}
