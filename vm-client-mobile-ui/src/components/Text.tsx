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
import { Platform, Text as RNText, type TextProps, type TextStyle } from "react-native";

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

/**
 * The serif for display text — the web's Fraunces, as near as a phone gets
 * without bundling a font: Georgia ships on every iPhone, and "serif" is Noto
 * Serif on Android. A heading in a serif is the cheapest single signal of care
 * a shop can send; everything smaller stays in the system sans.
 */
const DISPLAY_FAMILY = Platform.select({ ios: "Georgia", android: "serif", default: undefined });

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
    ...(variant === "display" && DISPLAY_FAMILY !== undefined && { fontFamily: DISPLAY_FAMILY }),
    ...(tabular && { fontVariant: ["tabular-nums"] }),
    ...(center && { textAlign: "center" }),
  };

  return <RNText {...rest} style={[composed, style]} />;
}
