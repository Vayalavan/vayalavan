/**
 * The chart primitives shared by the reporting screens.
 *
 * Extracted from the admin dashboard the moment the supplier app needed the
 * same shapes: two copies of a bar row drift within a release, and a chart
 * that drifts is a chart that disagrees with itself between screens.
 *
 * Deliberately NOT a charting library. Everything here is layout — divs with a
 * width, one SVG path — because the shapes the platform actually needs are
 * ranked bars and a series over days, and the fixed stack (CLAUDE.md §3) does
 * not include a chart dependency.
 *
 * Three rules hold across all of them:
 *   - every value is written out as text beside its mark, so the bar shows the
 *     shape and the text carries the number;
 *   - colour comes from the brand tokens only (CLAUDE.md §9), with the muted
 *     field green for magnitude and amber where something needs attention;
 *   - a chart always ships its own screen-reader table, so an accessible
 *     version cannot be forgotten by the screen using it.
 */
import { useState, type ReactElement, type ReactNode } from "react";

/** Which brand colour a mark carries. */
export type ChartTone = "primary" | "accent";

function toneFill(tone: ChartTone): string {
  return tone === "accent" ? "bg-accent-500" : "bg-primary-600";
}

/** One point in a series over time. */
export interface SeriesPoint {
  /** Stable identity — an ISO date on every current caller. */
  key: string;
  /** How the point is labelled on the axis and in its tooltip: "18 Aug". */
  label: string;
  /** The magnitude drawn. Paise, counts — the chart never interprets it. */
  value: number;
  /** The formatted value, e.g. "₹1,250.00". Charts never format money. */
  display: string;
  /** A second tooltip line, e.g. "19 paid of 22 placed". */
  note?: string;
}

export interface ChartHeadingProps {
  title: string;
  hint?: string;
}

/** A chart's title row, so every card introduces its chart the same way. */
export function ChartHeading({ title, hint }: ChartHeadingProps): ReactElement {
  return (
    <div className="flex flex-wrap items-baseline justify-between gap-x-3 gap-y-1">
      <h3 className="text-sm font-semibold text-primary-900">{title}</h3>
      {hint && <p className="text-xs text-primary-900/50">{hint}</p>}
    </div>
  );
}

export interface ChartTableProps {
  caption: string;
  head: string[];
  rows: Array<Array<ReactNode>>;
}

/**
 * A chart's numbers as a table, for screen readers.
 *
 * Visually hidden rather than absent: a chart nobody can read is not an
 * accessible chart, and this is the same data the marks are drawn from.
 */
export function ChartTable({ caption, head, rows }: ChartTableProps): ReactElement {
  return (
    <table className="sr-only">
      <caption>{caption}</caption>
      <thead>
        <tr>{head.map((h) => <th key={h} scope="col">{h}</th>)}</tr>
      </thead>
      <tbody>
        {rows.map((row, i) => (
          <tr key={i}>{row.map((cell, j) => <td key={j}>{cell}</td>)}</tr>
        ))}
      </tbody>
    </table>
  );
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

/**
 * One labelled bar in a ranked list.
 *
 * The value is always written out beside the label: the bar shows the shape of
 * the distribution, the text carries the number, and neither depends on the
 * other being legible.
 */
export function MetricBar({
  label, value, share, note, tone = "primary",
}: MetricBarProps): ReactElement {
  const width = Math.max(0, Math.min(1, share)) * 100;

  return (
    <div>
      <div className="flex items-baseline justify-between gap-3">
        <span className="truncate text-sm text-primary-900" title={label}>{label}</span>
        <span className="shrink-0 text-sm font-medium tabular-nums text-primary-900">
          {value}
        </span>
      </div>
      <div className="mt-1 h-2 w-full overflow-hidden rounded-full bg-surface-sunken">
        <div className={`h-full rounded-full ${toneFill(tone)}`} style={{ width: `${width}%` }} />
      </div>
      {note && <p className="mt-1 text-xs text-primary-900/50">{note}</p>}
    </div>
  );
}

export interface SplitBarSegment {
  label: string;
  /** The magnitude the segment is sized by — a count, or paise. */
  value: number;
  /**
   * What the legend prints instead of `value`.
   *
   * Required whenever `value` is not something a reader should see raw: a bar
   * sized by paise must not print "1180000" in its legend, and the caller is
   * the only one that knows which it is.
   */
  display?: string;
  tone: ChartTone;
}

/** Two or more parts of one whole, with every part named and counted. */
export function SplitBar({ segments }: { segments: SplitBarSegment[] }): ReactElement {
  const total = segments.reduce((sum, s) => sum + s.value, 0) || 1;

  return (
    <div>
      {/* gap-0.5 is the 2px surface gap between adjacent fills — without it
          two segments of similar weight read as one bar. */}
      <div className="flex h-2.5 w-full gap-0.5 overflow-hidden rounded-full bg-surface-sunken">
        {segments.map((s) => (
          <div
            key={s.label}
            className={`h-full first:rounded-l-full last:rounded-r-full ${toneFill(s.tone)}`}
            style={{ width: `${(s.value / total) * 100}%` }}
          />
        ))}
      </div>
      <ul className="mt-2 flex flex-wrap gap-x-4 gap-y-1 text-xs text-primary-900/70">
        {segments.map((s) => (
          <li key={s.label} className="flex items-center gap-1.5">
            <span aria-hidden="true" className={`inline-block h-2 w-2 rounded-full ${toneFill(s.tone)}`} />
            {s.label}
            <span className="font-medium tabular-nums text-primary-900">
              {s.display ?? s.value}
            </span>
            <span className="text-primary-900/45">({chartSharePct(s.value, total)})</span>
          </li>
        ))}
      </ul>
    </div>
  );
}

export interface TimeChartProps {
  points: SeriesPoint[];
  /** Announced in place of the drawing. The table below carries the figures. */
  ariaLabel: string;
  tableCaption: string;
  /** Column heading for the value column, e.g. "Revenue". */
  valueHeading: string;
}

/**
 * A series over days, as columns.
 *
 * One measure, one axis, always. A second measure on a different scale belongs
 * in the tooltip or in its own chart — two y-scales on one chart is the
 * fastest way to draw a relationship that is not there.
 */
export function ColumnChart({
  points, ariaLabel, tableCaption, valueHeading,
}: TimeChartProps): ReactElement | null {
  const [active, setActive] = useState<number | null>(null);

  const first = points[0];
  const last = points[points.length - 1];
  if (!first || !last) return null;

  const peak = Math.max(...points.map((p) => p.value), 1);

  return (
    <div>
      <div
        className="mt-4 flex items-end gap-1 border-b border-surface-border"
        style={{ height: "9rem" }}
        role="img"
        aria-label={ariaLabel}
      >
        {points.map((point, i) => {
          // A day that traded never renders as nothing: 2% keeps a sliver
          // visible. A day that did NOT trade gets a flat grey stub instead —
          // so "sold almost nothing" and "sold nothing" cannot be confused,
          // and a quiet day is still visibly a day rather than a hole.
          const traded = point.value > 0;
          const height = traded ? Math.max(2, (point.value / peak) * 100) : 0;

          return (
            <div
              key={point.key}
              className="relative flex h-full flex-1 flex-col justify-end"
              onMouseEnter={() => setActive(i)}
              onMouseLeave={() => setActive(null)}
              aria-hidden="true"
            >
              {active === i && <PointTooltip point={point} />}
              {traded ? (
                <div
                  className={`mx-auto w-full max-w-10 rounded-t ${
                    active === i ? "bg-primary-700" : "bg-primary-600"
                  }`}
                  style={{ height: `${height}%` }}
                />
              ) : (
                <div className="mx-auto h-0.5 w-full max-w-10 rounded-full bg-surface-border" />
              )}
            </div>
          );
        })}
      </div>

      {/* Ends only. A label under every column collides the moment a month is
          selected, and the tooltip already names the day being read. */}
      <div className="mt-1 flex justify-between text-xs text-primary-900/50">
        <span>{first.label}</span>
        <span>{last.label}</span>
      </div>

      <ChartTable
        caption={tableCaption}
        head={["Day", valueHeading, "Detail"]}
        rows={points.map((p) => [p.label, p.display, p.note ?? ""])}
      />
    </div>
  );
}

/**
 * The same series as a line.
 *
 * Drawn as an SVG path stretched with preserveAspectRatio="none", so the line
 * fills whatever width it is given without measuring anything in JavaScript.
 * That stretch is also why the POINTS are HTML dots positioned over the plot
 * rather than SVG circles: a circle in a non-uniformly scaled viewBox comes
 * out an ellipse. The stroke survives because vector-effect="non-scaling-stroke"
 * is exempt from the scale.
 */
export function LineChart({
  points, ariaLabel, tableCaption, valueHeading,
}: TimeChartProps): ReactElement | null {
  const [active, setActive] = useState<number | null>(null);

  const first = points[0];
  const last = points[points.length - 1];
  if (!first || !last) return null;

  const peak = Math.max(...points.map((p) => p.value), 1);

  // Plotted in a fixed box and scaled by CSS. The padding leaves room for the
  // markers, so a point on the peak is not clipped by the viewBox.
  const width = 700;
  const height = 200;
  const pad = 12;
  const x = (i: number) => pad + (i * (width - pad * 2)) / Math.max(1, points.length - 1);
  const y = (value: number) => height - pad - (value / peak) * (height - pad * 2);

  return (
    <div>
      <div className="relative mt-4">
        <svg
          viewBox={`0 0 ${width} ${height}`}
          className="h-40 w-full"
          preserveAspectRatio="none"
          role="img"
          aria-label={ariaLabel}
        >
          {/* A baseline only. Gridlines behind seven points are furniture
              nobody reads. */}
          <line
            x1={pad} y1={height - pad} x2={width - pad} y2={height - pad}
            className="stroke-surface-border" strokeWidth={1}
            vectorEffect="non-scaling-stroke"
          />
          <polyline
            points={points.map((p, i) => `${x(i)},${y(p.value)}`).join(" ")}
            fill="none"
            className="stroke-primary-600"
            strokeWidth={2}
            strokeLinejoin="round"
            strokeLinecap="round"
            vectorEffect="non-scaling-stroke"
          />
        </svg>

        {/* The points, and the hit targets. Round because they are HTML, and
            wider than they look because a 10px dot is a miserable thing to
            aim at — the button is 28px, the dot inside it is not. */}
        {points.map((point, i) => (
          <button
            type="button"
            key={point.key}
            className="absolute flex h-7 w-7 -translate-x-1/2 -translate-y-1/2 items-center justify-center rounded-full"
            style={{
              left: `${(x(i) / width) * 100}%`,
              top: `${(y(point.value) / height) * 100}%`,
            }}
            onMouseEnter={() => setActive(i)}
            onMouseLeave={() => setActive(null)}
            onFocus={() => setActive(i)}
            onBlur={() => setActive(null)}
          >
            <span className="sr-only">
              {point.label}: {point.display}
              {point.note ? `, ${point.note}` : ""}
            </span>
            <span
              aria-hidden="true"
              className={`block rounded-full ring-2 ring-surface-raised ${
                active === i ? "h-3.5 w-3.5 bg-primary-700" : "h-2.5 w-2.5 bg-primary-600"
              }`}
            />
          </button>
        ))}

        {active !== null && points[active] && (
          <div
            className="pointer-events-none absolute z-10 w-36 -translate-x-1/2 -translate-y-full rounded-card border border-surface-border bg-surface-raised p-2 text-xs shadow-card"
            style={{
              left: `${(x(active) / width) * 100}%`,
              top: `${(y(points[active].value) / height) * 100 - 6}%`,
            }}
          >
            <TooltipBody point={points[active]} />
          </div>
        )}
      </div>

      <div className="mt-1 flex justify-between text-xs text-primary-900/50">
        <span>{first.label}</span>
        {/* The last point is the only one labelled directly: it is the one
            someone is actually looking for, and seven labels collide. */}
        <span className="font-medium text-primary-900">
          {last.label} · {last.display}
        </span>
      </div>

      <ChartTable
        caption={tableCaption}
        head={["Day", valueHeading, "Detail"]}
        rows={points.map((p) => [p.label, p.display, p.note ?? ""])}
      />
    </div>
  );
}

function PointTooltip({ point }: { point: SeriesPoint }): ReactElement {
  return (
    <div className="absolute bottom-full left-1/2 z-10 mb-1 w-36 -translate-x-1/2 rounded-card border border-surface-border bg-surface-raised p-2 text-xs shadow-card">
      <TooltipBody point={point} />
    </div>
  );
}

function TooltipBody({ point }: { point: SeriesPoint }): ReactElement {
  return (
    <>
      <p className="font-medium text-primary-900">{point.label}</p>
      <p className="tabular-nums text-primary-900/70">{point.display}</p>
      {point.note && <p className="tabular-nums text-primary-900/70">{point.note}</p>}
    </>
  );
}

/**
 * A share as a percentage, for chart labels.
 *
 * Below 1% reads as "0%" and looks like a bug beside a non-zero amount, so it
 * is written as "<1%" instead.
 */
export function chartSharePct(part: number, whole: number): string {
  if (whole <= 0) return "0%";
  const pct = (part / whole) * 100;
  return pct > 0 && pct < 1 ? "<1%" : `${Math.round(pct)}%`;
}
