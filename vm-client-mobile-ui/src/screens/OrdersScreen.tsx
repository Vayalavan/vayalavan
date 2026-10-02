/**
 * A customer's own orders.
 *
 * Status words are the customer's, not the database's: "processed" describes
 * our warehouse step, and someone checking their phone wants to know whether
 * their food is coming. The mapping is the same one vm-client-ui uses, so the
 * app and the website never describe one order two ways.
 */
import type { ReactElement } from "react";
import { Pressable, View } from "react-native";
import { useNavigation } from "@react-navigation/native";
import type { NativeStackNavigationProp } from "@react-navigation/native-stack";
import { useQuery } from "@tanstack/react-query";

import { Badge, EmptyState, ErrorState, Loading } from "../components/Feedback";
import { Card } from "../components/Card";
import { OrderThumbnails } from "../components/OrderThumbnails";
import { Screen } from "../components/Screen";
import { SignInPrompt } from "../components/SignInPrompt";
import { Text } from "../components/Text";
import type { ApiError } from "../lib/api";
import { useAuth } from "../lib/auth";
import { api } from "../lib/client";
import { formatISTDate, formatISTDateTime } from "../lib/datetime";
import { usePullToRefresh } from "../lib/usePullToRefresh";
import type { Order, OrderPage } from "../lib/types";
import type { OrdersStackParamList } from "../navigation/types";
import { colors, radius, spacing } from "../theme/tokens";

/** How each status reads to a customer, and which badge tone carries it. */
export const STATUS_COPY: Record<string, { label: string; tone: "primary" | "neutral" | "accent" }> = {
  pending_payment: { label: "Awaiting payment", tone: "neutral" },
  paid: { label: "Confirmed", tone: "primary" },
  processed: { label: "Being prepared", tone: "primary" },
  // "Dispatched", not "On its way": the word the rest of the platform uses for
  // this transition, and identical to the web's badge (CLAUDE.md §1 parity).
  dispatched: { label: "Dispatched", tone: "primary" },
  payment_failed: { label: "Payment failed", tone: "accent" },
  expired: { label: "Expired", tone: "accent" },
  cancelled: { label: "Cancelled", tone: "accent" },
  refunded: { label: "Refunded", tone: "accent" },
};

/**
 * Why an order stopped, in the customer's terms.
 *
 * Only the statuses that end an order appear here; anything else is still
 * moving and has a timeline to show instead. Wording matches vm-client-ui
 * word for word — a customer who checks the app and then the website must not
 * be given two different explanations of the same thing.
 */
export const TERMINAL_COPY: Record<string, string> = {
  expired:
    "Payment wasn't completed in time, so we released the items back to the " +
    "shop. Nothing was charged. You're welcome to order them again if they're " +
    "still available today.",
  payment_failed:
    "The payment didn't go through, so this order wasn't placed. Nothing was " +
    "charged. Please try again, or use a different payment method.",
  cancelled: "This order was cancelled. Anything charged is refunded to the original payment method.",
  refunded: "This order was refunded to the original payment method.",
};

export function StatusBadge({ status }: { status: string }): ReactElement {
  const copy = STATUS_COPY[status] ?? {
    label: status.replace(/_/g, " "),
    tone: "neutral" as const,
  };
  return <Badge label={copy.label} tone={copy.tone} />;
}

export function OrdersScreen(): ReactElement {
  const { user } = useAuth();
  const navigation = useNavigation<NativeStackNavigationProp<OrdersStackParamList>>();

  const orders = useQuery<OrderPage, ApiError>({
    queryKey: ["my-orders"],
    queryFn: () => api.get<OrderPage>("/orders"),
    enabled: Boolean(user),
  });

  const { refreshing, onRefresh } = usePullToRefresh(orders.refetch);

  if (!user) {
    return (
      <SignInPrompt
        title="Your orders live with your account"
        body="Sign in to see what you have ordered, when it was processed and when to expect it."
      />
    );
  }

  if (orders.isPending) return <Loading label="Loading your orders…" />;

  if (orders.isError) {
    return (
      <Screen>
        <ErrorState error={orders.error} onRetry={onRefresh} />
      </Screen>
    );
  }

  return (
    <Screen onRefresh={onRefresh} refreshing={refreshing}>
      {orders.data.orders.length === 0 ? (
        <EmptyState
          title="No orders yet"
          body="When you order today’s produce it will appear here."
        />
      ) : (
        orders.data.orders.map((order) => (
          <OrderRow
            key={order.id}
            order={order}
            onPress={() =>
              navigation.navigate("OrderDetail", {
                orderId: order.id,
                orderNumber: order.order_number,
              })
            }
          />
        ))
      )}
    </Screen>
  );
}

function OrderRow({ order, onPress }: { order: Order; onPress: () => void }): ReactElement {
  return (
    <Pressable
      onPress={onPress}
      accessibilityRole="button"
      accessibilityLabel={`Order ${order.order_number}, ${order.total_display}`}
      accessibilityHint="Opens the timeline and items"
      style={({ pressed }) => ({ opacity: pressed ? 0.85 : 1 })}
    >
      <Card>
        <View style={{ gap: spacing.sm }}>
          <View
            style={{ flexDirection: "row", alignItems: "center", gap: spacing.sm }}
          >
            <Text variant="bodyStrong" tone="strong" style={{ flex: 1 }}>
              {order.order_number}
            </Text>
            {order.schedule_id !== undefined && (
              <View
                style={{
                  paddingHorizontal: spacing.sm,
                  paddingVertical: 2,
                  borderRadius: radius.pill,
                  backgroundColor: colors.gold[50],
                }}
              >
                <Text variant="caption" style={{ color: colors.gold[700], fontWeight: "700" }}>
                  Scheduled
                </Text>
              </View>
            )}
            <StatusBadge status={order.status} />
          </View>

          <View
            style={{
              flexDirection: "row",
              alignItems: "flex-end",
              justifyContent: "space-between",
              gap: spacing.md,
            }}
          >
            <View style={{ flex: 1 }}>
              <Text variant="caption" tone="muted">
                Placed {formatISTDateTime(order.placed_at)}
              </Text>
              <Text variant="caption" tone="muted">
                Expected {formatISTDate(order.expected_delivery_date)}
              </Text>
            </View>
            <Text variant="title" tone="strong" tabular>
              {order.total_display}
            </Text>
          </View>

          {/* What was in it, before any of the text is read. */}
          <OrderThumbnails items={order.items} />
        </View>
      </Card>
    </Pressable>
  );
}
