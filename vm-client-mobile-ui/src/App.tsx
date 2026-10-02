/**
 * The app root: providers, then the navigation graph.
 *
 * Order matters. The config gate is outermost because everything below it
 * assumes a reachable API; the error boundary sits above the navigator so a
 * crash inside a screen shows a message rather than a blank white view, which
 * on a phone is indistinguishable from the app being broken. CartProvider is
 * inside AuthProvider because the cart query is enabled by having a session.
 */
import { Component, type ErrorInfo, type ReactElement, type ReactNode } from "react";
import { NavigationContainer, type Theme } from "@react-navigation/native";
import { QueryClientProvider } from "@tanstack/react-query";
import { StatusBar } from "expo-status-bar";
import { View } from "react-native";
import { SafeAreaProvider } from "react-native-safe-area-context";
import "react-native-gesture-handler";

import { Button } from "./components/Button";
import { Card } from "./components/Card";
import { Text } from "./components/Text";
import { VayalLockup } from "./components/Logo";
import { AuthProvider } from "./lib/auth";
import { CartProvider } from "./lib/cart";
import { configResult } from "./lib/config";
import { queryClient } from "./lib/queryClient";
import { RootNavigator } from "./navigation/RootNavigator";
import { colors, spacing, text } from "./theme/tokens";

/** React Navigation's own theme, pointed at the Vayal palette. */
const navigationTheme: Theme = {
  dark: false,
  colors: {
    primary: colors.primary[600],
    background: colors.cream[100],
    card: colors.cream[50],
    text: text.strong,
    border: colors.cream[300],
    notification: colors.accent[500],
  },
  fonts: {
    regular: { fontFamily: "System", fontWeight: "400" },
    medium: { fontFamily: "System", fontWeight: "500" },
    bold: { fontFamily: "System", fontWeight: "700" },
    heavy: { fontFamily: "System", fontWeight: "800" },
  },
};

/** A full-screen message, for the two cases where the app cannot start. */
function FatalScreen({
  title,
  message,
  action,
}: {
  title: string;
  message: string;
  action?: ReactNode;
}): ReactElement {
  return (
    <View
      style={{
        flex: 1,
        backgroundColor: colors.surface.DEFAULT,
        alignItems: "center",
        justifyContent: "center",
        padding: spacing.xl,
        gap: spacing.xl,
      }}
    >
      <VayalLockup size="md" />
      <Card>
        <View style={{ gap: spacing.md }}>
          <Text variant="title" tone="strong">
            {title}
          </Text>
          <Text tone="body">{message}</Text>
          {action}
        </View>
      </Card>
    </View>
  );
}

interface BoundaryState {
  error: Error | null;
}

/**
 * Catches a render-time crash anywhere below it.
 *
 * Without this, an exception in a screen unmounts the whole tree and leaves a
 * white screen — which a customer reads as "the app is broken", uninstalls,
 * and never reports. Offering "Try again" costs one state reset and recovers
 * most transient failures.
 */
class ErrorBoundary extends Component<{ children: ReactNode }, BoundaryState> {
  override state: BoundaryState = { error: null };

  static getDerivedStateFromError(error: Error): BoundaryState {
    return { error };
  }

  override componentDidCatch(error: Error, info: ErrorInfo): void {
    // Goes to the device log, where `npx expo start` and a crash reporter can
    // both see it. There is no logging service wired up yet.
    console.error("Unhandled error in customer app", error, info.componentStack);
  }

  override render(): ReactNode {
    const { error } = this.state;
    if (error === null) return this.props.children;

    return (
      <FatalScreen
        title="Something went wrong"
        message={
          "The app hit a problem it could not recover from on its own. " +
          "Trying again will usually clear it — nothing in your cart is lost, " +
          "it lives on our servers rather than on this phone."
        }
        action={
          <Button label="Try again" onPress={() => this.setState({ error: null })} />
        }
      />
    );
  }
}

export function App(): ReactElement {
  // A build that shipped without an API URL cannot do anything useful. Say so
  // plainly instead of failing on every request with a network error.
  if (!configResult.ok) {
    return (
      <SafeAreaProvider>
        <StatusBar style="dark" />
        <FatalScreen title="This app is not configured" message={configResult.message} />
      </SafeAreaProvider>
    );
  }

  return (
    <SafeAreaProvider>
      {/* Dark glyphs, because every surface in this app is light. The status
          bar is transparent under edge-to-edge, so there is no background to
          set — the screen behind it paints through. */}
      <StatusBar style="dark" />
      <ErrorBoundary>
        <QueryClientProvider client={queryClient}>
          <AuthProvider>
            <CartProvider>
              <NavigationContainer theme={navigationTheme}>
                <RootNavigator />
              </NavigationContainer>
            </CartProvider>
          </AuthProvider>
        </QueryClientProvider>
      </ErrorBoundary>
    </SafeAreaProvider>
  );
}
