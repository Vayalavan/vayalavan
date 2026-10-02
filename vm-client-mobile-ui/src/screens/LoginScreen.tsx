/**
 * Sign in and create an account, on one screen.
 *
 * A single screen with a toggle rather than two, for the same reason the web
 * storefront does it: someone who lands here has one goal — get into the shop
 * — and making them find a second screen to do the other thing is friction at
 * the exact moment they are least patient.
 *
 * Presented as a modal (see RootNavigator). Dismissing it returns the customer
 * to whatever they were doing, with their catalogue and scroll position
 * intact.
 */
import { useCallback, useRef, useState, type ReactElement } from "react";
import { Linking, View, type TextInput } from "react-native";
import type { NativeStackScreenProps } from "@react-navigation/native-stack";

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
import type { RootStackParamList } from "../navigation/types";
import { colors, radius, spacing } from "../theme/tokens";

type Mode = "signin" | "register";

/** Field-level errors returned by the gateway's zod layer. */
type FieldErrors = Record<string, string>;

type Props = NativeStackScreenProps<RootStackParamList, "Login">;

export function LoginScreen({ navigation }: Props): ReactElement {
  const { signIn, register } = useAuth();

  const [mode, setMode] = useState<Mode>("signin");
  const [identifier, setIdentifier] = useState("");
  const [name, setName] = useState("");
  const [phone, setPhone] = useState("");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");

  const [fieldErrors, setFieldErrors] = useState<FieldErrors>({});
  const [formError, setFormError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  const phoneRef = useRef<TextInput>(null);
  const emailRef = useRef<TextInput>(null);
  const passwordRef = useRef<TextInput>(null);

  const registering = mode === "register";

  const submit = useCallback(async () => {
    if (submitting) return;
    setFieldErrors({});
    setFormError(null);
    setSubmitting(true);

    try {
      if (registering) {
        await register({
          name: name.trim(),
          phone: phone.trim(),
          email: email.trim(),
          password,
        });
      } else {
        await signIn(identifier.trim(), password);
      }
      // No further navigation: this is a modal over whatever the customer was
      // doing, and closing it puts them back there — usually with an item they
      // were about to add still on screen.
      navigation.goBack();
    } catch (caught) {
      if (caught instanceof ApiError) {
        const details = caught.details as FieldErrors | undefined;
        // Field messages are shown against the box that caused them; a
        // duplicate banner saying the same thing is noise.
        if (details && typeof details === "object" && Object.keys(details).length > 0) {
          setFieldErrors(details);
        } else {
          // Sign-in is often the first request the app makes, so it is where a
          // misconfigured API URL shows up first. Dev builds only.
          const hint = devNetworkHint(caught);
          setFormError(hint === null ? caught.message : `${caught.message}\n\n${hint}`);
        }
      } else {
        setFormError("Something went wrong. Please try again.");
      }
    } finally {
      setSubmitting(false);
    }
  }, [
    email,
    identifier,
    name,
    navigation,
    password,
    phone,
    register,
    registering,
    signIn,
    submitting,
  ]);

  const canSubmit = registering
    ? name.trim() !== "" && phone.trim() !== "" && email.trim() !== "" && password !== ""
    : identifier.trim() !== "" && password !== "";

  return (
    <Screen contentStyle={{ flexGrow: 1, justifyContent: "center", gap: spacing.xl }}>
      <View style={{ alignItems: "center" }}>
        <VayalLockup size="lg" />
      </View>

      <Card>
        <View style={{ gap: spacing.lg }}>
          {/* Exactly one of the two is active, which is what aria/selected
              means here — the same treatment as the web storefront's tabs. */}
          <View
            style={{
              flexDirection: "row",
              gap: spacing.xs,
              backgroundColor: colors.surface.sunken,
              borderRadius: radius.card,
              padding: spacing.xs,
            }}
          >
            {(
              [
                ["signin", "Sign in"],
                ["register", "Create account"],
              ] as const
            ).map(([value, label]) => (
              <Button
                key={value}
                label={label}
                size="sm"
                variant={mode === value ? "secondary" : "ghost"}
                onPress={() => {
                  setMode(value);
                  setFieldErrors({});
                  setFormError(null);
                }}
                style={{ flex: 1 }}
              />
            ))}
          </View>

          {registering ? (
            <>
              <TextField
                label="Your name"
                value={name}
                onChangeText={setName}
                autoCapitalize="words"
                error={fieldErrors["name"]}
                returnKeyType="next"
                onSubmitEditing={() => phoneRef.current?.focus()}
              />
              <TextField
                label="Phone"
                inputRef={phoneRef}
                value={phone}
                onChangeText={setPhone}
                placeholder="98765 43210"
                autoCapitalize="none"
                autoComplete="tel"
                keyboardType="phone-pad"
                error={fieldErrors["phone"]}
                returnKeyType="next"
                onSubmitEditing={() => emailRef.current?.focus()}
              />
              <TextField
                label="Email"
                inputRef={emailRef}
                value={email}
                onChangeText={setEmail}
                autoCapitalize="none"
                autoComplete="email"
                keyboardType="email-address"
                error={fieldErrors["email"]}
                returnKeyType="next"
                onSubmitEditing={() => passwordRef.current?.focus()}
              />
            </>
          ) : (
            <TextField
              label="Email or phone"
              value={identifier}
              onChangeText={setIdentifier}
              placeholder="you@example.com"
              autoCapitalize="none"
              autoComplete="username"
              // "email-address" would deny the number pad to everyone who
              // signs in with the phone number they registered with.
              keyboardType="default"
              error={fieldErrors["identifier"]}
              returnKeyType="next"
              onSubmitEditing={() => passwordRef.current?.focus()}
            />
          )}

          <TextField
            label="Password"
            inputRef={passwordRef}
            value={password}
            onChangeText={setPassword}
            secureTextEntry
            autoCapitalize="none"
            autoComplete={registering ? "off" : "current-password"}
            error={fieldErrors["password"]}
            {...(registering && { hint: "At least 8 characters." })}
            returnKeyType="go"
            onSubmitEditing={() => void submit()}
          />

          {formError !== null && <Banner tone="error" message={formError} />}

          <Button
            label={
              submitting
                ? "Please wait…"
                : registering
                  ? "Create account"
                  : "Sign in"
            }
            onPress={() => void submit()}
            loading={submitting}
            disabled={!canSubmit}
            block
          />
        </View>
      </Card>

      <View style={{ alignItems: "center", gap: spacing.xs }}>
        <Text variant="caption" tone="muted">
          Need help? Write to
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
