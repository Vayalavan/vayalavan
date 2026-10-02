/**
 * Runtime configuration, read from the manifest Expo baked at build time.
 *
 * The real fail-fast lives in app.config.ts, which throws during the build if
 * a required variable is missing (CLAUDE.md rule 3). A native binary cannot be
 * reconfigured afterwards, so by the time this module runs the values are
 * either present or the build never happened.
 *
 * This second check exists for the one case app.config.ts cannot cover: an
 * over-the-air update or a hand-edited manifest arriving with a field removed.
 * It reports the problem as a value the UI can render, rather than throwing at
 * module load — a throw here is a white screen with no explanation, which is
 * the worst possible outcome on a supplier's phone.
 */
import Constants from "expo-constants";

export interface AppConfig {
  /** Gateway base URL including the /api mount point. */
  readonly apiBaseUrl: string;
  readonly supportEmail: string;
  readonly appEnv: string;
  /** Shown on the account screen, so a bug report can name a build. */
  readonly appVersion: string;
}

export type ConfigResult =
  | { ok: true; config: AppConfig }
  | { ok: false; message: string };

interface ExtraShape {
  apiBaseUrl?: unknown;
  supportEmail?: unknown;
  appEnv?: unknown;
}

function readString(extra: ExtraShape, key: keyof ExtraShape): string | null {
  const value = extra[key];
  if (typeof value !== "string" || value.trim() === "") return null;
  return value.trim();
}

function load(): ConfigResult {
  const extra = (Constants.expoConfig?.extra ?? {}) as ExtraShape;

  const apiBaseUrl = readString(extra, "apiBaseUrl");
  const supportEmail = readString(extra, "supportEmail");

  const missing: string[] = [];
  if (!apiBaseUrl) missing.push("MOBILE_API_BASE_URL");
  if (!supportEmail) missing.push("SUPPORT_EMAIL");

  if (!apiBaseUrl || !supportEmail) {
    return {
      ok: false,
      message:
        `This build is missing required configuration: ${missing.join(", ")}. ` +
        `It cannot reach the Vayal servers. Please reinstall from a correctly ` +
        `built release.`,
    };
  }

  return {
    ok: true,
    config: {
      apiBaseUrl,
      supportEmail,
      appEnv: readString(extra, "appEnv") ?? "production",
      appVersion: Constants.expoConfig?.version ?? "0.0.0",
    },
  };
}

export const configResult: ConfigResult = load();

/**
 * The configuration, for code that only runs once <ConfigGate> has passed.
 *
 * Throws if read while the config is invalid, which by then can only be a
 * programming error — the gate renders an explanation instead of the app.
 */
export function useConfigOrThrow(): AppConfig {
  if (!configResult.ok) throw new Error(configResult.message);
  return configResult.config;
}

/**
 * The config as a plain value, for modules constructed at import time (the API
 * client). Falls back to an empty base URL when misconfigured; every request
 * then fails visibly rather than silently pointing somewhere unintended.
 */
export const config: AppConfig = configResult.ok
  ? configResult.config
  : { apiBaseUrl: "", supportEmail: "", appEnv: "unknown", appVersion: "0.0.0" };
