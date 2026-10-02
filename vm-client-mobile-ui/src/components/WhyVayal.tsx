/**
 * Why choose Vayalavan.
 *
 * The same four reasons as the web, in the same words, for the same reason
 * they are those four: each is something the platform actually enforces and
 * each is checkable on the produce above it. A reason to buy that the product
 * does not back up is a slogan, and a customer finds out on the first order.
 *
 * Sits at the FOOT of the catalogue: someone who opened the app came to shop,
 * and someone still deciding scrolls to the end and reads.
 */
import type { ReactElement } from "react";
import { ImageBackground, Linking, Pressable, View } from "react-native";

import { Card } from "./Card";
import { Text } from "./Text";
import { config } from "../lib/config";
import { colors, radius, spacing } from "../theme/tokens";

const WHY = require("../../assets/photos/why.jpg") as number;

const REASONS: ReadonlyArray<readonly [string, string]> = [
  [
    "Cut this morning, not last week",
    "Growers declare what they have each day, and the shop only sells what was declared.",
  ],
  [
    "You know whose field it came from",
    "Meet the farm behind every harvest. Each listing tells you who grew it and keeps the grower’s price at the heart of every purchase.",
  ],
  [
    "The size you picked is the size you get",
    "Fruit is graded the way the grower packs it — M2, L1, XL2 — each with its weight range and its own price.",
  ],
  [
    "Order by 4 pm, know the day it lands",
    "Before the cutoff is processed the same day, and the delivery date is on the order from the moment you place it.",
  ],
];

export function WhyVayal(): ReactElement {
  return (
    <View style={{ marginTop: spacing.xl, gap: spacing.md }}>
      <ImageBackground
        source={WHY}
        resizeMode="cover"
        accessible={false}
        style={{
          height: 160,
          borderRadius: radius.panel,
          overflow: "hidden",
          justifyContent: "flex-end",
          backgroundColor: colors.primary[900],
        }}
      >
        <View
          style={{
            position: "absolute",
            top: 0,
            left: 0,
            right: 0,
            bottom: 0,
            backgroundColor: colors.primary[950],
            // Enough to seat white type without flattening the field: the
            // green of a flooded paddy is what the photograph is for, and a
            // heavier wash trades it away for contrast the text does not need.
            opacity: 0.45,
          }}
        />
        <View style={{ padding: spacing.lg }}>
          <Text variant="caption" tone="onPrimary" style={{ letterSpacing: 2, opacity: 0.85 }}>
            WHY CHOOSE US
          </Text>
          <Text variant="title" tone="onPrimary" accessibilityRole="header">
            Why choose Vayalavan?
          </Text>
          <Text variant="caption" tone="onPrimary" style={{ opacity: 0.9 }}>
            A vayalavan is the one who works the field — and every box here
            comes from one.
          </Text>
        </View>
      </ImageBackground>

      <Card>
        <View style={{ gap: spacing.lg }}>
          <Text variant="caption" tone="muted">
            Four things we hold ourselves to. Each one is visible on the produce
            above — not a promise you have to take on trust.
          </Text>

          {REASONS.map(([title, body], index) => (
            <View
              key={title}
              style={{
                gap: 2,
                ...(index > 0
                  ? {
                      borderTopWidth: 1,
                      borderTopColor: colors.surface.border,
                      paddingTop: spacing.lg,
                    }
                  : {}),
              }}
            >
              <Text variant="bodyStrong" tone="strong">
                {title}
              </Text>
              <Text variant="caption" tone="muted">
                {body}
              </Text>
            </View>
          ))}

          <Pressable
            onPress={() => void Linking.openURL(`mailto:${config.supportEmail}`)}
            accessibilityRole="link"
            accessibilityLabel={`Email us at ${config.supportEmail}`}
          >
            <Text variant="caption" tone="muted">
              Something not right with an order? Write to us at{" "}
              <Text variant="caption" tone="strong">
                {config.supportEmail}
              </Text>{" "}
              — a person reads it.
            </Text>
          </Pressable>
        </View>
      </Card>
    </View>
  );
}
