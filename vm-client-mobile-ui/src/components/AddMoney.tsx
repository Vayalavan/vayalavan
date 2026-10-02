/**
 * Adding money to the wallet — CLAUDE.md §6.7. The phone's counterpart of
 * vm-client-ui's AddMoney: preset amounts, any amount typed, and the Razorpay
 * sheet it opens carried with it, so it drops into the Wallet screen, the
 * checkout and a schedule alike.
 */
import { useState, type ReactElement } from "react";
import { Pressable, TextInput, View } from "react-native";

import { useAuth } from "../lib/auth";
import { TOPUP_COPY, useTopup, type WalletLimits } from "../lib/wallet";
import { formatRupees, rupeesToPaise } from "../lib/walletFormat";
import { colors, spacing, text as textColors, typography } from "../theme/tokens";
import { Button } from "./Button";
import { RazorpayCheckout } from "./RazorpayCheckout";
import { Text } from "./Text";

const PRESETS = [50_000, 100_000, 200_000, 500_000];

export function AddMoney({
  limits,
  suggestPaise,
}: {
  limits: WalletLimits;
  /** Pre-selects an amount: a shortfall, rounded up to a whole ₹100. */
  suggestPaise?: number;
}): ReactElement {
  const { user } = useAuth();
  const topup = useTopup();

  const suggested =
    suggestPaise !== undefined && suggestPaise > 0
      ? Math.min(
          Math.max(Math.ceil(suggestPaise / 10_000) * 10_000, limits.min_topup_paise),
          limits.max_topup_paise,
        )
      : null;
  const presets = Array.from(new Set([...(suggested !== null ? [suggested] : []), ...PRESETS]))
    .filter((p) => p >= limits.min_topup_paise && p <= limits.max_topup_paise)
    .slice(0, 4);

  const [chosen, setChosen] = useState<number | null>(suggested ?? presets[1] ?? presets[0] ?? null);
  const [custom, setCustom] = useState("");

  const typed = custom.trim() !== "";
  const amount = typed ? rupeesToPaise(custom) : chosen;
  const invalid =
    amount === null || amount < limits.min_topup_paise || amount > limits.max_topup_paise;
  const status = TOPUP_COPY[topup.phase];

  return (
    <View style={{ gap: spacing.md }}>
      <View style={{ flexDirection: "row", flexWrap: "wrap", gap: spacing.sm }}>
        {presets.map((preset) => {
          const selected = !typed && chosen === preset;
          return (
            <Pressable
              key={preset}
              onPress={() => {
                setChosen(preset);
                setCustom("");
                topup.reset();
              }}
              accessibilityRole="button"
              accessibilityState={{ selected }}
              accessibilityLabel={`Add ${formatRupees(preset)}${preset === suggested ? ", suggested" : ""}`}
              style={{
                flexBasis: "47%",
                flexGrow: 1,
                minHeight: 48,
                alignItems: "center",
                justifyContent: "center",
                borderRadius: 14,
                borderWidth: 1.5,
                borderColor: selected ? colors.primary[600] : colors.cream[300],
                backgroundColor: selected ? colors.primary[50] : colors.surface.raised,
              }}
            >
              <Text variant="bodyStrong" tone="strong" tabular>
                {formatRupees(preset)}
              </Text>
              {preset === suggested && (
                <Text variant="caption" style={{ color: colors.gold[700], fontSize: 10, fontWeight: "700" }}>
                  SUGGESTED
                </Text>
              )}
            </Pressable>
          );
        })}
      </View>

      <View
        style={{
          flexDirection: "row",
          alignItems: "center",
          borderRadius: 14,
          borderWidth: 1.5,
          borderColor: colors.cream[300],
          backgroundColor: colors.surface.raised,
          paddingHorizontal: spacing.md,
        }}
      >
        <Text tone="faint">₹</Text>
        <TextInput
          value={custom}
          onChangeText={(value) => {
            setCustom(value);
            topup.reset();
          }}
          keyboardType="decimal-pad"
          placeholder={`Other amount (${limits.min_topup_display.replace(".00", "")} – ${limits.max_topup_display.replace(".00", "")})`}
          placeholderTextColor={textColors.faint}
          accessibilityLabel="Other amount in rupees"
          style={{
            flex: 1,
            minHeight: 48,
            paddingHorizontal: spacing.sm,
            fontSize: typography.body.fontSize,
            color: textColors.strong,
          }}
        />
      </View>
      {typed && invalid && (
        <Text variant="caption" tone="danger">
          Enter an amount between {limits.min_topup_display} and {limits.max_topup_display}.
        </Text>
      )}

      <Button
        label={amount !== null && !invalid ? `Add ${formatRupees(amount)}` : "Add money"}
        onPress={() =>
          amount !== null &&
          topup.addMoney(amount, {
            name: user?.name ?? undefined,
            email: user?.email ?? undefined,
            contact: user?.phone ?? undefined,
          })
        }
        loading={topup.busy}
        disabled={invalid}
        block
      />

      {status !== undefined && (
        <Text variant="caption" tone={topup.phase === "done" ? "strong" : "muted"} center accessibilityRole="alert">
          {status}
        </Text>
      )}
      {topup.phase === "error" && topup.message !== null && (
        <Text variant="caption" tone="danger" center accessibilityRole="alert">
          {topup.message}
        </Text>
      )}
      <Text variant="caption" tone="faint" center>
        Paid securely via Razorpay. Your wallet can hold up to {limits.max_balance_display}.
      </Text>

      <RazorpayCheckout request={topup.request} onResult={topup.handleResult} />
    </View>
  );
}
