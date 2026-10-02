/**
 * The payment sheet, as a full-screen modal WebView.
 *
 * Razorpay Standard Checkout is a web sheet either way — the native SDK just
 * wraps one — and a WebView keeps this app runnable in Expo Go, which SDK 54 is
 * pinned for (CLAUDE.md §3). See `lib/razorpayHtml.ts` for the page itself.
 *
 * The component's whole job is: show the sheet, hand back exactly one outcome,
 * and be closable by a customer who changes their mind. It knows nothing about
 * orders — `lib/payment.ts` decides what an outcome means.
 */
import { useCallback, useRef, type ReactElement } from "react";
import { ActivityIndicator, Modal, Pressable, StyleSheet, View } from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";
import { WebView, type WebViewMessageEvent } from "react-native-webview";

import { Text } from "./Text";
import { colors, radius, spacing, HIT_SIZE } from "../theme/tokens";
import {
  buildCheckoutPage,
  parseCheckoutMessage,
  CHECKOUT_ORIGIN,
  type CheckoutMessage,
  type CheckoutPageOptions,
} from "../lib/razorpayHtml";

export interface RazorpayCheckoutProps {
  /** Null closes the sheet. Non-null opens it for that order. */
  request: Omit<CheckoutPageOptions, "themeColor"> | null;
  /** Fires exactly once per opening. */
  onResult(message: CheckoutMessage): void;
}

export function RazorpayCheckout({
  request,
  onResult,
}: RazorpayCheckoutProps): ReactElement | null {
  /**
   * One outcome per opening, enforced on this side too. The page already
   * guards it, but a WebView that is reloaded — a bank redirect coming back,
   * say — re-runs the page, and a second "dismiss" arriving after a success
   * would undo a payment that actually happened.
   */
  const settled = useRef(false);

  const settle = useCallback(
    (message: CheckoutMessage) => {
      if (settled.current) return;
      settled.current = true;
      onResult(message);
    },
    [onResult],
  );

  const handleMessage = useCallback(
    (event: WebViewMessageEvent) => {
      const message = parseCheckoutMessage(event.nativeEvent.data);
      // Unrecognised messages are ignored, not treated as a failure: a bank's
      // own 3-D Secure page can post whatever it likes into this WebView.
      if (message) settle(message);
    },
    [settle],
  );

  /**
   * Reset the guard whenever a NEW request arrives — including a retry for the
   * same order, which is a fresh object. Derived from props during render
   * rather than in an effect: an effect would run after the WebView had
   * already mounted, leaving a one-render window in which the first message of
   * the new attempt is discarded as a duplicate of the previous one.
   */
  const lastRequest = useRef<RazorpayCheckoutProps["request"]>(null);
  if (request !== lastRequest.current) {
    lastRequest.current = request;
    settled.current = false;
  }

  if (!request) return null;

  const html = buildCheckoutPage({
    ...request,
    // The brand colour comes from the tokens, exactly as every other component
    // gets it — no hex literal in a component (CLAUDE.md §9).
    themeColor: colors.primary[600],
  });

  return (
    <Modal
      visible
      animationType="slide"
      // Android's hardware back button. Treated as dismissal, which is what it
      // is: the order stays unpaid and the customer can retry.
      onRequestClose={() => settle({ type: "dismiss" })}
      transparent={false}
      statusBarTranslucent={false}
    >
      <SafeAreaView style={styles.container} edges={["top", "bottom"]}>
        <View style={styles.header}>
          <Text variant="bodyStrong">Secure payment</Text>
          {/* An explicit exit. The Razorpay sheet has its own close control,
              but it is inside the WebView and a page that failed to load has
              none — without this the customer would be stuck on a blank
              screen with no way back to their order. */}
          <Pressable
            onPress={() => settle({ type: "dismiss" })}
            accessibilityRole="button"
            accessibilityLabel="Cancel payment"
            hitSlop={12}
            style={styles.close}
          >
            <Text variant="body" tone="muted">
              Cancel
            </Text>
          </Pressable>
        </View>

        <WebView
          source={{
            html,
            // A real https origin, not about:blank. Checkout refuses to run
            // from a null origin, and the page loads its script from here.
            baseUrl: CHECKOUT_ORIGIN,
          }}
          originWhitelist={["https://*"]}
          javaScriptEnabled
          domStorageEnabled
          // Banks open their 3-D Secure step in a new window. Without this the
          // navigation is swallowed and the payment silently stalls.
          setSupportMultipleWindows={false}
          // A payment page must never be restored from cache.
          cacheEnabled={false}
          incognito
          startInLoadingState
          renderLoading={() => (
            <View style={styles.loading}>
              <ActivityIndicator color={colors.primary[600]} />
              <Text variant="caption" tone="muted" style={styles.loadingLabel}>
                Opening the payment sheet…
              </Text>
            </View>
          )}
          onMessage={handleMessage}
          onError={() =>
            settle({
              type: "failed",
              message:
                "We could not load the payment sheet. Check your connection and try again.",
            })
          }
          onHttpError={() =>
            settle({
              type: "failed",
              message:
                "The payment provider did not respond. Please try again in a moment.",
            })
          }
          style={styles.webview}
        />
      </SafeAreaView>
    </Modal>
  );
}

const styles = StyleSheet.create({
  container: { flex: 1, backgroundColor: colors.surface.raised },
  header: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
    paddingHorizontal: spacing.lg,
    paddingVertical: spacing.md,
    borderBottomWidth: 1,
    borderBottomColor: colors.surface.border,
  },
  close: {
    minHeight: HIT_SIZE,
    justifyContent: "center",
    paddingHorizontal: spacing.sm,
    borderRadius: radius.card,
  },
  webview: { flex: 1, backgroundColor: colors.surface.raised },
  loading: {
    // Spelt out because React Native 0.86 removed
    // StyleSheet.absoluteFillObject. Spreading a missing export is a silent
    // no-op, which would have left this overlay statically positioned.
    position: "absolute",
    top: 0,
    left: 0,
    right: 0,
    bottom: 0,
    alignItems: "center",
    justifyContent: "center",
    backgroundColor: colors.surface.raised,
  },
  loadingLabel: { marginTop: spacing.md },
});
