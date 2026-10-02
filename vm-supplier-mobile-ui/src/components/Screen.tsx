/**
 * Screen scaffolding: safe-area insets, the page background, pull-to-refresh,
 * and keyboard avoidance.
 *
 * Every screen uses one of these two rather than rolling its own ScrollView,
 * because the details they handle are the ones that only show up on a real
 * handset: content under a notch, a Save button hidden behind the keyboard,
 * and no way to force a refresh when a value looks stale.
 */
import type { ReactElement, ReactNode } from "react";
import {
  KeyboardAvoidingView,
  Platform,
  RefreshControl,
  ScrollView,
  View,
  type StyleProp,
  type ViewStyle,
} from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";

import { colors, spacing } from "../theme/tokens";
import { Text } from "./Text";

export interface ScreenProps {
  children: ReactNode;
  /** Wires pull-to-refresh. Omit on screens with nothing to refetch. */
  onRefresh?: () => void;
  refreshing?: boolean;
  /**
   * Pinned to the bottom above the safe area — the availability sheet's Save
   * bar. Rendered outside the ScrollView so it stays reachable on a long list.
   */
  footer?: ReactNode;
  contentStyle?: StyleProp<ViewStyle>;
}

export function Screen({
  children,
  onRefresh,
  refreshing = false,
  footer,
  contentStyle,
}: ScreenProps): ReactElement {
  const insets = useSafeAreaInsets();

  return (
    <KeyboardAvoidingView
      // iOS moves the whole view; on Android the system already resizes the
      // window, and "padding" there fights it and leaves a gap.
      behavior={Platform.OS === "ios" ? "padding" : undefined}
      style={{ flex: 1, backgroundColor: colors.surface.DEFAULT }}
    >
      <ScrollView
        style={{ flex: 1 }}
        contentContainerStyle={[
          {
            padding: spacing.lg,
            gap: spacing.md,
            // Enough clearance that the last card is not welded to the footer
            // or the home indicator.
            paddingBottom: spacing.xxl + insets.bottom,
          },
          contentStyle,
        ]}
        keyboardShouldPersistTaps="handled"
        keyboardDismissMode="on-drag"
        refreshControl={
          onRefresh === undefined ? undefined : (
            <RefreshControl
              refreshing={refreshing}
              onRefresh={onRefresh}
              tintColor={colors.primary[600]}
              colors={[colors.primary[600]]}
            />
          )
        }
      >
        {children}
      </ScrollView>

      {footer !== undefined && (
        <View
          style={{
            borderTopWidth: 1,
            borderTopColor: colors.surface.border,
            backgroundColor: colors.surface.raised,
            paddingHorizontal: spacing.lg,
            paddingTop: spacing.md,
            paddingBottom: spacing.md + insets.bottom,
          }}
        >
          {footer}
        </View>
      )}
    </KeyboardAvoidingView>
  );
}

/**
 * The page title block — the counterpart of vm-ui-kit's PageHeading.
 *
 * Screens keep a heading even though the navigator draws a title bar: the bar
 * has room for two or three words, and the description under these headings is
 * where the rules a supplier needs are stated ("customers only see produce
 * declared for today").
 */
export function PageHeading({
  title,
  description,
  actions,
}: {
  title: string;
  description?: string;
  actions?: ReactNode;
}): ReactElement {
  return (
    <View style={{ gap: spacing.sm }}>
      <Text variant="display" tone="strong" accessibilityRole="header">
        {title}
      </Text>
      {description !== undefined && <Text tone="muted">{description}</Text>}
      {actions !== undefined && (
        <View style={{ flexDirection: "row", flexWrap: "wrap", gap: spacing.sm }}>
          {actions}
        </View>
      )}
    </View>
  );
}
