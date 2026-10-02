/**
 * A produce photograph, with the two states every one of them has: no photo
 * yet, and sold out.
 *
 * `resizeMode="contain"`, not cover, for the same reason as the web cards:
 * cover fills the box neatly but crops, and on produce photography the crop
 * takes the part that identifies the item — an apple loses its stem and leaf
 * off the top edge. The letterboxing is invisible because the box is the
 * card's own white and product shots arrive on white.
 *
 * A URL that fails to load falls back to the placeholder. Product image URLs
 * are presigned and short-lived (one hour), so a card left on screen past the
 * expiry, a dead spot, or a storage endpoint the handset cannot reach all end
 * the same way: `<Image>` renders nothing at all and the customer gets a blank
 * grey box that looks like a broken app. The placeholder at least says "no
 * photo", which is the truth from where they are sitting.
 *
 * The sold-out treatment is NOT the web's `grayscale` filter. React Native has
 * no CSS filters, and the alternatives (a native image-filter library, or
 * asking the server for a desaturated variant) are a dependency and a round
 * trip for a visual nicety. Dimming the photo behind a scrim and captioning it
 * says the same thing, and reads better on a small screen than a grey
 * thumbnail that could just be a bad photo.
 */
import { useEffect, useState, type ReactElement } from "react";
import { Image, View } from "react-native";

import { colors, radius, spacing } from "../theme/tokens";
import { Text } from "./Text";

const PLACEHOLDER = require("../../assets/produce-placeholder.png") as number;

export interface ProduceImageProps {
  uri: string | null;
  /** For the alt text a screen reader announces. */
  name: string;
  height: number;
  soldOut?: boolean;
  /** The coarse "Only a few left" nudge. Never shown on a sold-out card. */
  stockHint?: string;
}

export function ProduceImage({
  uri,
  name,
  height,
  soldOut = false,
  stockHint = "",
}: ProduceImageProps): ReactElement {
  const [failed, setFailed] = useState(false);

  // A new URL deserves a fresh attempt — the catalogue refetches on focus and
  // mints new presigned URLs, which is exactly how an expired one recovers.
  useEffect(() => {
    setFailed(false);
  }, [uri]);

  const usePlaceholder = uri === null || uri === "" || failed;

  return (
    <View
      style={{
        height,
        backgroundColor: colors.surface.raised,
        overflow: "hidden",
      }}
    >
      <Image
        source={usePlaceholder ? PLACEHOLDER : { uri }}
        onError={() => setFailed(true)}
        resizeMode="contain"
        accessible
        accessibilityRole="image"
        accessibilityLabel={soldOut ? `${name}, sold out for today` : name}
        style={{
          width: "100%",
          height: "100%",
          padding: spacing.sm,
          // Dimmed rather than desaturated — see the note above.
          opacity: soldOut ? 0.45 : 1,
        }}
      />

      {soldOut && (
        <View
          style={{
            ...ABSOLUTE_FILL,
            alignItems: "center",
            justifyContent: "center",
          }}
          // The label below carries the same words to a screen reader through
          // the image's own accessibilityLabel, so this must not be announced
          // a second time.
          accessibilityElementsHidden
          importantForAccessibility="no-hide-descendants"
        >
          <View
            style={{
              backgroundColor: colors.primary[900],
              opacity: 0.9,
              borderRadius: radius.pill,
              paddingHorizontal: spacing.md,
              paddingVertical: spacing.xs,
            }}
          >
            <Text variant="label" tone="onPrimary">
              Sold out for today
            </Text>
          </View>
        </View>
      )}

      {!soldOut && stockHint !== "" && (
        <View
          style={{
            position: "absolute",
            top: spacing.sm,
            left: spacing.sm,
            backgroundColor: colors.accent[500],
            borderRadius: radius.pill,
            paddingHorizontal: spacing.sm,
            paddingVertical: 2,
          }}
        >
          <Text variant="caption" tone="onPrimary" style={{ fontWeight: "600" }}>
            {stockHint}
          </Text>
        </View>
      )}
    </View>
  );
}

const ABSOLUTE_FILL = {
  position: "absolute",
  top: 0,
  right: 0,
  bottom: 0,
  left: 0,
} as const;
