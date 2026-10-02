/**
 * The Schedule / Repeat half of checkout — CLAUDE.md §6.7. The phone's
 * counterpart of vm-client-ui's SchedulePlanner.
 *
 * A DATE is chosen, never a time. The first date is a horizontal strip of
 * days rather than a calendar: the window is 60 days, the next few are what
 * people pick, and a strip is one thumb movement where a picker is three. The
 * end of a repeat is a choice of spans for the same reason.
 */
import { useEffect, useState, type ReactElement } from "react";
import { Pressable, ScrollView, View } from "react-native";
import { useNavigation } from "@react-navigation/native";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import type { ApiError } from "../lib/api";
import { useAuth } from "../lib/auth";
import { CART_KEY } from "../lib/cart";
import { api } from "../lib/client";
import {
  SCHEDULES_KEY,
  SCHEDULE_OPTIONS_KEY,
  WEEKDAYS,
  isoDatesFrom,
  useWallet,
  type Frequency,
  type Schedule,
  type ScheduleOptions,
} from "../lib/wallet";
import { chargeRuleText, dayOfMonth, formatRupees, weekdayOf } from "../lib/walletFormat";
import { colors, radius, spacing } from "../theme/tokens";
import { AddMoney } from "./AddMoney";
import { Button } from "./Button";
import { Text } from "./Text";

const FREQUENCIES: ReadonlyArray<{ value: Frequency; label: string }> = [
  { value: "once", label: "Once" },
  { value: "daily", label: "Daily" },
  { value: "weekly", label: "Weekly" },
  { value: "monthly", label: "Monthly" },
];

/** How long a repeat runs, in months; null is until cancelled. */
const SPANS: ReadonlyArray<{ months: number | null; label: string }> = [
  { months: null, label: "No end" },
  { months: 1, label: "1 month" },
  { months: 3, label: "3 months" },
  { months: 6, label: "6 months" },
];

const MONTHS = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];

function addMonthsIso(iso: string, months: number): string {
  const [y, m, d] = iso.split("-").map(Number);
  const target = new Date(Date.UTC(y ?? 1970, (m ?? 1) - 1 + months, d ?? 1));
  // The day before the same date next month(s): "for 1 month" from 5 Oct ends 4 Nov.
  return new Date(target.getTime() - 86_400_000).toISOString().slice(0, 10);
}

export function SchedulePlanner({
  addressId,
  totalPaise,
}: {
  addressId: string | null;
  totalPaise: number;
}): ReactElement {
  const { user } = useAuth();
  const navigation = useNavigation();
  const queryClient = useQueryClient();
  const wallet = useWallet();
  const options = useQuery<ScheduleOptions, ApiError>({
    queryKey: SCHEDULE_OPTIONS_KEY,
    queryFn: () => api.get<ScheduleOptions>("/schedules/options"),
    enabled: Boolean(user),
  });

  const [frequency, setFrequency] = useState<Frequency>("weekly");
  const [startDate, setStartDate] = useState("");
  const [weekdays, setWeekdays] = useState<number[]>([]);
  const [span, setSpan] = useState<number | null>(null);

  useEffect(() => {
    if (options.data !== undefined && startDate === "") {
      setStartDate(options.data.earliest_start);
      setWeekdays([weekdayOf(options.data.earliest_start)]);
    }
  }, [options.data, startDate]);

  const create = useMutation<Schedule, ApiError>({
    mutationFn: () =>
      api.post<Schedule>("/schedules", {
        address_id: addressId,
        frequency,
        weekdays: frequency === "weekly" ? weekdays : [],
        ...(frequency === "monthly" ? { day_of_month: dayOfMonth(startDate) } : {}),
        start_date: startDate,
        ...(frequency !== "once" && span !== null ? { end_date: addMonthsIso(startDate, span) } : {}),
      }),
    onSuccess: (schedule) => {
      void queryClient.invalidateQueries({ queryKey: CART_KEY });
      void queryClient.invalidateQueries({ queryKey: SCHEDULES_KEY });
      // Into the Account tab, on top of its home, so Back lands somewhere.
      navigation.navigate("Main", {
        screen: "Account",
        params: { screen: "ScheduleDetail", params: { scheduleId: schedule.id }, initial: false },
      });
    },
  });

  if (options.isPending || wallet.isPending) {
    return <Text tone="muted">Loading scheduling options…</Text>;
  }
  if (options.isError || wallet.isError) {
    return <Text tone="danger">{(options.error ?? wallet.error)?.message}</Text>;
  }

  const dates = isoDatesFrom(
    options.data.earliest_start,
    // Inclusive of latest_start.
    Math.round(
      (Date.parse(options.data.latest_start) - Date.parse(options.data.earliest_start)) / 86_400_000,
    ) + 1,
  );
  const balance = wallet.data.balance_paise;
  const shortfall = Math.max(0, totalPaise - balance);
  const invalidPattern = frequency === "weekly" && weekdays.length === 0;

  return (
    <View style={{ gap: spacing.lg }}>
      {/* Frequency */}
      <View style={{ gap: spacing.sm }}>
        <Text variant="label" tone="strong">
          How often?
        </Text>
        <View
          accessibilityRole="radiogroup"
          style={{
            flexDirection: "row",
            gap: 4,
            padding: 4,
            borderRadius: 16,
            backgroundColor: colors.cream[200],
          }}
        >
          {FREQUENCIES.map((option) => {
            const on = frequency === option.value;
            return (
              <Pressable
                key={option.value}
                onPress={() => setFrequency(option.value)}
                accessibilityRole="radio"
                accessibilityState={{ selected: on }}
                style={{
                  flex: 1,
                  minHeight: 40,
                  alignItems: "center",
                  justifyContent: "center",
                  borderRadius: 12,
                  backgroundColor: on ? colors.surface.raised : "transparent",
                }}
              >
                <Text variant="label" tone={on ? "strong" : "muted"}>
                  {option.label}
                </Text>
              </Pressable>
            );
          })}
        </View>
      </View>

      {/* First date */}
      <View style={{ gap: spacing.sm }}>
        <Text variant="label" tone="strong">
          {frequency === "once" ? "Delivery date" : "First delivery"}
        </Text>
        <ScrollView horizontal showsHorizontalScrollIndicator={false} contentContainerStyle={{ gap: spacing.sm }}>
          {dates.map((iso) => {
            const on = iso === startDate;
            const [, m, d] = iso.split("-").map(Number);
            return (
              <Pressable
                key={iso}
                onPress={() => {
                  setStartDate(iso);
                  if (weekdays.length <= 1) setWeekdays([weekdayOf(iso)]);
                }}
                accessibilityRole="radio"
                accessibilityState={{ selected: on }}
                accessibilityLabel={`${WEEKDAYS[weekdayOf(iso)]} ${d} ${MONTHS[(m ?? 1) - 1]}`}
                style={{
                  width: 58,
                  paddingVertical: spacing.sm,
                  alignItems: "center",
                  borderRadius: 16,
                  borderWidth: 1.5,
                  borderColor: on ? colors.primary[600] : colors.cream[300],
                  backgroundColor: on ? colors.primary[600] : colors.surface.raised,
                }}
              >
                <Text variant="caption" tone={on ? "onPrimary" : "muted"}>
                  {WEEKDAYS[weekdayOf(iso)]}
                </Text>
                <Text variant="title" tone={on ? "onPrimary" : "strong"} tabular>
                  {d}
                </Text>
                <Text variant="caption" tone={on ? "onPrimary" : "faint"}>
                  {MONTHS[(m ?? 1) - 1]}
                </Text>
              </Pressable>
            );
          })}
        </ScrollView>
        <Text variant="caption" tone="faint">
          You choose the day; delivery times follow our courier schedule.
        </Text>
      </View>

      {frequency === "weekly" && (
        <View style={{ gap: spacing.sm }}>
          <Text variant="label" tone="strong">
            On which days?
          </Text>
          <View style={{ flexDirection: "row", gap: 4 }}>
            {WEEKDAYS.map((name, day) => {
              const on = weekdays.includes(day);
              return (
                <Pressable
                  key={name}
                  onPress={() =>
                    setWeekdays(on ? weekdays.filter((d) => d !== day) : [...weekdays, day].sort())
                  }
                  accessibilityRole="checkbox"
                  accessibilityState={{ checked: on }}
                  style={{
                    flex: 1,
                    minHeight: 40,
                    alignItems: "center",
                    justifyContent: "center",
                    borderRadius: 12,
                    borderWidth: 1,
                    borderColor: on ? colors.primary[600] : colors.cream[300],
                    backgroundColor: on ? colors.primary[600] : colors.surface.raised,
                  }}
                >
                  <Text variant="caption" tone={on ? "onPrimary" : "strong"} style={{ fontWeight: "700" }}>
                    {name}
                  </Text>
                </Pressable>
              );
            })}
          </View>
          {invalidPattern && (
            <Text variant="caption" tone="danger">
              Pick at least one day.
            </Text>
          )}
        </View>
      )}

      {frequency === "monthly" && startDate !== "" && (
        <Text tone="muted">
          On day {dayOfMonth(startDate)} of every month
          {dayOfMonth(startDate) >= 29 ? " (or the month’s last day, when it is shorter)" : ""}.
        </Text>
      )}

      {frequency !== "once" && (
        <View style={{ gap: spacing.sm }}>
          <Text variant="label" tone="strong">
            For how long?
          </Text>
          <View style={{ flexDirection: "row", flexWrap: "wrap", gap: spacing.sm }}>
            {SPANS.map((option) => {
              const on = span === option.months;
              return (
                <Pressable
                  key={option.label}
                  onPress={() => setSpan(option.months)}
                  accessibilityRole="radio"
                  accessibilityState={{ selected: on }}
                  style={{
                    paddingHorizontal: spacing.md,
                    minHeight: 36,
                    justifyContent: "center",
                    borderRadius: radius.pill,
                    borderWidth: 1,
                    borderColor: on ? colors.primary[600] : colors.cream[300],
                    backgroundColor: on ? colors.primary[50] : colors.surface.raised,
                  }}
                >
                  <Text variant="label" tone={on ? "strong" : "muted"}>
                    {option.label}
                  </Text>
                </Pressable>
              );
            })}
          </View>
        </View>
      )}

      {/* Wallet */}
      <View style={{ borderRadius: 20, padding: spacing.lg, gap: spacing.sm, backgroundColor: colors.primary[900] }}>
        <View style={{ flexDirection: "row", justifyContent: "space-between", alignItems: "center" }}>
          <Text variant="caption" style={{ color: colors.gold[200], fontWeight: "700", letterSpacing: 1.5 }}>
            PAID FROM WALLET
          </Text>
          <Text variant="title" tone="onPrimary" tabular>
            {wallet.data.balance_display}
          </Text>
        </View>
        <Text variant="caption" style={{ color: colors.primary[100] }}>
          Each delivery is charged at {chargeRuleText(options.data)}, at that day’s prices — about{" "}
          {formatRupees(totalPaise)} for this basket today. Nothing is charged now.
        </Text>
      </View>

      {shortfall > 0 && (
        <View
          style={{
            gap: spacing.md,
            padding: spacing.lg,
            borderRadius: 20,
            borderWidth: 1,
            borderColor: colors.gold[200],
            backgroundColor: colors.gold[50],
          }}
        >
          <Text variant="bodyStrong" style={{ color: colors.gold[700] }}>
            Add at least {formatRupees(shortfall)} so your first delivery isn’t skipped.
          </Text>
          <AddMoney limits={wallet.data.limits} suggestPaise={shortfall} />
        </View>
      )}

      {create.isError && (
        <Text tone="danger" accessibilityRole="alert">
          {create.error.message}
        </Text>
      )}

      <Button
        label={frequency === "once" ? "Schedule this delivery" : "Start repeat order"}
        onPress={() => create.mutate()}
        loading={create.isPending}
        disabled={addressId === null || startDate === "" || invalidPattern}
        block
      />
      <Text variant="caption" tone="faint" center>
        Skip, pause or cancel any time before a delivery is charged.
      </Text>
    </View>
  );
}
