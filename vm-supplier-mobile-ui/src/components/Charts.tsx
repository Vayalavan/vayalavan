/**
 * The insights screen's marks: a labelled bar and a column series.
 *
 * The native counterpart of vm-ui-kit's chart primitives, and a deliberate
 * duplicate rather than an import — the mobile apps sit outside the npm
 * workspaces and cannot use the kit, which is React DOM anyway (CLAUDE.md §2).
 * The numbers they draw come from the same endpoint and the same shared
 * arithmetic in `lib/analytics`, so the two cannot disagree about a value;
 * only the geometry differs.
 *
 * The chart forms match the web: the one time series is a line. It needs
 * react-native-svg, which is pinned by `expo install` to the version SDK 54
 * expects and is bundled INSIDE Expo Go — so it draws on a real handset
 * without an EAS development build, which is the property the SDK pin exists
 * to protect (CLAUDE.md §3).
 *
 * Every bar is labelled with its value in text: the bar carries the shape, the
 * text carries the number, and neither depends on the other being legible.
 */
import { useState, type ReactElement } from "react";
import { Pressable, View, type LayoutChangeEvent } from "react-native";
import Svg, { Circle, Line, Polyline } from "react-native-svg";

import { peakOf } from "../lib/analytics";
import { colors, radius, spacing } from "../theme/tokens";
import { Text } from "./Text";

/** Which brand colour a mark carries. */
export type ChartTone = "primary" | "accent";

function toneColor(tone: ChartTone): string {
  return tone === "accent" ? colors.accent[500] : colors.primary[600];
}

export interface MetricBarProps {
  label: string;
  /** The formatted value, printed beside the label. */
  value: string;
  /** 0–1. Clamped, so a caller cannot draw a bar past its track. */
  share: number;
  note?: string;
  tone?: ChartTone;
}

/** One labelled bar in a ranked list. */
export function MetricBar({
  label, value, share, note, tone = "primary",
}: MetricBarProps): ReactElement {
  const width = Math.max(0, Math.min(1, share)) * 100;

  return (
    <View style={{ gap: spacing.xs }}>
      <View style={{ flexDirection: "row", alignItems: "baseline", gap: spacing.sm }}>
        <Text tone="strong" numberOfLines={1} style={{ flex: 1 }}>
          {label}
        </Text>
        <Text variant="bodyStrong" tone="strong" tabular>
          {value}
        </Text>
      </View>

      <View
        style={{
          height: 8,
          borderRadius: radius.pill,
          backgroundColor: colors.surface.sunken,
          overflow: "hidden",
        }}
      >
        <View
          style={{
            height: "100%",
            width: `${width}%`,
            borderRadius: radius.pill,
            backgroundColor: toneColor(tone),
          }}
        />
      </View>

      {note !== undefined && (
        <Text variant="caption" tone="muted">
          {note}
        </Text>
      )}
    </View>
  );
}

export interface SplitSegment {
  label: string;
  /** The magnitude the segment is sized by — a count, or paise. */
  value: number;
  /**
   * What the legend prints beside the label.
   *
   * Required whenever `value` is not something a reader should see raw: a bar
   * sized by paise must not print "1180000" in its legend.
   */
  display?: string;
  tone: ChartTone;
}

/** Two parts of one whole, with both parts named and counted. */
export function SplitBar({ segments }: { segments: SplitSegment[] }): ReactElement {
  const total = segments.reduce((sum, s) => sum + s.value, 0) || 1;

  return (
    <View style={{ gap: spacing.sm }}>
      {/* A 2px gap between fills — without it two segments of similar weight
          read as one bar. */}
      <View
        style={{
          flexDirection: "row",
          gap: 2,
          height: 10,
          borderRadius: radius.pill,
          backgroundColor: colors.surface.sunken,
          overflow: "hidden",
        }}
      >
        {segments.map((segment) => (
          <View
            key={segment.label}
            style={{
              width: `${(segment.value / total) * 100}%`,
              height: "100%",
              backgroundColor: toneColor(segment.tone),
            }}
          />
        ))}
      </View>

      <View style={{ flexDirection: "row", flexWrap: "wrap", gap: spacing.md }}>
        {segments.map((segment) => (
          <View
            key={segment.label}
            style={{ flexDirection: "row", alignItems: "center", gap: spacing.xs }}
          >
            <View
              style={{
                width: 8,
                height: 8,
                borderRadius: radius.pill,
                backgroundColor: toneColor(segment.tone),
              }}
            />
            <Text variant="caption" tone="muted">
              {segment.label}
            </Text>
            {segment.display !== undefined && (
              <Text variant="caption" tone="strong" tabular>
                {segment.display}
              </Text>
            )}
          </View>
        ))}
      </View>
    </View>
  );
}

/** One point in a series over days. */
export interface SeriesPoint {
  key: string;
  /** How the point is labelled: "18 Aug". */
  label: string;
  /** The magnitude drawn. The chart never interprets it. */
  value: number;
  /** The formatted value, e.g. "₹1,250.00". Charts never format money. */
  display: string;
  /** A second callout line, e.g. "14 packs · 7 orders". */
  note?: string;
}

/** Width of a callout, in points. Fixed so it can be clamped to the plot. */
const CALLOUT_WIDTH = 150;

/**
 * Roughly how tall a callout is, used only to keep one inside the plot.
 *
 * Android clips children that spill past their parent's bounds, so a callout
 * floated above a full-height bar would simply not appear on half the
 * handsets — the failure that is invisible on a simulator running the other
 * platform.
 */
const CALLOUT_HEIGHT = 62;

/**
 * One point, spoken.
 *
 * Every mark is a button carrying this, so the series is readable with
 * VoiceOver or TalkBack without seeing the callout it opens.
 */
function pointLabel(point: SeriesPoint): string {
  return point.note === undefined
    ? `${point.label}: ${point.display}`
    : `${point.label}: ${point.display}, ${point.note}`;
}

/**
 * The tapped point's figures.
 *
 * The web shows this on hover; a handset has no hover, so it appears on touch
 * and stays until another point is tapped or the same one is tapped again.
 * Clamped to the plot's own width — a callout on the last day of the week
 * would otherwise be half off the side of the phone.
 */
function Callout({
  point,
  centreAt,
  plotWidth,
  bottom,
}: {
  point: SeriesPoint;
  /** Where the tapped mark sits, in points from the plot's left edge. */
  centreAt: number;
  plotWidth: number;
  /** How far above the plot's bottom edge to float. */
  bottom: number;
}): ReactElement {
  const left = Math.max(
    0,
    Math.min(centreAt - CALLOUT_WIDTH / 2, Math.max(0, plotWidth - CALLOUT_WIDTH)),
  );

  return (
    <View
      pointerEvents="none"
      style={{
        position: "absolute",
        left,
        bottom,
        width: CALLOUT_WIDTH,
        padding: spacing.sm,
        gap: 2,
        borderRadius: radius.card,
        borderWidth: 1,
        borderColor: colors.surface.border,
        backgroundColor: colors.surface.raised,
        // The plot's marks are opaque; without a shadow the callout reads as
        // part of the chart rather than as something floating over it.
        shadowColor: "#1a3628",
        shadowOpacity: 0.12,
        shadowRadius: 8,
        shadowOffset: { width: 0, height: 2 },
        elevation: 3,
      }}
    >
      <Text variant="caption" tone="strong">
        {point.label}
      </Text>
      <Text variant="caption" tone="body" tabular>
        {point.display}
      </Text>
      {point.note !== undefined && (
        <Text variant="caption" tone="muted" tabular>
          {point.note}
        </Text>
      )}
    </View>
  );
}

/**
 * The same series as a line.
 *
 * Coordinates are computed in REAL pixels from the measured width rather than
 * in a scaled viewBox: a non-uniform viewBox stretch — which is how the web
 * fills its card — turns every circle into an ellipse, and there is no CSS
 * here to compensate. One `onLayout` costs a single extra render on mount and
 * keeps the markers round.
 */
export function LineChart({
  points,
  accessibilityLabel,
}: {
  points: SeriesPoint[];
  accessibilityLabel: string;
}): ReactElement | null {
  const [width, setWidth] = useState(0);
  const [active, setActive] = useState<number | null>(null);

  const first = points[0];
  const last = points[points.length - 1];
  if (first === undefined || last === undefined) return null;

  const height = 130;
  // Room for a marker on the peak and on the baseline, so neither is clipped.
  const pad = 10;
  const peak = peakOf(points.map((point) => point.value));

  const x = (index: number) =>
    pad + (index * Math.max(0, width - pad * 2)) / Math.max(1, points.length - 1);
  const y = (value: number) => height - pad - (value / peak) * (height - pad * 2);

  return (
    <View style={{ gap: spacing.xs }}>
      <View
        accessibilityLabel={accessibilityLabel}
        onLayout={(event: LayoutChangeEvent) => setWidth(event.nativeEvent.layout.width)}
        style={{ height }}
      >
        {width > 0 && (
          <Svg width={width} height={height}>
            {/* A baseline only. Gridlines behind seven points are furniture
                nobody reads. */}
            <Line
              x1={pad}
              y1={height - pad}
              x2={width - pad}
              y2={height - pad}
              stroke={colors.surface.border}
              strokeWidth={1}
            />
            <Polyline
              points={points.map((point, i) => `${x(i)},${y(point.value)}`).join(" ")}
              fill="none"
              stroke={colors.primary[600]}
              strokeWidth={2}
              strokeLinejoin="round"
              strokeLinecap="round"
            />
            {points.map((point, i) => (
              <Circle
                key={point.key}
                cx={x(i)}
                cy={y(point.value)}
                r={active === i ? 6 : 4}
                fill={active === i ? colors.primary[700] : colors.primary[600]}
                // A 2px surface ring keeps a marker legible where the line
                // passes under it.
                stroke={colors.surface.raised}
                strokeWidth={2}
              />
            ))}
          </Svg>
        )}

        {/* Touch targets, over the drawing. Full-height columns rather than
            the dots themselves: an 8px circle is not a tap target, and the
            day someone wants is often the one on the baseline. */}
        <View style={{ position: "absolute", inset: 0, flexDirection: "row" }}>
          {points.map((point, i) => (
            <Pressable
              key={point.key}
              onPress={() => setActive(active === i ? null : i)}
              accessibilityRole="button"
              accessibilityLabel={pointLabel(point)}
              style={{ flex: 1 }}
            />
          ))}
        </View>

        {active !== null && points[active] !== undefined && width > 0 && (
          <Callout
            point={points[active]!}
            centreAt={x(active)}
            plotWidth={width}
            // Floated above the marker, and never past the top of the plot.
            bottom={Math.min(height - CALLOUT_HEIGHT, height - y(points[active]!.value) + 10)}
          />
        )}
      </View>

      <View style={{ flexDirection: "row", justifyContent: "space-between" }}>
        <Text variant="caption" tone="faint">
          {first.label}
        </Text>
        {/* The last point is the only one labelled directly: it is the one a
            grower is actually looking for, and seven labels collide at 360px. */}
        <Text variant="caption" tone="strong" tabular>
          {last.label} · {last.display}
        </Text>
      </View>
    </View>
  );
}
