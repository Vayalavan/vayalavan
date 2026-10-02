/**
 * Pull-to-refresh state, owned by the gesture rather than by the query.
 *
 * The obvious wiring — `refreshing={query.isFetching}` — is wrong, and wrong in
 * a way that only shows up on a device. `isFetching` is true for EVERY fetch,
 * including the silent background revalidation TanStack Query fires whenever a
 * screen mounts with data older than `staleTime`. Switching tabs therefore made
 * the spinner appear at the top of the content with nobody having pulled
 * anything, and `RefreshControl` — which is built to REFLECT a user gesture,
 * not to be driven programmatically — then left the indicator on screen until
 * the next interaction re-rendered it away.
 *
 * The cart showed it worst: adding an item invalidates the cart query, so the
 * data is guaranteed stale by the time the Cart tab is tapped, and the spinner
 * was there every single time.
 *
 * Background refetches must be silent. The content is already on screen and
 * already correct enough to read; that is the entire point of
 * stale-while-revalidate. Only a deliberate pull deserves an indicator, so only
 * a deliberate pull sets this flag.
 *
 * It also fixes a smaller bug at every call site: `useCallback(() => query.refetch(),
 * [query])` re-created the handler on every render, because a query RESULT is a
 * new object each time. `refetch` itself is stable, so passing that keeps the
 * RefreshControl's props stable too.
 */
import { useCallback, useEffect, useRef, useState } from "react";

export interface PullToRefresh {
  /** True only while a user-initiated refresh is in flight. */
  refreshing: boolean;
  /** Pass straight to `<Screen onRefresh>` or `<RefreshControl onRefresh>`. */
  onRefresh: () => void;
}

/**
 * @param refetch A query's `refetch`. Stable across renders — pass that, not
 *                the query result.
 */
export function usePullToRefresh(refetch: () => Promise<unknown>): PullToRefresh {
  const [refreshing, setRefreshing] = useState(false);

  // Navigating away mid-refresh must not set state on a screen that is gone.
  // Re-armed on mount rather than only cleared on unmount, so StrictMode's
  // double-mount in development does not leave it permanently false.
  const mounted = useRef(true);
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);

  const onRefresh = useCallback(() => {
    setRefreshing(true);
    void refetch().finally(() => {
      if (mounted.current) setRefreshing(false);
    });
  }, [refetch]);

  return { refreshing, onRefresh };
}
