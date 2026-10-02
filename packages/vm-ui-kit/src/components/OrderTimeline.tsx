import type { ReactElement } from "react";

/**
 * One step of the order timeline.
 *
 * `completed` is computed by the SERVER from timestamps at read time
 * (CLAUDE.md §6.1) — never stored, and never derived from the device clock.
 */
export interface TimelineMilestone {
  name: string;
  /**
   * Raw from the API: RFC3339 for the two timestamps, "YYYY-MM-DD" for the
   * delivery day. Formatted here rather than by the caller — see
   * `formatMilestoneAt` for why that has to be one shared place.
   */
  at: string;
  completed: boolean;
}

const DATE_ONLY = /^\d{4}-\d{2}-\d{2}$/;

function formatIST(value: Date, withTime: boolean): string {
  return new Intl.DateTimeFormat("en-IN", {
    day: "numeric",
    month: "short",
    year: "numeric",
    ...(withTime ? { hour: "numeric", minute: "2-digit", hour12: true } : {}),
    // Pinned, never the device timezone. CLAUDE.md rule 2: business days are
    // Asia/Kolkata. A customer travelling abroad must see the same delivery
    // date as the courier does, not one shifted by their phone's clock.
    timeZone: "Asia/Kolkata",
  }).format(value);
}

/**
 * Renders one milestone's timestamp for humans.
 *
 * Exported for tests. The component formats rather than trusting the caller
 * because the previous contract ("already formatted by the caller") meant no
 * one formatted at all, and the UI showed customers a raw
 * "2026-08-15T03:28:59+05:30". Three UIs would also have been three chances to
 * format in local time and quietly shift the date across midnight.
 *
 * Handles both shapes the API sends: full timestamps for "received" and
 * "processed", a bare date for the delivery day — which has no meaningful
 * time-of-day, so none is invented.
 */
export function formatMilestoneAt(value: string): string {
  if (DATE_ONLY.test(value)) {
    // Anchored to IST explicitly. Bare "2026-08-16" would parse as UTC
    // midnight, which is the 15th in any timezone behind UTC.
    const dateOnly = new Date(`${value}T00:00:00+05:30`);
    return Number.isNaN(dateOnly.getTime()) ? value : formatIST(dateOnly, false);
  }
  const parsed = new Date(value);
  // Show the server's own string rather than "Invalid Date" if the shape
  // ever changes — wrong-looking is recoverable, "Invalid Date" is alarming.
  return Number.isNaN(parsed.getTime()) ? value : formatIST(parsed, true);
}

export interface OrderTimelineProps {
  /**
   * Terminal orders (cancelled, refunded, expired, payment_failed) carry no
   * milestones at all — CLAUDE.md §6.1. Accepts null/undefined as well as `[]`
   * because an older API build, or any serialiser that renders an empty Go
   * slice as `null`, will send one.
   */
  milestones: TimelineMilestone[] | null | undefined;
  /** e.g. "Expected delivery: 17 Jun 2026" */
  expectedDeliveryText: string;
  /** The no-live-tracking note, verbatim from the server. */
  courierNotice: string;
  /** "Any issues? Write to us at …" */
  supportNotice: string;
  className?: string;
}

/**
 * The three-step order tracker.
 *
 * Horizontal on desktop, vertical on mobile — the same markup in both, with
 * the axis flipped by CSS. Two separate trees would be two places for the
 * copy to drift, and the copy here is fixed by CLAUDE.md §6.1.
 *
 * Exactly three steps, by design. There is deliberately no "out for delivery":
 * parcels go to third-party couriers with no tracking feed, so promising finer
 * granularity than we can observe would be a lie the customer discovers on the
 * day.
 */
export function OrderTimeline({
  milestones,
  expectedDeliveryText,
  courierNotice,
  supportNotice,
  className = "",
}: OrderTimelineProps): ReactElement | null {
  // No milestones means there is nothing to promise: hide the block entirely
  // rather than render an empty tracker (CLAUDE.md §6.1).
  if (!milestones || milestones.length === 0) return null;

  return (
    <div className={className}>
      {/* An ordered list is the honest semantic: sequential steps, and a
          screen reader announces "1 of 3". */}
      <ol className="flex flex-col gap-0 sm:flex-row sm:gap-0">
        {milestones.map((milestone, index) => {
          const isLast = index === milestones.length - 1;
          // A segment is filled only once the step it LEADS TO is reached.
          // Keying it off the current step drew a solid green line into an
          // empty circle, which reads as "processing is under way" when in
          // fact nothing has happened since the order was placed.
          const reachedNext = milestones[index + 1]?.completed ?? false;
          return (
            <li
              key={milestone.name}
              className="relative flex flex-1 gap-3 pb-6 sm:flex-col sm:gap-0 sm:pb-0"
            >
              {/* Connector. Vertical on mobile (down the left gutter),
                  horizontal on desktop (across the row). */}
              {!isLast && (
                <>
                  <span
                    aria-hidden="true"
                    className={`absolute left-[11px] top-6 h-full w-0.5 sm:hidden ${
                      reachedNext ? "bg-primary-500" : "bg-surface-border"
                    }`}
                  />
                  {/* left-3 / top-3 is the circle's centre: the dot is h-6 w-6
                      and sits at the column's leading edge, so half of it is
                      12px in and 12px down. `left-1/2` here started the line
                      at the middle of the whole column, leaving a visible gap
                      between the tick and the line it should touch. Each item
                      is flex-1, so w-full spans exactly centre-to-centre. */}
                  <span
                    aria-hidden="true"
                    className={`absolute left-3 top-3 hidden h-0.5 w-full sm:block ${
                      reachedNext ? "bg-primary-500" : "bg-surface-border"
                    }`}
                  />
                </>
              )}

              <span
                aria-hidden="true"
                className={`relative z-10 flex h-6 w-6 shrink-0 items-center justify-center rounded-full border-2 sm:mb-3 ${
                  milestone.completed
                    ? "border-primary-600 bg-primary-600 text-white"
                    : "border-surface-border bg-surface-raised text-transparent"
                }`}
              >
                {/* A tick, drawn rather than a font glyph so it renders
                    identically everywhere. */}
                <svg viewBox="0 0 12 12" className="h-3 w-3" fill="none">
                  <path
                    d="M2.5 6.5 5 9l4.5-5"
                    stroke="currentColor"
                    strokeWidth="2"
                    strokeLinecap="round"
                    strokeLinejoin="round"
                  />
                </svg>
              </span>

              <div className="min-w-0 sm:pr-4">
                <p
                  className={`text-sm font-medium ${
                    milestone.completed ? "text-primary-900" : "text-primary-900/50"
                  }`}
                >
                  {milestone.name}
                </p>
                <p className="mt-0.5 text-xs text-primary-900/60">
                  {formatMilestoneAt(milestone.at)}
                </p>
                {/* Status in text, not colour alone — the tick is aria-hidden,
                    so this is what a screen reader actually announces. */}
                <span className="sr-only">
                  {milestone.completed ? "Completed" : "Not yet completed"}
                </span>
              </div>
            </li>
          );
        })}
      </ol>

      <div className="mt-4 rounded-card bg-surface-sunken p-4">
        <p className="font-medium text-primary-900">{expectedDeliveryText}</p>
        <p className="mt-2 text-sm text-primary-900/70">{courierNotice}</p>
        <p className="mt-2 text-sm text-primary-900/70">{supportNotice}</p>
      </div>
    </div>
  );
}
