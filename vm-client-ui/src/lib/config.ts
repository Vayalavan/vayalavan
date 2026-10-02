/**
 * Frontend configuration, read from Vite's import.meta.env.
 *
 * CLAUDE.md rule 3: no hardcoded URLs, and the app fails fast with a clear
 * message when a required variable is missing. Failing at module load means
 * a misconfigured build breaks immediately and obviously, rather than
 * producing a "fetch to undefined/api/products" error deep in a page.
 *
 * Everything here ships to the browser in plain text. Secrets must never be
 * VITE_-prefixed.
 */

function required(name: string, value: string | undefined): string {
  if (!value || value.trim() === "") {
    throw new Error(
      `Missing required environment variable ${name}. ` +
        `Copy .env.example to .env and set it.`,
    );
  }
  return value.trim();
}

export interface AppConfig {
  readonly apiBaseUrl: string;
  readonly supportEmail: string;
  readonly appName: string;
  /** Razorpay PUBLIC key id. The secret never reaches the browser. */
  readonly razorpayKeyId: string;
}

export const config: AppConfig = {
  apiBaseUrl: required("VITE_API_BASE_URL", import.meta.env.VITE_API_BASE_URL),
  supportEmail: required("VITE_SUPPORT_EMAIL", import.meta.env.VITE_SUPPORT_EMAIL),
  appName: required("VITE_APP_NAME", import.meta.env.VITE_APP_NAME),
  razorpayKeyId: required("VITE_RAZORPAY_KEY_ID", import.meta.env.VITE_RAZORPAY_KEY_ID),
};
