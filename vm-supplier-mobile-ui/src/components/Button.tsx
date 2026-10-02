/**
 * Buttons.
 *
 * Pressable rather than TouchableOpacity: it gives a real pressed state and
 * proper accessibility roles, and it is the API that is not on its way out.
 *
 * Every variant is at least HIT_SIZE tall. Suppliers use this outdoors,
 * one-handed, often with wet hands — CLAUDE.md's mobile-first rule means
 * something more here than a responsive breakpoint.
 */
import type { ReactElement, ReactNode } from "react";
import {
  ActivityIndicator,
  Pressable,
  View,
  type StyleProp,
  type ViewStyle,
} from "react-native";

import { HIT_SIZE, colors, radius, spacing } from "../theme/tokens";
import { Text } from "./Text";

export type ButtonVariant = "primary" | "secondary" | "ghost" | "danger";
export type ButtonSize = "md" | "sm";

export interface ButtonProps {
  label: string;
  onPress: () => void;
  variant?: ButtonVariant;
  size?: ButtonSize;
  disabled?: boolean;
  /** Shows a spinner and blocks presses. Implies `disabled`. */
  loading?: boolean;
  /** Stretches to fill its row. */
  block?: boolean;
  /** Rendered before the label — a small glyph, not an image. */
  leading?: ReactNode;
  accessibilityHint?: string;
  style?: StyleProp<ViewStyle>;
}

interface VariantStyle {
  background: string;
  border: string;
  tone: "strong" | "onPrimary" | "danger" | "body";
}

const VARIANTS: Record<ButtonVariant, VariantStyle> = {
  primary: {
    background: colors.primary[600],
    border: colors.primary[600],
    tone: "onPrimary",
  },
  secondary: {
    background: colors.surface.raised,
    border: colors.surface.border,
    tone: "strong",
  },
  ghost: {
    background: "transparent",
    border: "transparent",
    tone: "body",
  },
  danger: {
    background: colors.surface.raised,
    border: colors.accent[300],
    tone: "danger",
  },
};

export function Button({
  label,
  onPress,
  variant = "primary",
  size = "md",
  disabled = false,
  loading = false,
  block = false,
  leading,
  accessibilityHint,
  style,
}: ButtonProps): ReactElement {
  const palette = VARIANTS[variant];
  const inert = disabled || loading;

  return (
    <Pressable
      onPress={onPress}
      disabled={inert}
      accessibilityRole="button"
      accessibilityState={{ disabled: inert, busy: loading }}
      accessibilityLabel={label}
      {...(accessibilityHint !== undefined && { accessibilityHint })}
      style={({ pressed }) => [
        {
          minHeight: size === "md" ? HIT_SIZE : 38,
          paddingHorizontal: size === "md" ? spacing.xl : spacing.md,
          paddingVertical: size === "md" ? spacing.md : spacing.sm,
          borderRadius: radius.card,
          borderWidth: 1,
          borderColor: palette.border,
          backgroundColor: palette.background,
          alignItems: "center",
          justifyContent: "center",
          ...(block && { alignSelf: "stretch" }),
          // Opacity, not a second colour ramp: it reads as "not available
          // right now" on every variant without inventing four more tokens.
          opacity: inert ? 0.5 : pressed ? 0.85 : 1,
        },
        style as ViewStyle,
      ]}
    >
      <View style={{ flexDirection: "row", alignItems: "center", gap: spacing.sm }}>
        {loading ? (
          <ActivityIndicator
            size="small"
            color={palette.tone === "onPrimary" ? colors.surface.raised : colors.primary[600]}
          />
        ) : (
          leading
        )}
        <Text
          variant={size === "md" ? "bodyStrong" : "label"}
          tone={palette.tone}
          numberOfLines={1}
        >
          {label}
        </Text>
      </View>
    </Pressable>
  );
}
