/**
 * The Schedule / Repeat half of checkout — CLAUDE.md §6.7.
 *
 * The customer chooses a first delivery DATE, never a time: the time follows
 * from the cutoff. Nothing is charged here; each delivery is paid from the
 * wallet on its own charging afternoon. The panel shows the wallet against the
 * first delivery and offers a top-up inline, because a schedule a customer
 * cannot fund is one that silently skips.
 */
import { useEffect, useState, type ReactElement } from "react";
import { useNavigate } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { ApiError } from "@vayal/ui-kit";
import { api } from "../lib/api.js";
import { useAuth } from "../lib/auth.js";
import { CART_KEY } from "../lib/cart.js";
import {
  SCHEDULES_KEY,
  WEEKDAYS,
  useWallet,
  type Frequency,
  type Schedule,
  type ScheduleOptions,
} from "../lib/wallet.js";
import { chargeRuleText, dayOfMonth, formatRupees, weekdayOf } from "../lib/walletFormat.js";
import { AddMoney } from "./AddMoney.js";

const FREQUENCIES: ReadonlyArray<{ value: Frequency; label: string; hint: string }> = [
  { value: "once", label: "Once", hint: "On one date" },
  { value: "daily", label: "Daily", hint: "Every day" },
  { value: "weekly", label: "Weekly", hint: "Days you pick" },
  { value: "monthly", label: "Monthly", hint: "Same date" },
];

export function SchedulePlanner({
  addressId,
  totalPaise,
  disabled,
}: {
  addressId: string | null;
  /** The cart total today — the estimate for one delivery. */
  totalPaise: number;
  disabled: boolean;
}): ReactElement {
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const wallet = useWallet();
  const { user, initialising } = useAuth();
  const options = useQuery<ScheduleOptions, ApiError>({
    queryKey: ["schedule-options"],
    queryFn: () => api.get<ScheduleOptions>("/schedules/options"),
    enabled: !initialising && user !== null,
  });

  const [frequency, setFrequency] = useState<Frequency>("weekly");
  const [startDate, setStartDate] = useState("");
  const [weekdays, setWeekdays] = useState<number[]>([]);
  const [monthDay, setMonthDay] = useState<number | null>(null);
  const [endDate, setEndDate] = useState("");

  // Default the first date to the earliest allowed, and the weekly/monthly
  // pattern to that date's own weekday and day — so the common case is one
  // tap: choose "Weekly", done.
  useEffect(() => {
    if (options.data && startDate === "") {
      setStartDate(options.data.earliest_start);
      setWeekdays([weekdayOf(options.data.earliest_start)]);
      setMonthDay(dayOfMonth(options.data.earliest_start));
    }
  }, [options.data, startDate]);

  const create = useMutation<Schedule, ApiError>({
    mutationFn: () =>
      api.post<Schedule>("/schedules", {
        address_id: addressId,
        frequency,
        weekdays: frequency === "weekly" ? weekdays : [],
        ...(frequency === "monthly" && monthDay ? { day_of_month: monthDay } : {}),
        start_date: startDate,
        ...(frequency !== "once" && endDate ? { end_date: endDate } : {}),
      }),
    onSuccess: (schedule) => {
      void queryClient.invalidateQueries({ queryKey: CART_KEY });
      void queryClient.invalidateQueries({ queryKey: SCHEDULES_KEY });
      navigate(`/schedules/${schedule.id}`, { state: { created: true } });
    },
  });

  if (options.isPending || wallet.isPending) {
    return <p className="text-sm text-primary-900/60">Loading scheduling options…</p>;
  }
  if (options.isError || wallet.isError) {
    return (
      <p role="alert" className="text-sm text-accent-800">
        {(options.error ?? wallet.error)?.message}
      </p>
    );
  }

  const balance = wallet.data.balance_paise;
  const shortfall = Math.max(0, totalPaise - balance);
  const invalidPattern = frequency === "weekly" && weekdays.length === 0;
  const fieldErrors = (create.error?.details ?? {}) as Record<string, string>;

  return (
    <div className="space-y-4">
      {/* Frequency */}
      <div>
        <p className="mb-2 text-sm font-semibold text-primary-900">How often?</p>
        <div className="grid grid-cols-4 gap-1.5 rounded-2xl bg-cream-200/70 p-1">
          {FREQUENCIES.map((option) => (
            <button
              key={option.value}
              type="button"
              aria-pressed={frequency === option.value}
              onClick={() => setFrequency(option.value)}
              className={`rounded-xl px-1 py-2 text-center transition ${
                frequency === option.value
                  ? "bg-surface-raised text-primary-900 shadow-card"
                  : "text-primary-900/60 hover:text-primary-900"
              }`}
            >
              <span className="block text-sm font-semibold">{option.label}</span>
              <span className="block text-[10px] leading-tight opacity-70">{option.hint}</span>
            </button>
          ))}
        </div>
      </div>

      {/* First date */}
      <label className="block">
        <span className="mb-1.5 block text-sm font-semibold text-primary-900">
          {frequency === "once" ? "Delivery date" : "First delivery"}
        </span>
        <input
          type="date"
          value={startDate}
          min={options.data.earliest_start}
          max={options.data.latest_start}
          onChange={(event) => {
            const value = event.target.value;
            setStartDate(value);
            if (value) {
              if (weekdays.length <= 1) setWeekdays([weekdayOf(value)]);
              setMonthDay(dayOfMonth(value));
            }
          }}
          className="w-full rounded-xl border-[1.5px] border-cream-300 bg-surface-raised px-3 py-2.5 text-sm text-primary-900 outline-none focus:border-primary-500"
        />
        <span className="mt-1 block text-xs text-primary-900/55">
          Earliest {options.data.earliest_start_display}. You choose the day; delivery times
          follow our courier schedule.
        </span>
        {fieldErrors["start_date"] && (
          <span className="mt-1 block text-xs text-accent-800">{fieldErrors["start_date"]}</span>
        )}
      </label>

      {/* Pattern */}
      {frequency === "weekly" && (
        <div>
          <p className="mb-2 text-sm font-semibold text-primary-900">On which days?</p>
          <div className="grid grid-cols-7 gap-1">
            {WEEKDAYS.map((name, day) => {
              const on = weekdays.includes(day);
              return (
                <button
                  key={name}
                  type="button"
                  aria-pressed={on}
                  onClick={() =>
                    setWeekdays(on ? weekdays.filter((d) => d !== day) : [...weekdays, day].sort())
                  }
                  className={`rounded-xl py-2 text-xs font-semibold transition ${
                    on
                      ? "bg-primary-600 text-white shadow-card"
                      : "border border-cream-300 bg-surface-raised text-primary-800 hover:border-primary-300"
                  }`}
                >
                  {name}
                </button>
              );
            })}
          </div>
          {invalidPattern && (
            <p className="mt-1 text-xs text-accent-800">Pick at least one day.</p>
          )}
        </div>
      )}

      {frequency === "monthly" && monthDay !== null && (
        <p className="rounded-xl bg-cream-100 px-3 py-2 text-sm text-primary-900/75">
          On day <strong>{monthDay}</strong> of every month
          {monthDay >= 29 ? " (or the month's last day, when it is shorter)" : ""}. Change the
          first delivery date to change the day.
        </p>
      )}

      {frequency !== "once" && (
        <label className="block">
          <span className="mb-1.5 block text-sm font-semibold text-primary-900">
            Until <span className="font-normal text-primary-900/50">(optional)</span>
          </span>
          <input
            type="date"
            value={endDate}
            min={startDate || options.data.earliest_start}
            max={options.data.max_end}
            onChange={(event) => setEndDate(event.target.value)}
            className="w-full rounded-xl border-[1.5px] border-cream-300 bg-surface-raised px-3 py-2.5 text-sm text-primary-900 outline-none focus:border-primary-500"
          />
          <span className="mt-1 block text-xs text-primary-900/55">
            Leave empty to repeat until you pause or cancel.
          </span>
        </label>
      )}

      {/* Wallet */}
      <div className="rounded-2xl bg-primary-900 p-4 text-white">
        <div className="flex items-center justify-between">
          <span className="text-xs font-semibold uppercase tracking-[0.18em] text-gold-200">
            Paid from wallet
          </span>
          <span className="font-display text-xl font-semibold tabular-nums">
            {wallet.data.balance_display}
          </span>
        </div>
        <p className="mt-2 text-xs leading-relaxed text-white/65">
          Each delivery is charged at {chargeRuleText(options.data)}, at that day&rsquo;s
          prices — about <strong className="text-white">{formatRupees(totalPaise)}</strong> for
          this basket today. Nothing is charged now.
        </p>
      </div>

      {shortfall > 0 && (
        <div className="rounded-2xl border border-gold-200 bg-gold-50/70 p-4">
          <p className="mb-3 text-sm font-medium text-gold-700">
            Add at least {formatRupees(shortfall)} so your first delivery isn&rsquo;t skipped.
          </p>
          <AddMoney limits={wallet.data.limits} suggestPaise={shortfall} compact />
        </div>
      )}

      {create.isError && (
        <p role="alert" className="rounded-xl bg-accent-50 px-3 py-2 text-sm text-accent-800">
          {create.error.message}
        </p>
      )}

      <button
        type="button"
        disabled={disabled || !addressId || !startDate || invalidPattern || create.isPending}
        onClick={() => create.mutate()}
        className="flex h-12 w-full items-center justify-center rounded-full bg-primary-600 px-5 text-[0.9375rem] font-semibold text-white shadow-[0_8px_24px_-8px] shadow-primary-600/60 transition hover:bg-primary-700 disabled:cursor-not-allowed disabled:bg-surface-sunken disabled:text-primary-900/40 disabled:shadow-none"
      >
        {create.isPending
          ? "Creating…"
          : frequency === "once"
            ? "Schedule this delivery"
            : "Start repeat order"}
      </button>
      <p className="text-center text-xs text-primary-900/50">
        Skip, pause or cancel any time before a delivery is charged.
      </p>
    </div>
  );
}
