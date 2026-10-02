/**
 * The app's single API client instance.
 *
 * Import `api` anywhere; never construct another client, or requests would
 * bypass the auth token and request-id wiring.
 */
import { createApiClient } from "@vayal/ui-kit";
import { config } from "./config.js";

/**
 * In-memory access token.
 *
 * Deliberately not localStorage: a token in localStorage is readable by any
 * XSS payload on the page. Keeping it in memory means a refresh signs the
 * user out until the refresh-token flow lands, which is the right trade for
 * a store that handles payments. Revisit alongside the httpOnly refresh
 * cookie in vm-profile-api.
 */
let accessToken: string | null = null;

export function setAccessToken(token: string | null): void {
  accessToken = token;
}

export function getAccessToken(): string | null {
  return accessToken;
}

export const api = createApiClient({
  baseUrl: config.apiBaseUrl,
  getAuthToken: () => accessToken,
  onUnauthorized: () => {
    // The session is gone. Clear it so no further request carries a token
    // the server has already rejected. Redirect handling belongs to the
    // router once real auth routes exist.
    accessToken = null;
  },
});
