/**
 * Typography primitives.
 *
 * React Native has no cascade: a <Text> inside a <View> inherits nothing, so
 * every string on screen needs its size, weight and colour stated. Doing that
 * inline is how a design drifts — this file is the equivalent of the web app's
 * `text-primary-900/70` utility classes, and screens should never reach for a
 * raw <Text> from react-native.
 */
import type { ReactElement } from "react";
import { Text as RNText, type TextProps, type TextStyle } from "react-native";

import { text as palette, typography } from "../theme/tokens";

type Variant =
  | "display"
  | "title"
  | "heading"
  | "body"
  | "bodyStrong"
  | "label"
  | "caption";

type Tone = "strong" | "body" | "muted" | "faint" | "onPrimary" | "danger";

export interface VayalTextProps extends TextProps {
  variant?: Variant;
  tone?: Tone;
  /**
   * Lining figures for anything in a column — money, weights, counts.
   *
   * Proportional digits make a total shift sideways as it updates, and make
   * two rupee amounts in a list fail to line up at the decimal point.
   */
  tabular?: boolean;
  center?: boolean;
}

const TONE_COLOR: Record<Tone, string> = {
  strong: palette.strong,
  body: palette.body,
  muted: palette.muted,
  faint: palette.faint,
  onPrimary: palette.onPrimary,
  danger: palette.danger,
};

export function Text({
  variant = "body",
  tone = "body",
  tabular = false,
  center = false,
  style,
  ...rest
}: VayalTextProps): ReactElement {
  const base = typography[variant];

  const composed: TextStyle = {
    fontSize: base.fontSize,
    fontWeight: base.fontWeight,
    color: TONE_COLOR[tone],
    ...(tabular && { fontVariant: ["tabular-nums"] }),
    ...(center && { textAlign: "center" }),
  };

  return <RNText {...rest} style={[composed, style]} />;
}
