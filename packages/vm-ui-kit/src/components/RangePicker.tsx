/**
 * The reporting-period selector, shared by every screen that reports over a
 * period: the admin dashboard, the admin orders table, and a supplier's own
 * sales.
 *
 * One implementation on purpose. Two screens offering "past 7 days" that
 * disagree about whether it includes today would be worse than either choice
 * alone. The server resolves the period — this only names it, and the keys
 * below are exactly the ones vm-orders-api accepts.
 */
import { type ReactElement } from "react";

/** A period key the API understands, paired with what to call it. */
export type RangeOption = readonly [key: string, label: string];

/**
 * The presets, in the order someone reaches for them.
 *
 * "All time" is deliberately absent: it is meaningful only where the figures
 * are a running total to reconcile against (the supplier sales screen), and on
 * an operational screen it is a slow way to ask a question nobody asked. Pass
 * SALES_RANGES, or your own list, to include it.
 */
export const DEFAULT_RANGES: readonly RangeOption[] = [
  ["today", "Today"],
  ["2d", "Past 2 days"],
  ["3d", "Past 3 days"],
  // "Past 7 days", not "Past week", and matching the label vm-orders-api
  // echoes back on the same screen — one period must not have two names.
  ["7d", "Past 7 days"],
  ["1m", "Past month"],
  ["custom", "Custom range…"],
];

/**
 * The supplier sales variant: all time first, because that is the total a
 * grower reconciles their bank statement against, and it stays the default.
 */
export const SALES_RANGES: readonly RangeOption[] = [
  ["all", "All time"],
  ...DEFAULT_RANGES,
];

export interface RangePickerProps {
  range: string;
  from: string;
  to: string;
  onRange: (value: string) => void;
  onFrom: (value: string) => void;
  onTo: (value: string) => void;
  /** Defaults to DEFAULT_RANGES. */
  options?: readonly RangeOption[];
  /** Distinguishes the inputs when two pickers share a page. */
  idPrefix?: string;
  /** Announced to screen readers in place of "Reporting period". */
  label?: string;
}

/**
 * Period selector.
 *
 * A native <select> and native date inputs rather than a custom widget: they
 * are keyboard accessible, they use the device's own date picker on a phone,
 * and they carry no dependency. The dates are only sent once BOTH are set.
 */
export function RangePicker({
  range,
  from,
  to,
  onRange,
  onFrom,
  onTo,
  options = DEFAULT_RANGES,
  idPrefix = "range",
  label = "Reporting period",
}: RangePickerProps): ReactElement {
  // Nothing in the future can have orders in it, so the browser stops the
  // operator before the server has to.
  const today = new Date().toISOString().slice(0, 10);

  return (
    <div className="flex flex-wrap items-center gap-2">
      <label htmlFor={idPrefix} className="sr-only">
        {label}
      </label>
      <select
        id={idPrefix}
        value={range}
        onChange={(e) => onRange(e.target.value)}
        className="rounded-card border border-surface-border bg-surface-raised px-3 py-2 text-sm"
      >
        {options.map(([value, optionLabel]) => (
          <option key={value} value={value}>
            {optionLabel}
          </option>
        ))}
      </select>

      {range === "custom" && (
        <>
          <label htmlFor={`${idPrefix}-from`} className="sr-only">
            Start date
          </label>
          <input
            id={`${idPrefix}-from`}
            type="date"
            value={from}
            max={to || today}
            onChange={(e) => onFrom(e.target.value)}
            className="rounded-card border border-surface-border bg-surface-raised px-3 py-2 text-sm"
          />
          <span aria-hidden="true" className="text-primary-900/50">
            to
          </span>
          <label htmlFor={`${idPrefix}-to`} className="sr-only">
            End date
          </label>
          <input
            id={`${idPrefix}-to`}
            type="date"
            value={to}
            min={from || undefined}
            max={today}
            onChange={(e) => onTo(e.target.value)}
            className="rounded-card border border-surface-border bg-surface-raised px-3 py-2 text-sm"
          />
        </>
      )}
    </div>
  );
}
