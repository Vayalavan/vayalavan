import { useEffect, useState, type ReactElement } from "react";
import { useQuery } from "@tanstack/react-query";
import { ApiError } from "@vayal/ui-kit";
import { api } from "../lib/api.js";
import { formatRemaining, announceRemaining } from "../lib/countdown.js";

interface CutoffResponse {
  /** Server time in IST, the reference point for the countdown. */
  server_now: string;
  cutoff_at: string;
  cutoff_hour: number;
  before_cutoff: boolean;
  /** Authoritative remaining seconds, computed server-side. */
  seconds_until_cutoff: number;
  expected_delivery_text: string;
}

/** How often we re-ask the server for the truth. */
const REFRESH_MS = 60_000;

/**
 * The persistent cutoff banner.
 *
 * The DECISION — are we before the cutoff, and how long is left — is always
 * the server's (CLAUDE.md rule 2). A device clock can be wrong by hours, and a
 * customer told they have twenty minutes when the cutoff has passed gets a
 * delivery date we cannot honour.
 *
 * The local timer only animates BETWEEN server answers: it counts down from
 * the server's `seconds_until_cutoff` using elapsed time, and re-syncs every
 * minute. So a skewed clock makes the seconds tick at the wrong rate for at
 * most a minute; it can never change which side of the cutoff we are on.
 */
export function CutoffBanner(): ReactElement | null {
  const cutoff = useQuery<CutoffResponse, ApiError>({
    queryKey: ["cutoff"],
    queryFn: () => api.get<CutoffResponse>("/cutoff"),
    refetchInterval: REFRESH_MS,
    refetchOnWindowFocus: true,
    // A stale countdown is worse than none, so never serve from cache.
    staleTime: 0,
  });

  // Seconds remaining, seeded from the server and ticked down locally.
  const [remaining, setRemaining] = useState<number | null>(null);

  useEffect(() => {
    if (!cutoff.data) return;

    const seeded = cutoff.data.seconds_until_cutoff;
    setRemaining(seeded);

    // Measured with elapsed wall time from when the answer arrived, so a
    // device whose clock jumps does not corrupt the count.
    const startedAt = Date.now();
    const timer = setInterval(() => {
      const elapsed = Math.floor((Date.now() - startedAt) / 1000);
      setRemaining(Math.max(0, seeded - elapsed));
    }, 1000);

    return () => clearInterval(timer);
  }, [cutoff.data]);

  // Say nothing rather than guess while the first answer is in flight.
  if (!cutoff.data || remaining === null) return null;

  const beforeCutoff = cutoff.data.before_cutoff && remaining > 0;

  return (
    <div
      // Deliberately NOT a live region. The countdown redraws every second,
      // and a live region would make a screen reader read the banner aloud
      // once a second, drowning out the page. The coarse announcement below
      // carries the same information at a humane rate.
      // Deep green before the cutoff, deep earth after: the strip is the
      // one piece of the header that changes with the clock, and a dark band
      // under a cream header reads as information rather than decoration.
      className={`px-4 py-2.5 text-sm sm:px-6 ${
        beforeCutoff ? "bg-primary-900 text-white/85" : "bg-secondary-900 text-white/85"
      }`}
    >
      <div className="mx-auto flex max-w-7xl flex-col gap-1 sm:flex-row sm:items-center sm:justify-between">
        {beforeCutoff ? (
          <p aria-hidden="true">
            <ClockGlyph />
            Order within{" "}
            {/* tabular-nums keeps the digits from jittering as they change. */}
            <strong className="font-semibold tabular-nums text-gold-200">
              {formatRemaining(remaining)}
            </strong>{" "}
            for same day processing.
          </p>
        ) : (
          <p>
            <span aria-hidden="true">🌙 </span>
            Today&rsquo;s cutoff has passed. Orders placed now are processed
            tomorrow.
          </p>
        )}

        {/* What a screen reader actually hears. Rounded to whole minutes, so
            it changes 60x less often than the visible clock and stays quiet
            for most of the countdown. */}
        {beforeCutoff && (
          <p role="status" aria-live="polite" className="sr-only">
            {announceRemaining(remaining)} to order for same day processing.
          </p>
        )}

        <p className="text-white/60 sm:text-right">
          {cutoff.data.expected_delivery_text}
        </p>
      </div>
    </div>
  );
}

/** A small clock, drawn, so it sits on the text baseline on every platform. */
function ClockGlyph(): ReactElement {
  return (
    <svg
      aria-hidden="true"
      viewBox="0 0 20 20"
      fill="none"
      stroke="currentColor"
      strokeWidth={1.8}
      strokeLinecap="round"
      className="mr-1.5 inline h-4 w-4 -translate-y-px text-gold-200"
    >
      <circle cx="10" cy="10" r="7.25" />
      <path d="M10 6v4l2.5 1.5" />
    </svg>
  );
}
