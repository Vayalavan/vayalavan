/**
 * The 4pm cutoff countdown, above the catalogue.
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
 *
 * One difference from the web banner, and it is the reason this is not a
 * straight port: a backgrounded phone stops running timers. Coming back to a
 * frozen countdown that resumes from where it stopped would be a LIE about
 * time remaining, so the interval is torn down on blur and the server is
 * re-asked on focus.
 */
import { useEffect, useRef, useState, type ReactElement } from "react";
import { AppState, View, type AppStateStatus } from "react-native";
import { useQuery } from "@tanstack/react-query";

import type { ApiError } from "../lib/api";
import { api } from "../lib/client";
import { announceRemaining, formatRemaining } from "../lib/countdown";
import type { Cutoff } from "../lib/types";
import { colors, radius, spacing } from "../theme/tokens";
import { Text } from "./Text";

/** How often we re-ask the server for the truth. */
const REFRESH_MS = 60_000;

export function CutoffBanner(): ReactElement | null {
  const cutoff = useQuery<Cutoff, ApiError>({
    queryKey: ["cutoff"],
    queryFn: () => api.get<Cutoff>("/cutoff"),
    refetchInterval: REFRESH_MS,
    refetchOnWindowFocus: true,
    // A stale countdown is worse than none, so never serve from cache.
    staleTime: 0,
    // The banner is decoration for a shop that works without it. A failed
    // fetch renders nothing rather than an error the customer cannot act on.
    retry: 1,
  });

  // Seconds remaining, seeded from the server and ticked down locally.
  const [remaining, setRemaining] = useState<number | null>(null);
  const [active, setActive] = useState(AppState.currentState === "active");
  const seededAt = useRef(0);

  useEffect(() => {
    const subscription = AppState.addEventListener("change", (status: AppStateStatus) =>
      setActive(status === "active"),
    );
    return () => subscription.remove();
  }, []);

  useEffect(() => {
    if (!cutoff.data) return;

    const seeded = cutoff.data.seconds_until_cutoff;
    seededAt.current = Date.now();
    setRemaining(seeded);

    // No timer while backgrounded: it would not fire anyway, and the value it
    // left behind is what the re-focus refetch is for.
    if (!active) return;

    // Measured with elapsed wall time from when the answer arrived, so a
    // device whose clock jumps does not corrupt the count.
    const timer = setInterval(() => {
      const elapsed = Math.floor((Date.now() - seededAt.current) / 1000);
      setRemaining(Math.max(0, seeded - elapsed));
    }, 1000);

    return () => clearInterval(timer);
  }, [cutoff.data, active]);

  // Say nothing rather than guess while the first answer is in flight.
  if (!cutoff.data || remaining === null) return null;

  const beforeCutoff = cutoff.data.before_cutoff && remaining > 0;

  return (
    <View
      style={{
        backgroundColor: beforeCutoff ? colors.primary[50] : colors.secondary[50],
        borderColor: beforeCutoff ? colors.primary[200] : colors.secondary[200],
        borderWidth: 1,
        borderRadius: radius.card,
        paddingHorizontal: spacing.md,
        paddingVertical: spacing.sm,
        gap: 2,
      }}
    >
      {beforeCutoff ? (
        <>
          {/* Deliberately hidden from screen readers: this line redraws every
              second, and announcing it would drown out the rest of the screen.
              The coarse version below carries the same information at a
              humane rate. */}
          <Text
            tone="strong"
            accessibilityElementsHidden
            importantForAccessibility="no-hide-descendants"
          >
            {"⏱  Order within "}
            <Text variant="bodyStrong" tone="strong" tabular>
              {formatRemaining(remaining)}
            </Text>
            {" for same day processing."}
          </Text>

          {/* Rounded to whole minutes, so it changes 60× less often than the
              visible clock and stays quiet for most of the countdown. */}
          <Text
            accessibilityLiveRegion="polite"
            accessibilityLabel={`${announceRemaining(remaining)} to order for same day processing.`}
            // Zero-height: the sighted line above already says it.
            style={{ height: 0 }}
          >
            {""}
          </Text>
        </>
      ) : (
        <Text tone="strong">
          {"🌙  Today’s cutoff has passed. Orders placed now are processed tomorrow."}
        </Text>
      )}

      <Text variant="caption" tone="muted">
        {cutoff.data.expected_delivery_text}
      </Text>
    </View>
  );
}
