/**
 * The reporting-period selector for the sales screen.
 *
 * The periods themselves live in `lib/salesRange.ts`, which holds no
 * react-native import so the rules can be tested under plain Node. They mirror
 * the web's RangePicker in @vayal/ui-kit, which this app cannot import (it is
 * outside the npm workspaces, and that component is React DOM), so `lib/salesRange.test.ts` asserts that list still
 * matches it. A supplier who checks their phone and then their laptop must not
 * be offered different weeks.
 *
 * A row of chips rather than a dropdown: it is one tap instead of two, and the
 * current period stays visible, which matters when the number on screen only
 * means something alongside the period it covers.
 *
 * Custom dates are typed rather than picked from a calendar. A native date
 * picker means a new dependency for two fields on one screen, and every other
 * date this app shows is already IST-formatted text. The inputs validate as
 * you type and the server validates again.
 */
import { useState, type ReactElement } from "react";
import { Pressable, ScrollView, View } from "react-native";

import { istTodayISO } from "../lib/datetime";
import { isISODate, RANGES } from "../lib/salesRange";
import { colors, radius, spacing } from "../theme/tokens";
import { TextField } from "./Field";
import { Text } from "./Text";

export interface RangeFilterProps {
  range: string;
  from: string;
  to: string;
  onRange: (value: string) => void;
  onFrom: (value: string) => void;
  onTo: (value: string) => void;
  /**
   * Which periods to offer. Defaults to the sales screen's list.
   *
   * A screen must pass the list its ENDPOINT accepts: the analytics endpoint
   * refuses all-time, and a chip that produces a 400 is worse than no chip.
   */
  options?: ReadonlyArray<readonly [key: string, label: string]>;
}

export function RangeFilter({
  range,
  from,
  to,
  onRange,
  onFrom,
  onTo,
  options = RANGES,
}: RangeFilterProps): ReactElement {
  // Only complain about a date once it has been typed in full — a message
  // under a field that is still being filled in is noise.
  const [fromTouched, setFromTouched] = useState(false);
  const [toTouched, setToTouched] = useState(false);

  const today = istTodayISO();
  const fromBad = fromTouched && from !== "" && !isISODate(from);
  const toBad = toTouched && to !== "" && !isISODate(to);
  const backwards =
    isISODate(from) && isISODate(to) && from > to
      ? "The start date must be on or before the end date."
      : undefined;

  return (
    <View style={{ gap: spacing.sm }}>
      <ScrollView
        horizontal
        showsHorizontalScrollIndicator={false}
        contentContainerStyle={{ gap: spacing.xs, paddingRight: spacing.md }}
        accessibilityRole="radiogroup"
        accessibilityLabel="Sales period"
      >
        {options.map(([key, label]) => {
          const selected = key === range;
          return (
            <Pressable
              key={key}
              onPress={() => onRange(key)}
              accessibilityRole="radio"
              accessibilityState={{ selected }}
              accessibilityLabel={label}
              style={({ pressed }) => ({
                paddingHorizontal: spacing.md,
                paddingVertical: spacing.xs,
                borderRadius: radius.pill,
                borderWidth: 1,
                borderColor: selected ? colors.primary[600] : colors.surface.border,
                backgroundColor: selected ? colors.primary[600] : colors.surface.raised,
                opacity: pressed ? 0.85 : 1,
              })}
            >
              <Text variant="label" tone={selected ? "onPrimary" : "body"}>
                {label}
              </Text>
            </Pressable>
          );
        })}
      </ScrollView>

      {range === "custom" && (
        <View style={{ gap: spacing.sm }}>
          <TextField
            label="From"
            value={from}
            onChangeText={(value) => {
              setFromTouched(true);
              onFrom(value);
            }}
            placeholder={today}
            hint="Dates are YYYY-MM-DD, in Indian time."
            keyboardType="numbers-and-punctuation"
            autoCapitalize="none"
            {...(fromBad && { error: "Use YYYY-MM-DD, for example " + today + "." })}
          />
          <TextField
            label="To"
            value={to}
            onChangeText={(value) => {
              setToTouched(true);
              onTo(value);
            }}
            placeholder={today}
            keyboardType="numbers-and-punctuation"
            autoCapitalize="none"
            {...((toBad || backwards !== undefined) && {
              error: backwards ?? "Use YYYY-MM-DD, for example " + today + ".",
            })}
          />
        </View>
      )}
    </View>
  );
}
