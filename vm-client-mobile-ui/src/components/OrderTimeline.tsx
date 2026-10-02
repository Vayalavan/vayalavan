/**
 * The three-milestone order timeline (CLAUDE.md §6.1).
 *
 * Exactly three steps, and no more: parcels go to third-party couriers and we
 * have no tracking, so a fourth step promising finer granularity than we can
 * observe would be a lie. The three notices underneath say so in the exact
 * words §6.1 specifies, and they come from the SERVER — the same strings the
 * web storefront and the confirmation email use, so nobody has to keep three
 * copies of the courier disclaimer in step.
 *
 * `completed` is likewise the server's, derived from timestamps at read time.
 * A phone clock must not be able to tick an order forward.
 */
import type { ReactElement } from "react";
import { View } from "react-native";

import { formatISTDateTime } from "../lib/datetime";
import type { TimelineMilestone } from "../lib/types";
import { colors, spacing } from "../theme/tokens";
import { Text } from "./Text";

const DOT = 20;

export function OrderTimeline({
  milestones,
  expectedDeliveryText,
  courierNotice,
  supportNotice,
}: {
  milestones: TimelineMilestone[];
  expectedDeliveryText: string;
  courierNotice: string;
  supportNotice: string;
}): ReactElement {
  return (
    <View style={{ gap: spacing.lg }}>
      <View>
        {milestones.map((milestone, index) => {
          const last = index === milestones.length - 1;
          return (
            <View key={milestone.name} style={{ flexDirection: "row", gap: spacing.md }}>
              {/* The rail: a dot per step, joined by a line that is coloured
                  only as far as the order has actually got. */}
              <View style={{ alignItems: "center", width: DOT }}>
                <View
                  style={{
                    width: DOT,
                    height: DOT,
                    borderRadius: DOT / 2,
                    borderWidth: 2,
                    borderColor: milestone.completed
                      ? colors.primary[600]
                      : colors.surface.border,
                    backgroundColor: milestone.completed
                      ? colors.primary[600]
                      : colors.surface.raised,
                    alignItems: "center",
                    justifyContent: "center",
                  }}
                >
                  {milestone.completed && (
                    <Text variant="caption" tone="onPrimary" style={{ fontWeight: "700" }}>
                      ✓
                    </Text>
                  )}
                </View>
                {!last && (
                  <View
                    style={{
                      flex: 1,
                      width: 2,
                      minHeight: spacing.xl,
                      backgroundColor: milestone.completed
                        ? colors.primary[600]
                        : colors.surface.border,
                    }}
                  />
                )}
              </View>

              <View style={{ flex: 1, paddingBottom: last ? 0 : spacing.lg }}>
                <Text
                  variant="bodyStrong"
                  tone={milestone.completed ? "strong" : "muted"}
                  // Read as one phrase, so a screen reader does not announce
                  // the date without saying whether the step has happened.
                  accessibilityLabel={`${milestone.name}: ${
                    milestone.completed ? "done" : "expected"
                  } ${formatISTDateTime(milestone.at)}`}
                >
                  {milestone.name}
                </Text>
                <Text variant="caption" tone="muted">
                  {formatISTDateTime(milestone.at)}
                </Text>
              </View>
            </View>
          );
        })}
      </View>

      <View style={{ gap: spacing.xs }}>
        <Text variant="bodyStrong" tone="strong">
          {expectedDeliveryText}
        </Text>
        <Text variant="caption" tone="muted">
          {courierNotice}
        </Text>
        {/* Selectable: this carries the support address, and a customer with a
            problem should be able to copy it rather than retype it. */}
        <Text variant="caption" tone="muted" selectable>
          {supportNotice}
        </Text>
      </View>
    </View>
  );
}
