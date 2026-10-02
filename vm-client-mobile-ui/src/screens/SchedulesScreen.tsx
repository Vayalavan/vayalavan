/**
 * Scheduled and repeat orders — CLAUDE.md §6.7. The phone's counterpart of
 * vm-client-ui's SchedulesPage: a list, and a detail screen where a date is
 * skipped, a quantity changed, or the whole schedule paused — each allowed
 * until that date's charging time, which the screen states.
 */
import { useState, type ReactElement } from "react";
import { Alert, Pressable, View } from "react-native";
import { useNavigation } from "@react-navigation/native";
import type { NativeStackNavigationProp, NativeStackScreenProps } from "@react-navigation/native-stack";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { AddMoney } from "../components/AddMoney";
import { Button } from "../components/Button";
import { Card } from "../components/Card";
import { EmptyState, ErrorState, Loading } from "../components/Feedback";
import { Screen } from "../components/Screen";
import { SignInPrompt } from "../components/SignInPrompt";
import { Text } from "../components/Text";
import type { ApiError } from "../lib/api";
import { useAuth } from "../lib/auth";
import { api } from "../lib/client";
import { gradedName } from "../lib/grade";
import { usePullToRefresh } from "../lib/usePullToRefresh";
import {
  OCCURRENCE_COPY,
  SCHEDULES_KEY,
  formatChargeAt,
  useWallet,
  type Schedule,
  type ScheduleList,
} from "../lib/wallet";
import { displayDate, formatRupees } from "../lib/walletFormat";
import type { AccountStackParamList } from "../navigation/types";
import { colors, radius, shadow, spacing } from "../theme/tokens";

const STATUS_COLORS: Record<Schedule["status"], { bg: string; fg: string }> = {
  active: { bg: colors.primary[50], fg: colors.primary[700] },
  paused: { bg: colors.gold[50], fg: colors.gold[700] },
  cancelled: { bg: colors.surface.sunken, fg: colors.silver[700] },
  completed: { bg: colors.surface.sunken, fg: colors.silver[700] },
};

function StatusBadge({ status }: { status: Schedule["status"] }): ReactElement {
  const tone = STATUS_COLORS[status];
  return (
    <View style={{ paddingHorizontal: spacing.sm, paddingVertical: 2, borderRadius: radius.pill, backgroundColor: tone.bg }}>
      <Text variant="caption" style={{ color: tone.fg, fontWeight: "700", textTransform: "capitalize" }}>
        {status}
      </Text>
    </View>
  );
}

// ---------------------------------------------------------------------------
// List
// ---------------------------------------------------------------------------

export function SchedulesScreen(): ReactElement {
  const { user } = useAuth();
  const navigation = useNavigation<NativeStackNavigationProp<AccountStackParamList>>();
  const list = useQuery<ScheduleList, ApiError>({
    queryKey: SCHEDULES_KEY,
    queryFn: () => api.get<ScheduleList>("/schedules"),
    enabled: Boolean(user),
  });
  const pull = usePullToRefresh(list.refetch);

  if (!user) {
    return <SignInPrompt title="Sign in to see your schedules" body="Plan deliveries ahead, once or on repeat." />;
  }
  if (list.isPending) return <Loading label="Loading your schedules…" />;
  if (list.isError) {
    return (
      <Screen>
        <ErrorState error={list.error} onRetry={() => void list.refetch()} />
      </Screen>
    );
  }

  if (list.data.schedules.length === 0) {
    return (
      <Screen>
        <EmptyState
          title="Never run out of fruit"
          body="Fill your cart, then choose Schedule / Repeat at checkout. Pick a date — or every day, week or month — and each delivery is paid from your wallet."
        />
      </Screen>
    );
  }

  return (
    <Screen onRefresh={pull.onRefresh} refreshing={pull.refreshing}>
      <Pressable
        onPress={() => navigation.navigate("Wallet")}
        accessibilityRole="button"
        style={{
          flexDirection: "row",
          justifyContent: "space-between",
          alignItems: "center",
          padding: spacing.lg,
          borderRadius: 20,
          backgroundColor: colors.primary[900],
        }}
      >
        <Text variant="label" style={{ color: colors.gold[200] }}>
          WALLET
        </Text>
        <Text variant="title" tone="onPrimary" tabular>
          {list.data.wallet_balance_display} ›
        </Text>
      </Pressable>

      {list.data.schedules.map((schedule) => {
        const short = schedule.status === "active" && schedule.estimate_paise > list.data.wallet_balance_paise;
        return (
          <Pressable
            key={schedule.id}
            onPress={() => navigation.navigate("ScheduleDetail", { scheduleId: schedule.id })}
            accessibilityRole="button"
          >
            <Card>
              <View style={{ gap: spacing.md }}>
                <View style={{ flexDirection: "row", justifyContent: "space-between", alignItems: "flex-start", gap: spacing.sm }}>
                  <View style={{ flex: 1 }}>
                    <Text variant="display" tone="strong" style={{ fontSize: 19, lineHeight: 24 }}>
                      {schedule.summary}
                    </Text>
                    <Text variant="caption" tone="faint">
                      {schedule.items.length} item{schedule.items.length === 1 ? "" : "s"} · to{" "}
                      {schedule.address.recipient_name}, {schedule.address.city}
                    </Text>
                  </View>
                  <StatusBadge status={schedule.status} />
                </View>
                <Text tone="muted" numberOfLines={2}>
                  {schedule.items.map((item) => `${gradedName(item.product_name, item.size_code)} × ${item.qty}`).join(", ")}
                </Text>
                <View style={{ flexDirection: "row", justifyContent: "space-between", borderTopWidth: 1, borderTopColor: colors.cream[200], paddingTop: spacing.md }}>
                  <View>
                    <Text variant="caption" tone="faint">NEXT DELIVERY</Text>
                    <Text variant="bodyStrong" tone="strong">
                      {schedule.status === "active" ? (schedule.next_delivery_display ?? "—") : "—"}
                    </Text>
                  </View>
                  <View style={{ alignItems: "flex-end" }}>
                    <Text variant="caption" tone="faint">ABOUT</Text>
                    <Text variant="bodyStrong" tone="strong" tabular>
                      {schedule.estimate_display}
                    </Text>
                  </View>
                </View>
                {short && (
                  <Text variant="caption" style={{ color: colors.gold[700], fontWeight: "600" }}>
                    Your wallet won’t cover the next delivery. Top up before{" "}
                    {schedule.next_charge_at !== undefined ? formatChargeAt(schedule.next_charge_at) : "it is charged"}.
                  </Text>
                )}
              </View>
            </Card>
          </Pressable>
        );
      })}
    </Screen>
  );
}

// ---------------------------------------------------------------------------
// Detail
// ---------------------------------------------------------------------------

type DetailProps = NativeStackScreenProps<AccountStackParamList, "ScheduleDetail">;

export function ScheduleDetailScreen({ route, navigation }: DetailProps): ReactElement {
  const { scheduleId } = route.params;
  const { user } = useAuth();
  const queryClient = useQueryClient();
  const wallet = useWallet();
  const [draft, setDraft] = useState<Record<string, number> | null>(null);

  const key = ["schedule", scheduleId];
  const detail = useQuery<Schedule, ApiError>({
    queryKey: key,
    queryFn: () => api.get<Schedule>(`/schedules/${scheduleId}`),
    enabled: Boolean(user),
  });
  const pull = usePullToRefresh(detail.refetch);

  const refresh = (updated?: Schedule): void => {
    if (updated !== undefined) queryClient.setQueryData(key, updated);
    void queryClient.invalidateQueries({ queryKey: SCHEDULES_KEY });
  };

  const action = useMutation<Schedule, ApiError, "pause" | "resume">({
    mutationFn: (verb) => api.post<Schedule>(`/schedules/${scheduleId}/${verb}`, {}),
    onSuccess: refresh,
  });
  const cancel = useMutation<unknown, ApiError>({
    mutationFn: () => api.post(`/schedules/${scheduleId}/cancel`, {}),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: SCHEDULES_KEY });
      navigation.goBack();
    },
  });
  const skip = useMutation<Schedule, ApiError, { date: string; skipped: boolean }>({
    mutationFn: ({ date, skipped }) =>
      skipped
        ? api.delete<Schedule>(`/schedules/${scheduleId}/skip/${date}`)
        : api.post<Schedule>(`/schedules/${scheduleId}/skip`, { delivery_date: date }),
    onSuccess: refresh,
  });
  const save = useMutation<Schedule, ApiError, Record<string, number>>({
    mutationFn: (quantities) =>
      api.patch<Schedule>(`/schedules/${scheduleId}`, {
        items: Object.entries(quantities).map(([product_unit_id, qty]) => ({ product_unit_id, qty })),
      }),
    onSuccess: (updated) => {
      setDraft(null);
      refresh(updated);
    },
  });

  if (detail.isPending) return <Loading label="Loading schedule…" />;
  if (detail.isError) {
    return (
      <Screen>
        <ErrorState error={detail.error} onRetry={() => void detail.refetch()} />
      </Screen>
    );
  }

  const schedule = detail.data;
  const open = schedule.status === "active" || schedule.status === "paused";
  const balance = wallet.data?.balance_paise ?? 0;
  const short = schedule.status === "active" && schedule.estimate_paise > balance;
  const quantities = draft ?? Object.fromEntries(schedule.items.map((i) => [i.product_unit_id, i.qty]));
  const error = action.error ?? skip.error ?? save.error ?? cancel.error;

  const confirmCancel = (): void =>
    Alert.alert(
      "Cancel this schedule?",
      "No further deliveries will be charged. Orders already placed are not affected.",
      [
        { text: "Keep it", style: "cancel" },
        { text: "Cancel schedule", style: "destructive", onPress: () => cancel.mutate() },
      ],
    );

  return (
    <Screen onRefresh={pull.onRefresh} refreshing={pull.refreshing}>
      <Card>
        <View style={{ gap: spacing.sm }}>
          <View style={{ flexDirection: "row", alignItems: "center", gap: spacing.sm }}>
            <StatusBadge status={schedule.status} />
            <Text variant="caption" tone="faint">
              From {displayDate(schedule.start_date)}
              {schedule.end_date !== undefined ? ` to ${displayDate(schedule.end_date)}` : ""}
            </Text>
          </View>
          <Text variant="display" tone="strong" accessibilityRole="header">
            {schedule.summary}
          </Text>
          <Text tone="muted">
            To {schedule.address.recipient_name}, {schedule.address.line1}, {schedule.address.city}{" "}
            {schedule.address.pincode}
          </Text>
          {schedule.status === "paused" && schedule.low_balance_skips > 0 && (
            <Text variant="caption" style={{ color: colors.gold[700] }}>
              Paused after deliveries were skipped for a low wallet balance. Top up, then resume.
            </Text>
          )}
          {open && (
            <View style={{ flexDirection: "row", gap: spacing.sm, marginTop: spacing.xs }}>
              <View style={{ flex: 1 }}>
                <Button
                  label={schedule.status === "active" ? "Pause" : "Resume"}
                  variant="secondary"
                  size="sm"
                  onPress={() => action.mutate(schedule.status === "active" ? "pause" : "resume")}
                  loading={action.isPending}
                  block
                />
              </View>
              <View style={{ flex: 1 }}>
                <Button label="Cancel" variant="ghost" size="sm" onPress={confirmCancel} block />
              </View>
            </View>
          )}
          {error !== null && (
            <Text variant="caption" tone="danger" accessibilityRole="alert">
              {error.message}
            </Text>
          )}
        </View>
      </Card>

      {open && wallet.data !== undefined && (
        <View style={{ borderRadius: 20, padding: spacing.lg, gap: spacing.xs, backgroundColor: colors.primary[900], ...shadow.card }}>
          <Text variant="caption" style={{ color: colors.gold[200], fontWeight: "700", letterSpacing: 1.5 }}>
            WALLET
          </Text>
          <Text variant="display" tone="onPrimary" tabular>
            {wallet.data.balance_display}
          </Text>
          <Text variant="caption" style={{ color: colors.primary[200] }}>
            {short
              ? `Add ${formatRupees(Math.ceil((schedule.estimate_paise - balance) / 100) * 100)} or more to cover the next delivery.`
              : "Covers the next delivery."}
          </Text>
        </View>
      )}
      {short && wallet.data !== undefined && (
        <Card>
          <AddMoney limits={wallet.data.limits} suggestPaise={schedule.estimate_paise - balance} />
        </Card>
      )}

      {open && schedule.upcoming !== undefined && schedule.upcoming.length > 0 && (
        <Card>
          <View style={{ gap: spacing.sm }}>
            <Text variant="heading" tone="strong">
              Coming up
            </Text>
            <Text variant="caption" tone="faint">
              Skip a date any time before it is charged.
            </Text>
            {schedule.upcoming.map((date) => (
              <View
                key={date.date}
                style={{
                  flexDirection: "row",
                  alignItems: "center",
                  justifyContent: "space-between",
                  gap: spacing.md,
                  padding: spacing.md,
                  borderRadius: 16,
                  borderWidth: 1,
                  borderStyle: date.skipped ? "dashed" : "solid",
                  borderColor: colors.cream[300],
                  backgroundColor: date.skipped ? colors.cream[50] : colors.surface.raised,
                }}
              >
                <View style={{ flex: 1 }}>
                  <Text
                    variant="bodyStrong"
                    tone={date.skipped ? "faint" : "strong"}
                    style={date.skipped ? { textDecorationLine: "line-through" } : undefined}
                  >
                    {date.display}
                  </Text>
                  <Text variant="caption" tone="faint">
                    {date.locked ? "Being packed — can’t change now" : `Charged ${formatChargeAt(date.charge_at)}`}
                  </Text>
                </View>
                {!date.locked && (
                  <Button
                    label={date.skipped ? "Undo skip" : "Skip"}
                    variant={date.skipped ? "primary" : "secondary"}
                    size="sm"
                    onPress={() => skip.mutate({ date: date.date, skipped: date.skipped })}
                    disabled={skip.isPending}
                  />
                )}
              </View>
            ))}
          </View>
        </Card>
      )}

      <Card>
        <View style={{ gap: spacing.sm }}>
          <View style={{ flexDirection: "row", justifyContent: "space-between" }}>
            <Text variant="heading" tone="strong">
              Each delivery
            </Text>
            <Text tone="muted">About {schedule.estimate_display}</Text>
          </View>
          {schedule.items.map((item) => {
            const qty = quantities[item.product_unit_id] ?? item.qty;
            return (
              <View
                key={item.product_unit_id}
                style={{ flexDirection: "row", alignItems: "center", gap: spacing.md, paddingVertical: spacing.xs }}
              >
                <View style={{ flex: 1 }}>
                  <Text
                    variant="bodyStrong"
                    tone={qty === 0 ? "faint" : "strong"}
                    style={qty === 0 ? { textDecorationLine: "line-through" } : undefined}
                  >
                    {gradedName(item.product_name, item.size_code)}
                  </Text>
                  <Text variant="caption" tone="faint">
                    {item.unit_label}
                    {item.unit_price_display !== undefined ? ` · ${item.unit_price_display} today` : ""}
                    {!item.available_today ? " · not in stock today" : ""}
                  </Text>
                </View>
                {open ? (
                  <View style={{ flexDirection: "row", alignItems: "center", borderWidth: 1, borderColor: colors.cream[300], borderRadius: radius.pill }}>
                    <Pressable
                      onPress={() => setDraft({ ...quantities, [item.product_unit_id]: Math.max(0, qty - 1) })}
                      accessibilityLabel={`Fewer ${item.product_name}`}
                      disabled={qty === 0}
                      style={{ width: 40, height: 40, alignItems: "center", justifyContent: "center" }}
                    >
                      <Text variant="title" tone="strong">−</Text>
                    </Pressable>
                    <Text variant="bodyStrong" tone="strong" tabular style={{ minWidth: 20, textAlign: "center" }}>
                      {qty}
                    </Text>
                    <Pressable
                      onPress={() => setDraft({ ...quantities, [item.product_unit_id]: Math.min(99, qty + 1) })}
                      accessibilityLabel={`More ${item.product_name}`}
                      style={{ width: 40, height: 40, alignItems: "center", justifyContent: "center" }}
                    >
                      <Text variant="title" tone="strong">+</Text>
                    </Pressable>
                  </View>
                ) : (
                  <Text variant="bodyStrong">× {qty}</Text>
                )}
              </View>
            );
          })}
          {draft !== null && (
            <View style={{ flexDirection: "row", gap: spacing.sm }}>
              <View style={{ flex: 1 }}>
                <Button label="Discard" variant="ghost" size="sm" onPress={() => setDraft(null)} block />
              </View>
              <View style={{ flex: 1 }}>
                <Button label="Save changes" size="sm" onPress={() => save.mutate(quantities)} loading={save.isPending} block />
              </View>
            </View>
          )}
          <Text variant="caption" tone="faint">
            Each delivery is charged at that day’s prices, for what is in stock. Anything out of stock is left out and not charged.
          </Text>
        </View>
      </Card>

      {schedule.history !== undefined && schedule.history.length > 0 && (
        <Card>
          <View style={{ gap: spacing.sm }}>
            <Text variant="heading" tone="strong">
              Past deliveries
            </Text>
            {schedule.history.map((entry) => (
              <View key={entry.date} style={{ gap: 2, paddingVertical: spacing.xs, borderTopWidth: 1, borderTopColor: colors.cream[200] }}>
                <View style={{ flexDirection: "row", justifyContent: "space-between", gap: spacing.sm }}>
                  <Text variant="bodyStrong" tone="strong">
                    {entry.display}
                  </Text>
                  <Text variant="caption" tone="muted">
                    {entry.order_number !== undefined
                      ? `${entry.order_number} · ${entry.total_display ?? ""}`
                      : OCCURRENCE_COPY[entry.status]}
                  </Text>
                </View>
                {entry.note !== undefined && (
                  <Text variant="caption" style={{ color: colors.gold[700] }}>
                    {entry.note}
                  </Text>
                )}
              </View>
            ))}
          </View>
        </Card>
      )}
    </Screen>
  );
}
