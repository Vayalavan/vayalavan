/**
 * Authentication state for the storefront.
 *
 * Access token: memory only. A token in localStorage is readable by any XSS
 * payload on the page.
 *
 * Refresh token: localStorage, so a page reload does not sign the supplier
 * out. This is a deliberate, documented trade — the alternative is losing the
 * session on every refresh, which for a portal people keep open all day is
 * unusable. Rotation plus reuse detection on the server bounds the damage: a
 * stolen refresh token is single-use, and using it invalidates every session
 * for that account.
 */
import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactElement,
  type ReactNode,
} from "react";
import { ApiError } from "@vayal/ui-kit";
import { api, setAccessToken } from "./api.js";

const REFRESH_TOKEN_KEY = "vayal.customer.refresh";

export interface CustomerUser {
  id: string;
  email: string | null;
  phone: string | null;
  role: string;
  status: string;
  name?: string | null;
}

interface Tokens {
  access_token: string;
  refresh_token: string;
}

interface AuthResponse {
  user: CustomerUser;
  tokens: Tokens;
}

interface AuthState {
  user: CustomerUser | null;
  /** True until the stored refresh token has been tried, so routes can wait. */
  initialising: boolean;
  signIn: (identifier: string, password: string) => Promise<void>;
  signOut: () => Promise<void>;
}

/**
 * The in-flight bootstrap refresh, shared across every mount of the provider.
 *
 * Module scope rather than a ref: StrictMode's second mount gets a fresh ref,
 * which is exactly the case this has to survive.
 */
let bootstrapInFlight: Promise<AuthResponse> | null = null;

const AuthContext = createContext<AuthState | null>(null);

export function AuthProvider({ children }: { children: ReactNode }): ReactElement {
  const [user, setUser] = useState<CustomerUser | null>(null);
  const [initialising, setInitialising] = useState(true);

  const applySession = useCallback((response: AuthResponse) => {
    setAccessToken(response.tokens.access_token);
    localStorage.setItem(REFRESH_TOKEN_KEY, response.tokens.refresh_token);
    setUser(response.user);
  }, []);

  const clearSession = useCallback(() => {
    setAccessToken(null);
    localStorage.removeItem(REFRESH_TOKEN_KEY);
    setUser(null);
  }, []);

  // On mount, trade the stored refresh token for a live session.
  //
  // Guarded by a module-level in-flight promise so this runs ONCE per page
  // load however many times the effect fires. React StrictMode mounts every
  // effect twice in development, which sent the same refresh token twice: the
  // first call rotated it, the second presented an already-rotated token, and
  // the server's reuse detection correctly concluded theft and revoked every
  // session. The symptom was being thrown back to the sign-in screen on
  // reload, caused entirely by the client asking twice.
  //
  // The `cancelled` flag alone was not enough — it suppressed the second
  // response but the second REQUEST had already been sent.
  useEffect(() => {
    const stored = localStorage.getItem(REFRESH_TOKEN_KEY);
    if (!stored) {
      setInitialising(false);
      return;
    }

    let cancelled = false;

    if (!bootstrapInFlight) {
      bootstrapInFlight = api
        .post<AuthResponse>("/auth/refresh", { refresh_token: stored })
        .finally(() => {
          // Cleared once settled so a later sign-out and sign-in can bootstrap
          // again; this dedupes concurrent calls, it does not cache a session.
          bootstrapInFlight = null;
        });
    }

    bootstrapInFlight
      .then((response) => {
        if (!cancelled) applySession(response);
      })
      .catch(() => {
        // Expired, revoked, or invalidated by reuse detection. Either way the
        // user signs in again.
        if (!cancelled) clearSession();
      })
      .finally(() => {
        if (!cancelled) setInitialising(false);
      });

    return () => {
      cancelled = true;
    };
  }, [applySession, clearSession]);

  const signIn = useCallback(
    async (identifier: string, password: string) => {
      const response = await api.post<AuthResponse>("/auth/login", {
        identifier,
        password,
      });
      if (response.user.role !== "customer") {
        // A customer or admin account is valid, just not for this portal.
        throw new ApiError(403, "WRONG_PORTAL", "This account is not a customer account.");
      }
      applySession(response);
    },
    [applySession],
  );

  const signOut = useCallback(async () => {
    const stored = localStorage.getItem(REFRESH_TOKEN_KEY);
    // Clear locally first: the supplier is signed out from their point of view
    // whether or not the server call succeeds.
    clearSession();
    if (stored) {
      await api.post("/auth/logout", { refresh_token: stored }).catch(() => {
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
