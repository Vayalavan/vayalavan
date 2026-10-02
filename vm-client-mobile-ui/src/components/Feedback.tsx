/**
 * The states every screen has to render: loading, empty, error, and a banner
 * for the result of something the customer just did.
 *
 * Collected in one file because they are the pieces most often skipped, and
 * skipping them is what leaves someone looking at a blank screen wondering
 * whether their order went through.
 */
import type { ReactElement, ReactNode } from "react";
import { ActivityIndicator, View } from "react-native";

import { ApiError } from "../lib/api";
import { config } from "../lib/config";
import { colors, radius, spacing } from "../theme/tokens";
import { Button } from "./Button";
import { Card } from "./Card";
import { Text } from "./Text";

/**
 * The developer-only explanation for a request that never reached a server.
 *
 * "Could not reach Vayalavan" is the right thing to say to a customer and the wrong
 * thing to say to a developer: a timeout caused by a missing port in
 * MOBILE_API_BASE_URL is indistinguishable from being out of signal. Returns
 * null in production, and for any failure that DID reach the server.
 */
export function devNetworkHint(error: unknown): string | null {
  if (config.appEnv === "production") return null;
  if (!(error instanceof ApiError) || !error.isNetworkError) return null;
  return (
    `Dev: could not reach ${config.apiBaseUrl}\n` +
    `The phone must be able to see that host and port. Check ` +
    `MOBILE_API_BASE_URL in the repo's .env.`
  );
}

export function Loading({ label = "Loading…" }: { label?: string }): ReactElement {
  return (
    <View
      accessibilityRole="progressbar"
      accessibilityLabel={label}
      style={{ padding: spacing.xxl, alignItems: "center", gap: spacing.md }}
    >
      <ActivityIndicator color={colors.primary[600]} />
      <Text tone="muted">{label}</Text>
    </View>
  );
}

export interface EmptyStateProps {
  title: string;
  body?: string;
  action?: ReactNode;
}

export function EmptyState({ title, body, action }: EmptyStateProps): ReactElement {
  return (
    <Card>
      <View style={{ gap: spacing.sm }}>
        <Text variant="heading" tone="strong">
          {title}
        </Text>
        {body !== undefined && <Text tone="muted">{body}</Text>}
        {action !== undefined && <View style={{ marginTop: spacing.sm }}>{action}</View>}
      </View>
    </Card>
  );
}

/**
 * A failed request, rendered so the customer can act on it.
 *
 * A dropped connection gets different words from a rejected request, because
 * the two need different things from the person reading them: one is "try
 * again in a moment", the other is "this will not fix itself". The request id
 * is shown only for server-side failures — it is the one string support can
 * use to find the exact request in the logs.
 */
export function ErrorState({
  error,
  onRetry,
}: {
  error: unknown;
  onRetry?: () => void;
}): ReactElement {
  const apiError = error instanceof ApiError ? error : null;
  const offline = apiError?.isNetworkError ?? false;

  return (
    <Card tone="accent">
      <View style={{ gap: spacing.sm }}>
        <Text variant="heading" tone="strong">
          {offline ? "No connection" : "Something went wrong"}
        </Text>
        <Text tone="body" accessibilityRole="alert">
          {apiError?.message ?? "Please try again."}
        </Text>

        {!offline && apiError?.requestId !== undefined && (
          <Text variant="caption" tone="faint" selectable>
            Reference: {apiError.requestId}
          </Text>
        )}

        {devNetworkHint(error) !== null && (
          <Text variant="caption" tone="faint" selectable>
            {devNetworkHint(error)}
          </Text>
        )}

        {onRetry !== undefined && (
          <View style={{ marginTop: spacing.sm, alignSelf: "flex-start" }}>
            <Button label="Try again" onPress={onRetry} variant="secondary" size="sm" />
          </View>
        )}
      </View>
    </Card>
  );
}

export type BannerTone = "success" | "warning" | "error";

/**
 * The result of the last action: "Address saved", "Removed from your cart".
 *
 * `accessibilityLiveRegion` and `role=alert` are what make this reach someone
 * using TalkBack or VoiceOver — otherwise the message appears silently and
 * only sighted users learn what happened.
 */
export function Banner({
  tone,
  message,
  onDismiss,
}: {
  tone: BannerTone;
  message: string;
  onDismiss?: () => void;
}): ReactElement {
  const styles = {
    success: { bg: colors.primary[50], border: colors.primary[200] },
    warning: { bg: colors.accent[50], border: colors.accent[200] },
    error: { bg: colors.accent[50], border: colors.accent[400] },
  }[tone];

  return (
    <View
      accessibilityRole="alert"
      accessibilityLiveRegion="polite"
      style={{
        flexDirection: "row",
        alignItems: "flex-start",
        gap: spacing.md,
        backgroundColor: styles.bg,
        borderColor: styles.border,
        borderWidth: 1,
        borderRadius: radius.card,
        padding: spacing.md,
      }}
    >
      <Text
        style={{ flex: 1 }}
        tone={tone === "success" ? "strong" : "danger"}
        variant="body"
      >
        {message}
      </Text>
      {onDismiss !== undefined && (
        <Button label="Dismiss" onPress={onDismiss} variant="ghost" size="sm" />
      )}
    </View>
  );
}

/** A small status pill: "Sold out", "Confirmed", "Default". */
export function Badge({
  label,
  tone = "neutral",
}: {
  label: string;
  tone?: "neutral" | "primary" | "accent" | "secondary";
}): ReactElement {
  const styles = {
    neutral: { bg: colors.surface.sunken, fg: colors.primary[900] },
    primary: { bg: colors.primary[100], fg: colors.primary[800] },
    accent: { bg: colors.accent[100], fg: colors.accent[900] },
    secondary: { bg: colors.secondary[100], fg: colors.secondary[800] },
  }[tone];

  return (
    <View
      style={{
        paddingHorizontal: spacing.sm,
        paddingVertical: 2,
        borderRadius: radius.pill,
        backgroundColor: styles.bg,
      }}
    >
      <Text variant="caption" style={{ color: styles.fg, fontWeight: "600" }}>
        {label}
      </Text>
    </View>
  );
}
