/**
 * Account: who is signed in, how to get help, and sign out.
 *
 * The web app has no screen like this — sign-out is a button in the header and
 * the support address is in the footer of every page. Neither has anywhere to
 * live on a phone, so they get a tab. The build details underneath are here for
 * one reason: when a supplier rings up about a problem, "which version" is the
 * first question, and asking them to find it in Settings never works.
 */
import { useCallback, type ReactElement } from "react";
import { Alert, Linking, View } from "react-native";

import { Button } from "../components/Button";
import { Card } from "../components/Card";
import { Badge } from "../components/Feedback";
import { VayalLockup } from "../components/Logo";
import { PageHeading, Screen } from "../components/Screen";
import { Text } from "../components/Text";
import { useAuth } from "../lib/auth";
import { config } from "../lib/config";
import { colors, spacing } from "../theme/tokens";

export function AccountScreen(): ReactElement {
  const { user, signOut } = useAuth();

  const confirmSignOut = useCallback(() => {
    Alert.alert(
      "Sign out?",
      "You will need your email or phone and password to sign back in.",
      [
        { text: "Cancel", style: "cancel" },
        { text: "Sign out", style: "destructive", onPress: () => void signOut() },
      ],
    );
  }, [signOut]);

  const contactSupport = useCallback(() => {
    const subject = encodeURIComponent("Vayalavan Supplier app — help");
    // Pre-filling the identifying details saves a round trip: support's first
    // reply is otherwise always "which account, and which version?".
    const body = encodeURIComponent(
      `\n\n—\nAccount: ${user?.email ?? user?.phone ?? "unknown"}\n` +
        `App version: ${config.appVersion}\n`,
    );
    void Linking.openURL(`mailto:${config.supportEmail}?subject=${subject}&body=${body}`);
  }, [user]);

  return (
    <Screen>
      <PageHeading title="Account" />

      <Card>
        <View style={{ gap: spacing.md }}>
          <View style={{ flexDirection: "row", alignItems: "center", gap: spacing.lg }}>
            <VayalLockup size="sm" decorative />
            <View style={{ flex: 1, gap: spacing.xs }}>
              <Text variant="heading" tone="strong">
                {user?.name ?? user?.email ?? user?.phone ?? "Supplier"}
              </Text>
              {user?.email !== null && user?.email !== undefined && (
                <Text variant="caption" tone="muted">
                  {user.email}
                </Text>
              )}
              {user?.phone !== null && user?.phone !== undefined && (
                <Text variant="caption" tone="muted">
                  {user.phone}
                </Text>
              )}
            </View>
          </View>

          {user?.status !== undefined && user.status !== "active" && (
            // A pending or suspended supplier can sign in but cannot sell.
            // Saying so here beats leaving them to work it out from an empty
            // availability sheet.
            <View style={{ flexDirection: "row", alignItems: "center", gap: spacing.sm }}>
              <Badge label={user.status} tone="accent" />
              <Text variant="caption" tone="muted" style={{ flex: 1 }}>
                {user.status === "pending"
                  ? "Your account is waiting for approval. You can set up produce now; it goes on sale once we approve you."
                  : "Your account is suspended. Write to us and we will sort it out."}
              </Text>
            </View>
          )}
        </View>
      </Card>

      <Card>
        <View style={{ gap: spacing.md }}>
          <Text variant="heading" tone="strong">
            Need a hand?
          </Text>
          <Text tone="muted">
            Write to us and we will get back to you. Include what you were doing when
            something went wrong.
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
      {/* Selectable so a supplier on a support call can copy it out rather
          than reading a URL down the phone. */}
      <Text variant="caption" tone="body" selectable style={{ flex: 1 }}>
        {value}
      </Text>
    </View>
  );
}
