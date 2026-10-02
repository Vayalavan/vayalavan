/**
 * TanStack Query, configured for a device that loses signal and gets
 * backgrounded.
 *
 * The web UIs get two behaviours from the browser for free that React Native
 * does not provide, and without them Query behaves badly in exactly the
 * conditions people actually shop in — on a train, in a lift, on 3G:
 *
 *   - Online/offline. In a browser Query listens to `window.online`. Here it
 *     assumes permanently online, so a request made in a dead spot fails,
 *     retries twice against nothing, and reports an error the customer has to
 *     dismiss — instead of pausing until the signal returns.
 *   - Focus. `refetchOnWindowFocus` is what keeps the catalogue honest after
 *     you look away: stock moves as other people buy, and a card that says
 *     "add to cart" on produce that sold out ten minutes ago is a promise we
 *     cannot keep. There is no window; the equivalent signal is AppState
 *     going back to `active`.
 *
 * Both are wired here, once, at module scope.
 */
import NetInfo from "@react-native-community/netinfo";
import { QueryClient, focusManager, onlineManager } from "@tanstack/react-query";
import { AppState, Platform, type AppStateStatus } from "react-native";

/**
 * "Online" means reachable, not merely connected.
 *
 * `isInternetReachable` is null while the probe is still running. Treating
 * that as offline would pause every query for the first second of every cold
 * start, so an unknown state counts as connected and lets the request try.
 */
onlineManager.setEventListener((setOnline) =>
  NetInfo.addEventListener((state) => {
    setOnline(Boolean(state.isConnected) && state.isInternetReachable !== false);
  }),
);

function onAppStateChange(status: AppStateStatus): void {
  // Android reports "background" for states iOS calls "inactive"; only the
  // transition to fully active should trigger a refetch.
  if (Platform.OS !== "web") {
    focusManager.setFocused(status === "active");
  }
}

AppState.addEventListener("change", onAppStateChange);

export const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      // Shorter than the supplier app's 30s, matching vm-client-ui: this is a
      // shop, and the number that goes stale is whether something is still in
      // stock.
      staleTime: 20_000,
      // Cached data outlives a short trip to another app, so coming back shows
      // the catalogue immediately and revalidates behind it rather than
      // flashing a spinner at someone who was gone for ten seconds.
      gcTime: 10 * 60_000,
      refetchOnReconnect: true,
      refetchOnWindowFocus: true,
      retry: (failureCount, error) => {
        const status = (error as { status?: number }).status ?? 0;
        // None of these resolve themselves by asking again.
        if (status === 401 || status === 403 || status === 404) return false;
        // Nor does a 429 — retrying "too many requests" sends more requests,
        // turning one rate-limited call into three and extending the block.
        // The cart's quantity steppers are what provoke it.
        if (status === 429) return false;
        // Nor does a request the screen itself cancelled.
        if ((error as { code?: string }).code === "REQUEST_CANCELLED") return false;
        return failureCount < 2;
      },
      // Longer than the web's default: a retry on a phone is often waiting for
      // a tower to come back, not for a server to recover.
      retryDelay: (attempt) => Math.min(1_000 * 2 ** attempt, 8_000),
    },
    mutations: {
      // Writes are never retried automatically. Order placement is the reason:
      // it moves money and stock, and although it carries an Idempotency-Key
      // (CLAUDE.md rule 6), a silent second attempt after an ambiguous failure
      // is exactly the situation where a customer should be told what happened
      // rather than have the app decide for them.
      retry: false,
    },
  },
});
