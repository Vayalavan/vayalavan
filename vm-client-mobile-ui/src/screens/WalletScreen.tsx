/**
 * The wallet — CLAUDE.md §6.7. The phone's counterpart of vm-client-ui's
 * WalletPage: the balance, a way to add to it, and every movement of it.
 */
import type { ReactElement } from "react";
import { Pressable, View } from "react-native";
import { useNavigation } from "@react-navigation/native";
import type { NativeStackNavigationProp } from "@react-navigation/native-stack";

import { AddMoney } from "../components/AddMoney";
import { Card } from "../components/Card";
import { ErrorState, Loading } from "../components/Feedback";
import { Screen } from "../components/Screen";
import { SignInPrompt } from "../components/SignInPrompt";
import { Text } from "../components/Text";
import { useAuth } from "../lib/auth";
import { formatISTDateTime } from "../lib/datetime";
import { usePullToRefresh } from "../lib/usePullToRefresh";
import { useWallet, type WalletTransaction } from "../lib/wallet";
import type { AccountStackParamList } from "../navigation/types";
import { colors, shadow, spacing } from "../theme/tokens";

export function WalletScreen(): ReactElement {
  const { user } = useAuth();
  const wallet = useWallet();
  const pull = usePullToRefresh(wallet.refetch);
  const navigation = useNavigation<NativeStackNavigationProp<AccountStackParamList>>();

  if (!user) {
    return <SignInPrompt title="Sign in to use your wallet" body="Your wallet pays for scheduled deliveries." />;
  }
  if (wallet.isPending) return <Loading label="Loading your wallet…" />;
  if (wallet.isError) {
    return (
      <Screen>
        <ErrorState error={wallet.error} onRetry={() => void wallet.refetch()} />
      </Screen>
    );
  }

  const data = wallet.data;

  return (
    <Screen onRefresh={pull.onRefresh} refreshing={pull.refreshing}>
      {/* The balance, as the screen's hero. */}
      <View
        style={{
          borderRadius: 24,
          padding: spacing.xl,
          gap: spacing.xs,
          backgroundColor: colors.primary[900],
          ...shadow.lift,
        }}
      >
        <Text variant="caption" style={{ color: colors.gold[200], fontWeight: "700", letterSpacing: 2 }}>
          VAYALAVAN WALLET
        </Text>
        <Text variant="display" tone="onPrimary" tabular style={{ fontSize: 38, lineHeight: 46 }}>
          {data.balance_display}
        </Text>
        <Text variant="caption" style={{ color: colors.primary[200] }}>
          Available balance
        </Text>
        <Pressable
          onPress={() => navigation.navigate("Schedules")}
          accessibilityRole="button"
          style={{
            alignSelf: "flex-start",
            marginTop: spacing.md,
            paddingHorizontal: spacing.lg,
            paddingVertical: spacing.sm,
            borderRadius: 999,
            borderWidth: 1,
            borderColor: colors.primary[700],
            backgroundColor: colors.primary[800],
          }}
        >
          <Text variant="label" tone="onPrimary">
            Your scheduled orders ›
          </Text>
        </Pressable>
      </View>

      <Card>
        <View style={{ gap: spacing.md }}>
          <View style={{ gap: spacing.xs }}>
            <Text variant="heading" tone="strong">
              Add money
            </Text>
            <Text tone="muted">Top up once, and your scheduled deliveries pay for themselves.</Text>
          </View>
          <AddMoney limits={data.limits} />
        </View>
      </Card>

      <Card>
        <View style={{ gap: spacing.sm }}>
          <Text variant="heading" tone="strong">
            Activity
          </Text>
          {data.transactions.length === 0 ? (
            <Text tone="muted">
              No activity yet. Money you add, and deliveries paid from the wallet, appear here.
            </Text>
          ) : (
            data.transactions.map((entry) => <TransactionRow key={entry.id} entry={entry} />)
          )}
        </View>
      </Card>

      <View
        style={{
          gap: spacing.sm,
          padding: spacing.lg,
          borderRadius: 20,
          borderWidth: 1,
          borderColor: colors.cream[300],
          backgroundColor: colors.cream[50],
        }}
      >
        <Text variant="caption" style={{ color: colors.primary[700], fontWeight: "700", letterSpacing: 1.5 }}>
          HOW THE WALLET WORKS
        </Text>
        <Text tone="body">
          <Text variant="bodyStrong" tone="strong">Charged per delivery. </Text>
          Each scheduled delivery is paid on the afternoon two days before it arrives, at that day’s prices.
        </Text>
        <Text tone="body">
          <Text variant="bodyStrong" tone="strong">Only what we send. </Text>
          If something is out of stock, you are charged only for what is packed.
        </Text>
        <Text tone="body">
          <Text variant="bodyStrong" tone="strong">Your money stays yours. </Text>
          Unused balance can be refunded to your original payment method — just write to us.
        </Text>
      </View>
    </Screen>
  );
}

function TransactionRow({ entry }: { entry: WalletTransaction }): ReactElement {
  const credit = entry.amount_paise > 0;
  return (
    <View
      style={{
        flexDirection: "row",
        alignItems: "center",
        gap: spacing.md,
        paddingVertical: spacing.sm,
        borderTopWidth: 1,
        borderTopColor: colors.cream[200],
      }}
    >
      <View
        style={{
          width: 36,
          height: 36,
          borderRadius: 18,
          alignItems: "center",
          justifyContent: "center",
          backgroundColor: credit ? colors.primary[50] : colors.cream[200],
        }}
      >
        <Text variant="bodyStrong" style={{ color: credit ? colors.primary[700] : colors.secondary[700] }}>
          {credit ? "+" : "−"}
        </Text>
      </View>
      <View style={{ flex: 1 }}>
        <Text variant="bodyStrong" tone="strong" numberOfLines={1}>
          {entry.label}
        </Text>
        <Text variant="caption" tone="faint">
          {formatISTDateTime(entry.created_at)} · Balance {entry.balance_after_display}
        </Text>
      </View>
      <Text variant="bodyStrong" tabular style={{ color: credit ? colors.primary[700] : colors.primary[900] }}>
        {credit ? "+" : ""}
        {entry.amount_display}
      </Text>
    </View>
  );
}
