/**
 * Authentication state for the supplier app.
 *
 * Access token: memory only, gone when the process dies.
 *
 * Refresh token: expo-secure-store, which is the iOS Keychain and Android's
 * EncryptedSharedPreferences behind one API. The web app keeps its refresh
 * token in localStorage and documents that as a deliberate trade; on a phone
 * there is no such trade to make, because the platform gives us hardware-backed
 * storage for free. It is written WHEN_UNLOCKED_THIS_DEVICE_ONLY so it is never
 * carried into an encrypted backup and restored onto a different handset.
 *
 * Two things here that the web app does not need, both because a phone app is
 * suspended and resumed rather than reloaded:
 *
 *   1. Single-flight refresh. vm-profile-api rotates refresh tokens and treats
 *      a reused one as theft — it revokes every session for that account. So
 *      two concurrent refreshes do not race, they sign the supplier out of
 *      everything. Every path into a refresh goes through one shared promise.
 *   2. Refresh on 401. A 15-minute access token is always expired by the time
 *      someone reopens the app; without this, every resume is a sign-in.
 */
import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactElement,
  type ReactNode,
} from "react";
import * as SecureStore from "expo-secure-store";

import { ApiError } from "./api";
import { api, onUnauthorized, setAccessToken } from "./client";
import type { AuthResponse, SupplierUser } from "./types";

const REFRESH_TOKEN_KEY = "vayal.supplier.refresh";

const SECURE_STORE_OPTIONS: SecureStore.SecureStoreOptions = {
  // Readable only while the device is unlocked, and never restored onto
  // another device from a backup.
  keychainAccessible: SecureStore.WHEN_UNLOCKED_THIS_DEVICE_ONLY,
};

async function readRefreshToken(): Promise<string | null> {
  try {
    return await SecureStore.getItemAsync(REFRESH_TOKEN_KEY, SECURE_STORE_OPTIONS);
  } catch {
    // A keystore that will not open (a corrupted entry after an OS upgrade is
    // the usual cause) is indistinguishable from having no session. Sign in
    // again, rather than wedging the app on a storage error.
    return null;
  }
}

async function writeRefreshToken(token: string | null): Promise<void> {
  try {
    if (token === null) {
      await SecureStore.deleteItemAsync(REFRESH_TOKEN_KEY, SECURE_STORE_OPTIONS);
    } else {
      await SecureStore.setItemAsync(REFRESH_TOKEN_KEY, token, SECURE_STORE_OPTIONS);
    }
  } catch {
    // Failing to persist costs the supplier a sign-in next launch. Failing
    // loudly here would cost them the sign-in they just completed.
  }
}

export interface AuthState {
  user: SupplierUser | null;
  /** True until the stored refresh token has been tried, so routes can wait. */
  initialising: boolean;
  signIn: (identifier: string, password: string) => Promise<void>;
  signOut: () => Promise<void>;
}

const AuthContext = createContext<AuthState | null>(null);

/**
 * The one in-flight refresh, shared by every caller.
 *
 * Module scope, not a ref: React's StrictMode mounts effects twice in
 * development and the second mount gets a fresh ref — which is exactly the
 * case this has to survive. On the web app that oversight sent the same
 * refresh token twice, tripped the server's reuse detection, and revoked every
 * session; the symptom was being thrown back to sign-in on every reload.
 *
 * A `cancelled` flag is not enough. It suppresses the second RESPONSE, but the
 * second REQUEST has already gone.
 */
let refreshInFlight: Promise<AuthResponse> | null = null;

/**
 * Trades the stored refresh token for a new session.
 *
 * Every caller within one refresh window gets the same promise, so the token
 * is presented exactly once however many requests 401 at the same moment —
 * which, on resume, is all of them at once.
 */
function refreshSession(): Promise<AuthResponse> {
  if (!refreshInFlight) {
    refreshInFlight = (async () => {
      const stored = await readRefreshToken();
      if (!stored) {
        throw new ApiError(401, "NO_SESSION", "You are signed out.");
      }
      return api.post<AuthResponse>(
        "/auth/refresh",
        { refresh_token: stored },
        // See RequestOptions.skipReauth: a 401 here must NOT ask the app to
        // refresh, because this IS the refresh.
        { skipReauth: true },
      );
    })().finally(() => {
      // Cleared once settled so a later sign-out and sign-in can refresh
      // again. This dedupes concurrent calls; it does not cache a session.
      refreshInFlight = null;
    });
  }
  return refreshInFlight;
}

export function AuthProvider({ children }: { children: ReactNode }): ReactElement {
  const [user, setUser] = useState<SupplierUser | null>(null);
  const [initialising, setInitialising] = useState(true);

  /**
   * Mirrors `user` for callbacks that must not be re-registered on every sign
   * in — the 401 hook below runs outside React's render cycle.
   */
  const signedIn = useRef(false);

  const applySession = useCallback(async (response: AuthResponse) => {
    setAccessToken(response.tokens.access_token);
    await writeRefreshToken(response.tokens.refresh_token);
    signedIn.current = true;
    setUser(response.user);
  }, []);

  const clearSession = useCallback(async () => {
    setAccessToken(null);
    await writeRefreshToken(null);
    signedIn.current = false;
    setUser(null);
  }, []);

  // Register the 401 handler once, for the lifetime of the provider.
  //
  // Returning true tells the API client to retry the request with the token
  // this just obtained. Returning false lets the 401 through to the caller,
  // and the user lands back on the sign-in screen because `user` is now null.
  useEffect(() => {
    onUnauthorized(async () => {
      if (!signedIn.current) return false;
      try {
        const response = await refreshSession();
        await applySession(response);
        return true;
      } catch {
        // Expired, revoked, or invalidated by reuse detection. Either way the
        // supplier signs in again.
        await clearSession();
        return false;
      }
    });

    return () => onUnauthorized(null);
  }, [applySession, clearSession]);

  // On launch, trade the stored refresh token for a live session.
  useEffect(() => {
    let cancelled = false;

    void (async () => {
      const stored = await readRefreshToken();
      if (!stored) {
        if (!cancelled) setInitialising(false);
        return;
      }

      try {
        const response = await refreshSession();
        if (!cancelled) await applySession(response);
      } catch {
        if (!cancelled) await clearSession();
      } finally {
        if (!cancelled) setInitialising(false);
      }
    })();

    return () => {
      cancelled = true;
    };
  }, [applySession, clearSession]);

  const signIn = useCallback(
    async (identifier: string, password: string) => {
      const response = await api.post<AuthResponse>(
        "/auth/login",
        { identifier, password },
        // Wrong password is a 401 that means "wrong password", not "your
        // session lapsed". Refreshing here would be nonsense.
        { skipReauth: true },
      );
      if (response.user.role !== "supplier") {
        // A customer or admin account is valid, just not for this app.
        throw new ApiError(
          403,
          "WRONG_APP",
          "That account is not a supplier account. Suppliers only, in this app.",
        );
      }
      await applySession(response);
    },
    [applySession],
  );

  const signOut = useCallback(async () => {
    const stored = await readRefreshToken();
    // Clear locally first: the supplier is signed out from their point of view
    // whether or not the server call succeeds. On a phone that call may well
    // fail — signing out is exactly the moment someone is about to lose signal.
    await clearSession();
    if (stored) {
      await api
        .post("/auth/logout", { refresh_token: stored }, { skipReauth: true })
        .catch(() => {
          /* already signed out locally; nothing useful to report */
        });
    }
  }, [clearSession]);

  const value = useMemo<AuthState>(
    () => ({ user, initialising, signIn, signOut }),
    [user, initialising, signIn, signOut],
  );

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}

export function useAuth(): AuthState {
  const context = useContext(AuthContext);
  if (!context) throw new Error("useAuth must be used inside <AuthProvider>");
  return context;
}
