/**
 * The shop front, on a phone.
 *
 * The same photograph and the same three facts as the web hero, at a height
 * that still leaves produce on screen: a customer opening the app wants the
 * grid, and a billboard they have to scroll past is a tax on every visit.
 *
 * No gradient library. React Native has no CSS gradients, and adding a native
 * module to fade a picture would mean a new dependency, a new EAS build and an
 * SDK pin to keep straight (CLAUDE.md §3) — so the scrim is a flat wash plus a
 * darker plate under the words. On a 390pt-wide image the difference from a
 * true gradient is not visible; the difference in what can go wrong is.
 */
import type { ReactElement } from "react";
import { ImageBackground, View } from "react-native";

import { Text } from "./Text";
import { colors, radius, spacing } from "../theme/tokens";

const HERO = require("../../assets/photos/hero.jpg") as number;

const FACTS: ReadonlyArray<readonly [string, string]> = [
  ["Same-day", "listed by the grower"],
  ["4 pm", "order cutoff"],
  ["Named", "farm on every item"],
];

export function HomeHero({ dateLine }: { dateLine: string }): ReactElement {
  return (
    <ImageBackground
      source={HERO}
      resizeMode="cover"
      // The image is decoration: everything it says is said in the text on
      // top of it, so a screen reader should walk straight past it.
      accessible={false}
      style={{
        borderRadius: radius.panel,
        overflow: "hidden",
        backgroundColor: colors.primary[900],
      }}
    >
      <View
        style={{
          backgroundColor: colors.primary[950],
          opacity: 0.42,
          ...StyleSheetAbsoluteFill,
        }}
      />
      <View style={{ padding: spacing.lg, paddingTop: spacing.xl, gap: spacing.sm }}>
        <Text variant="caption" tone="onPrimary" style={{ letterSpacing: 2, opacity: 0.9 }}>
          VAYAL MIKROGREENZ
        </Text>
        <Text variant="display" tone="onPrimary" accessibilityRole="header">
          Picked this morning.
        </Text>
        <Text variant="display" style={{ color: colors.accent[300] }}>
          At your doorstep soon.
        </Text>
        <Text variant="caption" tone="onPrimary" style={{ opacity: 0.85 }}>
          Fruit, vegetables and microgreens from growers who list what they cut
          that day.{dateLine}
        </Text>

        <View
          style={{
            flexDirection: "row",
            flexWrap: "wrap",
            gap: spacing.lg,
            marginTop: spacing.sm,
          }}
        >
          {FACTS.map(([value, label]) => (
            <View key={label}>
              <Text variant="heading" tone="onPrimary">
                {value}
              </Text>
              <Text variant="caption" tone="onPrimary" style={{ opacity: 0.7 }}>
                {label}
              </Text>
            </View>
          ))}
        </View>
      </View>
    </ImageBackground>
  );
}

/**
 * `StyleSheet.absoluteFillObject`, inlined.
 *
 * Spelt out rather than imported so the scrim's four zeroes sit next to the
 * opacity they belong with, which is the only thing about this view worth
 * reading.
 */
const StyleSheetAbsoluteFill = {
  position: "absolute",
  top: 0,
  left: 0,
  right: 0,
  bottom: 0,
} as const;
