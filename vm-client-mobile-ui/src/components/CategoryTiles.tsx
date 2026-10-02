/**
 * The category filter, as pictures.
 *
 * Same values and the same single piece of state the chips carried — what
 * changed is that produce is now chosen by eye, which is how it is chosen
 * everywhere else. A scrolling rail rather than a wrapped grid: five tiles at
 * a readable size do not fit across a phone, and a rail says "there is more
 * this way" in a way a cut-off third row does not.
 */
import type { ReactElement } from "react";
import { ImageBackground, Pressable, ScrollView, View } from "react-native";

import { Text } from "./Text";
import { colors, radius, spacing } from "../theme/tokens";

/** One picture per category, bundled by `make -C infra sync-brand`. */
const TILE: Record<string, number> = {
  "": require("../../assets/photos/cat-all.jpg") as number,
  fruit: require("../../assets/photos/cat-fruit.jpg") as number,
  vegetable: require("../../assets/photos/cat-vegetable.jpg") as number,
  microgreen: require("../../assets/photos/cat-microgreen.jpg") as number,
  other: require("../../assets/photos/cat-other.jpg") as number,
};

export interface CategoryTilesProps {
  categories: ReadonlyArray<readonly [string, string]>;
  value: string;
  onChange: (next: string) => void;
}

export function CategoryTiles({
  categories,
  value,
  onChange,
}: CategoryTilesProps): ReactElement {
  return (
    <ScrollView
      horizontal
      showsHorizontalScrollIndicator={false}
      accessibilityRole="tablist"
      accessibilityLabel="Filter by category"
      // Negative margin so the rail bleeds to the screen edge while the list
      // around it keeps its padding — a tile clipped by the edge is the cue
      // that the row scrolls.
      style={{ marginHorizontal: -spacing.lg }}
      contentContainerStyle={{ paddingHorizontal: spacing.lg, gap: spacing.sm }}
    >
      {categories.map(([key, label]) => {
        const selected = key === value;
        return (
          <Pressable
            key={key || "all"}
            onPress={() => onChange(key)}
            accessibilityRole="tab"
            accessibilityState={{ selected }}
            accessibilityLabel={label}
            style={{
              width: 116,
              height: 84,
              borderRadius: radius.card,
              overflow: "hidden",
              backgroundColor: colors.primary[900],
              borderWidth: 2,
              borderColor: selected ? colors.primary[600] : "transparent",
            }}
          >
            <ImageBackground
              source={TILE[key]}
              resizeMode="cover"
              style={{ flex: 1, justifyContent: "flex-end" }}
            >
              {/* A flat wash, darker when selected: the label has to stay
                  readable over whatever part of the photograph lands behind
                  it, and the choice has to read without relying on the border
                  alone — which a colourblind customer may not see. */}
              <View
                style={{
                  position: "absolute",
                  top: 0,
                  left: 0,
                  right: 0,
                  bottom: 0,
                  backgroundColor: colors.primary[950],
                  opacity: selected ? 0.62 : 0.45,
                }}
              />
              <View style={{ padding: spacing.sm }}>
                <Text variant="label" tone="onPrimary">
                  {label}
                </Text>
              </View>
            </ImageBackground>
          </Pressable>
        );
      })}
    </ScrollView>
  );
}
