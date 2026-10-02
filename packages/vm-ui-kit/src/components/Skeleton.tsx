import type { ReactElement } from "react";

/**
 * Loading placeholders.
 *
 * Shaped like the content they replace, so the page does not reflow when data
 * arrives — a spinner that becomes a three-column grid is a layout shift the
 * customer feels as jank.
 *
 * `motion-reduce:animate-none` respects a vestibular-disorder preference; a
 * pulsing block is exactly the kind of motion that setting exists for.
 */
export function Skeleton({ className = "" }: { className?: string }): ReactElement {
  return (
    <span
      aria-hidden="true"
      className={`block animate-pulse rounded-card bg-surface-sunken motion-reduce:animate-none ${className}`}
    />
  );
}

/** A product card placeholder, matching the real card's proportions. */
export function ProductCardSkeleton(): ReactElement {
  return (
    <div className="overflow-hidden rounded-card border border-surface-border bg-surface-raised">
      <Skeleton className="aspect-[4/3] rounded-none" />
      <div className="space-y-2 p-4">
        <Skeleton className="h-4 w-2/3" />
        <Skeleton className="h-3 w-1/3" />
        <div className="flex gap-1.5 pt-2">
          <Skeleton className="h-7 w-14" />
          <Skeleton className="h-7 w-14" />
        </div>
        <Skeleton className="h-9 w-full" />
      </div>
    </div>
  );
}

/**
 * A grid of card placeholders.
 *
 * The whole grid is one live region announced once, rather than each card
 * announcing itself — twelve "loading" messages is worse than none.
 */
export function ProductGridSkeleton({ count = 8 }: { count?: number }): ReactElement {
  return (
    <div
      role="status"
      aria-busy="true"
      aria-label="Loading today's produce"
      className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4"
    >
      {Array.from({ length: count }, (_, i) => (
        <ProductCardSkeleton key={i} />
      ))}
    </div>
  );
}
