/**
 * The app's single API client instance.
 *
 * Import `api` anywhere; never construct another, or requests would bypass the
 * auth token and request-id wiring.
 */
import { createApiClient, type ApiError } from "./api";
import { config } from "./config";

/**
 * In-memory access token.
 *
 * Not persisted, and not for the same reason as the web app. On a phone there
 * is no XSS to defend against, but there is a device that gets lost, shared,
 * or handed to a shop assistant. A 15-minute access token that dies with the
 * process is the cheap half of that; the refresh token is what persists, and
 * it lives in the platform keystore — see auth.tsx.
 */
let accessToken: string | null = null;

export function setAccessToken(token: string | null): void {
  accessToken = token;
}

export function getAccessToken(): string | null {
  return accessToken;
}

/**
 * Called when the server rejects the access token.
 *
 * Registered by AuthProvider at mount. Kept as a mutable hook rather than an
 * import so this module stays free of React and of the auth module, which
 * imports it — a cycle otherwise.
 */
let unauthorizedHandler: ((error: ApiError) => void) | null = null;

export function onUnauthorized(handler: ((error: ApiError) => void) | null): void {
  unauthorizedHandler = handler;
}

export const api = createApiClient({
  baseUrl: config.apiBaseUrl,
  getAuthToken: () => accessToken,
  onUnauthorized: (error) => {
    // Clear first, so no further request carries a token the server has
    // already rejected, whatever the handler decides to do about it.
    accessToken = null;
    unauthorizedHandler?.(error);
  },
});
