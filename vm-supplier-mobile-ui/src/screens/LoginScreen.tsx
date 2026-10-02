import { useCallback, useRef, useState, type ReactElement } from "react";
import { Linking, View, type TextInput } from "react-native";

import { Button } from "../components/Button";
import { Card } from "../components/Card";
import { Banner, devNetworkHint } from "../components/Feedback";
import { TextField } from "../components/Field";
import { VayalLockup } from "../components/Logo";
import { Screen } from "../components/Screen";
import { Text } from "../components/Text";
import { ApiError } from "../lib/api";
import { useAuth } from "../lib/auth";
import { config } from "../lib/config";
import { spacing } from "../theme/tokens";

export function LoginScreen(): ReactElement {
  const { signIn } = useAuth();

  const [identifier, setIdentifier] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  const passwordRef = useRef<TextInput>(null);

  const submit = useCallback(async () => {
    if (submitting) return;
    setError(null);
    setSubmitting(true);
    try {
      await signIn(identifier.trim(), password);
      // No navigation call: RootNavigator swaps the whole tree once `user` is
      // set, so there is no signed-in screen to push onto a signed-out stack.
    } catch (caught) {
      // The server returns one message for every credential failure, on
      // purpose — do not try to be more specific here.
      const message =
        caught instanceof ApiError
          ? caught.message
          : "Something went wrong. Please try again.";
      // Sign-in is the first request the app makes, so it is where a
      // misconfigured API URL shows up first. Dev builds only.
      const hint = devNetworkHint(caught);
      setError(hint === null ? message : `${message}\n\n${hint}`);
    } finally {
      setSubmitting(false);
    }
  }, [identifier, password, signIn, submitting]);

  return (
    <Screen contentStyle={{ flexGrow: 1, justifyContent: "center", gap: spacing.xl }}>
      <View style={{ alignItems: "center" }}>
        <VayalLockup size="lg" />
      </View>

      <Card>
        <View style={{ gap: spacing.lg }}>
          <View style={{ gap: spacing.xs }}>
            <Text variant="title" tone="strong" accessibilityRole="header">
              Supplier sign in
            </Text>
            <Text tone="muted">Manage your produce, availability and sales.</Text>
          </View>

          <TextField
            label="Email or phone"
            value={identifier}
            onChangeText={setIdentifier}
            placeholder="you@example.com"
            autoCapitalize="none"
            autoComplete="username"
            // "email-address" would deny the number pad to the many suppliers
            // who sign in with a phone number instead.
            keyboardType="default"
            returnKeyType="next"
            onSubmitEditing={() => passwordRef.current?.focus()}
          />

          <TextField
            label="Password"
            inputRef={passwordRef}
            value={password}
            onChangeText={setPassword}
            secureTextEntry
            autoCapitalize="none"
            autoComplete="current-password"
            returnKeyType="go"
            onSubmitEditing={() => void submit()}
          />

          {error !== null && <Banner tone="error" message={error} />}

          <Button
            label={submitting ? "Signing in…" : "Sign in"}
            onPress={() => void submit()}
            loading={submitting}
            disabled={identifier.trim() === "" || password === ""}
            block
          />
        </View>
      </Card>

      <View style={{ alignItems: "center", gap: spacing.xs }}>
        <Text variant="caption" tone="muted">
          Not registered yet? Write to
        </Text>
        <Button
          label={config.supportEmail}
          variant="ghost"
          size="sm"
          onPress={() => void Linking.openURL(`mailto:${config.supportEmail}`)}
          accessibilityHint="Opens your email app"
        />
      </View>
    </Screen>
  );
}
