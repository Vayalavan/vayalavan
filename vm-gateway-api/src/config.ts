/**
 * Configuration loading for vm-gateway-api.
 *
 * CLAUDE.md rule 3: everything configurable comes from env vars, and the
 * service fails fast with a clear message when a required one is missing.
 * zod gives us that for free — it reports every problem at once rather than
 * dying on the first, so a misconfigured deploy is one fix, not five.
 */
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { z } from "zod";

export const SERVICE_NAME = "vm-gateway-api";

/** This service's slot in the platform port map. */
const DEFAULT_PORT = 8080;

/**
 * Splits a comma-separated env value into a trimmed, non-empty list.
 * Used for CORS_ALLOWED_ORIGINS.
 */
const csv = z
  .string()
  .transform((raw) =>
    raw
      .split(",")
      .map((part) => part.trim())
      .filter((part) => part.length > 0),
  );

/**
 * Parses a Go-style duration ("15s", "500ms", "2m") into milliseconds.
 *
 * The single root .env is shared with three Go services, so SHUTDOWN_TIMEOUT
 * is written in Go's duration syntax. Parsing it here beats introducing a
 * second variable that means the same thing in different units.
 */
const goDuration = z
  .string()
  .default("15s")
  .transform((raw, ctx) => {
    const match = /^(\d+(?:\.\d+)?)(ms|s|m|h)$/.exec(raw.trim());
    if (!match) {
      ctx.addIssue({
        code: z.ZodIssueCode.custom,
        message: `expected a duration like "15s", "500ms" or "2m", got ${JSON.stringify(raw)}`,
      });
      return z.NEVER;
    }
    const value = Number(match[1]);
    const unit = match[2] as "ms" | "s" | "m" | "h";
    const factor = { ms: 1, s: 1_000, m: 60_000, h: 3_600_000 }[unit];
    return value * factor;
  });

const envSchema = z.object({
  APP_ENV: z
    .enum(["development", "staging", "production"])
    .default("development"),
  LOG_LEVEL: z.enum(["debug", "info", "warn", "error"]).default("info"),
  // Prefixed: seven processes share one .env, so no service owns a bare PORT.
  GATEWAY_PORT: z.coerce.number().int().positive().default(DEFAULT_PORT),
  SHUTDOWN_TIMEOUT: goDuration,

  CORS_ALLOWED_ORIGINS: csv.default(""),

  // No defaults: a gateway silently pointing at localhost in production
  // would fail on the first customer request rather than at boot.
  PROFILE_API_URL: z.string().url(),
  CATALOG_API_URL: z.string().url(),
  ORDERS_API_URL: z.string().url(),
  // vm-analytics-api (Python). Read-only reports over the analytics schema.
  ANALYTICS_API_URL: z.string().url(),

  // Secrets are required and never defaulted. A predictable JWT signing key
  // is an authentication bypass, not a configuration inconvenience.
  JWT_SECRET: z.string().min(1, "JWT_SECRET is required"),
  INTERNAL_SERVICE_TOKEN: z
    .string()
    .min(1, "INTERNAL_SERVICE_TOKEN is required"),

  RATE_LIMIT_WINDOW_MS: z.coerce.number().int().positive().default(60_000),
  RATE_LIMIT_MAX: z.coerce.number().int().positive().default(600),
});

export type Config = z.infer<typeof envSchema> & {
  readonly serviceName: string;
  readonly isProduction: boolean;
};

/**
 * Reads and validates configuration, throwing a single readable error that
 * lists every problem found.
 */
export function loadConfig(env: NodeJS.ProcessEnv = process.env): Config {
  const parsed = envSchema.safeParse(env);

  if (!parsed.success) {
    const problems = parsed.error.issues
      .map((issue) => `  - ${issue.path.join(".") || "(root)"}: ${issue.message}`)
      .join("\n");
    throw new Error(`Invalid configuration:\n${problems}`);
  }

  return {
    ...parsed.data,
    serviceName: SERVICE_NAME,
    isProduction: parsed.data.APP_ENV === "production",
  };
}

/**
 * Loads the single root .env into process.env for local development.
 *
 * Walks up from this file's own location to find the repository root, so the
 * gateway finds the same .env whether it was started by the process manager,
 * by `npm run dev` in its own directory, or by a debugger.
 *
 * Uses Node's built-in loader rather than a dependency. A missing file is
 * expected (production sets real env vars), so absence is never an error.
 */
export function loadDotEnv(): void {
  // src/config.ts -> src -> vm-gateway-api -> repository root
  const root = fileURLToPath(new URL("../..", import.meta.url));
  try {
    process.loadEnvFile(join(root, ".env"));
  } catch {
    // No .env file — the normal case outside local development.
  }
}
