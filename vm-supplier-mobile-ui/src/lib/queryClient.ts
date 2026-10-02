/**
 * TanStack Query, configured for a device that loses signal and gets
 * backgrounded.
 *
 * The web UIs get two behaviours from the browser for free that React Native
 * does not provide, and without them Query behaves badly in exactly the
 * conditions suppliers actually work in:
 *
 *   - Online/offline. In a browser Query listens to `window.online`. Here it
 *     assumes permanently online, so a request made in a dead spot fails,
 *     retries twice against nothing, and reports an error the supplier has to
 *     dismiss — instead of pausing until the signal returns.
 *   - Focus. `refetchOnWindowFocus` is what makes the availability sheet
 *     correct after you look away. There is no window; the equivalent signal
 *     is AppState going back to `active`.
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
      staleTime: 30_000,
      // Cached data outlives a short trip to another app, so coming back shows
      // the sheet immediately and revalidates behind it rather than flashing a
      // spinner at someone who was gone for ten seconds.
      gcTime: 10 * 60_000,
      refetchOnReconnect: true,
      refetchOnWindowFocus: true,
      retry: (failureCount, error) => {
        const status = (error as { status?: number }).status ?? 0;
        // None of these resolve themselves by asking again.
        if (status === 401 || status === 403 || status === 404) return false;
        // Nor does a request the screen itself cancelled.
        if ((error as { code?: string }).code === "REQUEST_CANCELLED") return false;
        return failureCount < 2;
      },
      // Longer than the web's default: a retry on a phone is often waiting for
      // a tower to come back, not for a server to recover.
      retryDelay: (attempt) => Math.min(1_000 * 2 ** attempt, 8_000),
    },
    mutations: {
      // Writes are never retried automatically. Not every supplier write is
      // idempotent — "copy yesterday" and the CSV commit are not — and a
      // silent second attempt after an ambiguous failure is how a supplier
      // ends up with the catalogue imported twice.
      retry: false,
    },
  },
});
