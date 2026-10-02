/**
 * The surface everything sits on — the native counterpart of vm-ui-kit's Card.
 */
import type { ReactElement, ReactNode } from "react";
import { View, type ViewStyle } from "react-native";

import { colors, radius, shadow, spacing } from "../theme/tokens";

export interface CardProps {
  children: ReactNode;
  /** Drops the internal padding, for a card that holds its own list rows. */
  flush?: boolean;
  /** Draws attention: a re-pricing notice, or something needing a decision. */
  tone?: "default" | "accent" | "primary";
  style?: ViewStyle | ViewStyle[];
}

const TONE: Record<NonNullable<CardProps["tone"]>, ViewStyle> = {
  default: {
    backgroundColor: colors.surface.raised,
    // A warm edge on a cream page; the neutral border read as grey.
    borderColor: colors.cream[300],
  },
  accent: {
    backgroundColor: colors.accent[50],
    borderColor: colors.accent[200],
  },
  primary: {
    backgroundColor: colors.primary[50],
    borderColor: colors.primary[200],
  },
};

export function Card({
  children,
  flush = false,
  tone = "default",
  style,
}: CardProps): ReactElement {
  return (
    <View
      style={[
        {
          borderRadius: radius.card,
          borderWidth: 1,
          padding: flush ? 0 : spacing.lg,
          // Clipped only when the card holds edge-to-edge rows that would
          // otherwise square off its corners. Android draws the elevation
          // shadow outside the view, and `overflow: hidden` cuts it away —
          // so it is not set unconditionally.
          ...(flush && { overflow: "hidden" as const }),
          ...TONE[tone],
          ...shadow.card,
        },
        style as ViewStyle,
      ]}
    >
      {children}
    </View>
  );
}
