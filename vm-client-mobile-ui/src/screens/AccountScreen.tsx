/**
 * Account: who is signed in, where we deliver, how to get help, and sign out.
 *
 * The web storefront spreads this across a header menu, a profile page and a
 * footer. None of those exist on a phone, so they become one tab. The build
 * details at the bottom are here for one reason: when someone rings up about a
 * problem, "which version" is the first question, and asking them to find it
 * in Settings never works.
 */
import { useCallback, type ReactElement } from "react";
import { Alert, Linking, Pressable, View } from "react-native";
import { useNavigation } from "@react-navigation/native";
import type { NativeStackNavigationProp } from "@react-navigation/native-stack";
import { useQuery } from "@tanstack/react-query";

import { Button } from "../components/Button";
import { Card } from "../components/Card";
import { VayalLockup } from "../components/Logo";
import { Screen } from "../components/Screen";
import { SignInPrompt } from "../components/SignInPrompt";
import { Text } from "../components/Text";
import type { ApiError } from "../lib/api";
import { useAuth } from "../lib/auth";
import { api } from "../lib/client";
import { config } from "../lib/config";
import { usePullToRefresh } from "../lib/usePullToRefresh";
import type { CustomerUser } from "../lib/types";
import type { AccountStackParamList } from "../navigation/types";
import { colors, spacing } from "../theme/tokens";

/** The policy pages, on the web storefront. Order matches its footer. */
const POLICIES: ReadonlyArray<readonly [string, string]> = [
  ["/about", "About Vayalavan"],
  ["/terms", "Terms"],
  ["/privacy", "Privacy"],
  ["/shipping", "Shipping & delivery"],
  ["/refunds", "Refunds & cancellation"],
];

export function AccountScreen(): ReactElement {
  const { user, signOut } = useAuth();
  const navigation = useNavigation<NativeStackNavigationProp<AccountStackParamList>>();

  // The gateway wraps the identity in { user: … }; unwrapped here so the rest
  // of the screen reads the fields directly.
  const me = useQuery<CustomerUser, ApiError>({
    queryKey: ["me"],
    queryFn: async () => (await api.get<{ user: CustomerUser }>("/me")).user,
    enabled: Boolean(user),
  });

  const pull = usePullToRefresh(me.refetch);

  const confirmSignOut = useCallback(() => {
    Alert.alert(
      "Sign out?",
      "Your cart stays on your account — you will find it again when you sign back in.",
      [
        { text: "Cancel", style: "cancel" },
        { text: "Sign out", style: "destructive", onPress: () => void signOut() },
      ],
    );
  }, [signOut]);

  const contactSupport = useCallback(() => {
    const subject = encodeURIComponent("Vayalavan app — help");
    // Pre-filling the identifying details saves a round trip: support's first
    // reply is otherwise always "which account, and which version?".
    const body = encodeURIComponent(
      `\n\n—\nAccount: ${user?.email ?? user?.phone ?? "unknown"}\n` +
        `App version: ${config.appVersion}\n`,
    );
    void Linking.openURL(`mailto:${config.supportEmail}?subject=${subject}&body=${body}`);
  }, [user]);

  if (!user) {
    return (
      <SignInPrompt
        title="Sign in to Vayalavan"
        body="You can browse today’s produce without an account. Ordering needs one, so we know where to deliver."
      />
    );
  }

  const profile = me.data ?? user;

  return (
    <Screen onRefresh={pull.onRefresh} refreshing={pull.refreshing}>
      <Card>
        <View style={{ flexDirection: "row", alignItems: "center", gap: spacing.lg }}>
          <VayalLockup size="sm" decorative />
          <View style={{ flex: 1, gap: spacing.xs }}>
            <Text variant="heading" tone="strong">
              {profile.name ?? profile.email ?? profile.phone ?? "Your account"}
            </Text>
            {profile.email !== null && profile.email !== undefined && (
              <Text variant="caption" tone="muted">
                {profile.email}
              </Text>
            )}
            {profile.phone !== null && profile.phone !== undefined && (
              <Text variant="caption" tone="muted">
                {profile.phone}
              </Text>
            )}
          </View>
        </View>
      </Card>

      {/* Wallet and schedules, together: the wallet exists to pay for them. */}
      <View style={{ flexDirection: "row", gap: spacing.md }}>
        {(
          [
            ["Wallet", "Balance & top-ups", "Wallet"],
            ["Scheduled", "Once or on repeat", "Schedules"],
          ] as const
        ).map(([title, hint, route]) => (
          <Pressable
            key={route}
            onPress={() => navigation.navigate(route)}
            accessibilityRole="button"
            accessibilityLabel={`${title}: ${hint}`}
            style={{
              flex: 1,
              gap: spacing.xs,
              padding: spacing.lg,
              borderRadius: 20,
              backgroundColor: route === "Wallet" ? colors.primary[900] : colors.surface.raised,
              borderWidth: route === "Wallet" ? 0 : 1,
              borderColor: colors.cream[300],
            }}
          >
            <Text variant="heading" tone={route === "Wallet" ? "onPrimary" : "strong"}>
              {title}
            </Text>
            <Text
              variant="caption"
              style={{ color: route === "Wallet" ? colors.gold[200] : colors.primary[700] }}
            >
              {hint} ›
            </Text>
          </Pressable>
        ))}
      </View>

      <Card>
        <View style={{ gap: spacing.md }}>
          <Text variant="heading" tone="strong">
            Delivery addresses
          </Text>
          <Text tone="muted">
            Where we send your produce. Saving one makes checkout a single tap.
          </Text>
          <Button
            label="Manage addresses"
            variant="secondary"
            onPress={() => navigation.navigate("Addresses")}
            block
          />
        </View>
      </Card>

      {/* Honest about scope rather than offering a control that does nothing:
          changing an email or phone is an identity change that needs
          verification, and that flow does not exist yet. */}
      <Card>
        <View style={{ gap: spacing.md }}>
          <Text variant="heading" tone="strong">
            Need a hand?
          </Text>
          <Text tone="muted">
            Write to us and we will get back to you. To change the email or phone
            on your account, write to us as well — they identify your account, so
            we verify the change rather than accept it silently.
          </Text>
          <Button
            label={`Email ${config.supportEmail}`}
            onPress={contactSupport}
            variant="secondary"
            block
            accessibilityHint="Opens your email app with your account details filled in"
          />
        </View>
      </Card>

      {/* Links out rather than reproducing the policy text. Two copies of the
          same terms drift, and the ones a customer agrees to when they pay are
          the published ones. Hidden entirely when the build has no storefront
          URL — a dead link is worse than no link. */}
      {config.storefrontUrl !== "" && (
        <Card>
          <View style={{ gap: spacing.sm }}>
            <Text variant="heading" tone="strong">
              Policies
            </Text>
            {POLICIES.map(([path, label]) => (
              <Button
                key={path}
                label={label}
                variant="ghost"
                size="sm"
                onPress={() =>
                  void Linking.openURL(
                    `${config.storefrontUrl.replace(/\/+$/, "")}${path}`,
                  )
                }
                accessibilityHint="Opens in your browser"
                style={{ alignSelf: "flex-start" }}
              />
            ))}
          </View>
        </Card>
      )}

      <Card>
        <View style={{ gap: spacing.md }}>
          <Text variant="heading" tone="strong">
            About this app
          </Text>
          <Detail label="Version" value={config.appVersion} />
          <Detail label="Environment" value={config.appEnv} />
          <Detail label="Server" value={config.apiBaseUrl} />
        </View>
      </Card>

      <Button label="Sign out" onPress={confirmSignOut} variant="danger" block />
    </Screen>
  );
}

function Detail({ label, value }: { label: string; value: string }): ReactElement {
  return (
    <View
      style={{
        flexDirection: "row",
        justifyContent: "space-between",
        gap: spacing.md,
        borderTopWidth: 1,
        borderTopColor: colors.surface.border,
        paddingTop: spacing.sm,
      }}
    >
      <Text variant="caption" tone="muted">
        {label}
      </Text>
      {/* Selectable so someone on a support call can copy it out rather than
          reading a URL down the phone. */}
      <Text variant="caption" tone="body" selectable style={{ flex: 1 }}>
        {value}
      </Text>
    </View>
  );
}
