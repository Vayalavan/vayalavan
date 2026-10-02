/**
 * One order: where it is, what is in it, what it cost, and where it is going.
 *
 * The timeline is fed by the same server-computed milestones the web
 * storefront and the admin console render — a customer and an ops person
 * looking at one order must never be shown different dates (CLAUDE.md §6.1).
 */
import type { ReactElement } from "react";
import { View } from "react-native";
import type { NativeStackScreenProps } from "@react-navigation/native-stack";
import { useQuery } from "@tanstack/react-query";

import { Button } from "../components/Button";
import { Card } from "../components/Card";
import { ErrorState, Loading } from "../components/Feedback";
import { OrderTimeline } from "../components/OrderTimeline";
import { RazorpayCheckout } from "../components/RazorpayCheckout";
import { Screen } from "../components/Screen";
import { Text } from "../components/Text";
import type { ApiError } from "../lib/api";
import { useAuth } from "../lib/auth";
import { api } from "../lib/client";
import { config } from "../lib/config";
import { paymentCopy, usePayment } from "../lib/payment";
import { formatISTDate, formatISTDateTime } from "../lib/datetime";
import { usePullToRefresh } from "../lib/usePullToRefresh";
import type { AddressSnapshot, Order } from "../lib/types";
import type { OrdersStackParamList } from "../navigation/types";
import { gradedName } from "../lib/grade";
import { colors, spacing } from "../theme/tokens";
import { STATUS_COPY, StatusBadge, TERMINAL_COPY } from "./OrdersScreen";

type Props = NativeStackScreenProps<OrdersStackParamList, "OrderDetail">;

export function OrderDetailScreen({ route }: Props): ReactElement {
  const { orderId } = route.params;
  const { user } = useAuth();
  const payment = usePayment();

  const order = useQuery<Order, ApiError>({
    queryKey: ["my-order", orderId],
    queryFn: () => api.get<Order>(`/orders/${orderId}`),
  });

  const { refreshing, onRefresh } = usePullToRefresh(order.refetch);

  if (order.isPending) return <Loading />;

  if (order.isError) {
    return (
      <Screen>
        {order.error.status === 404 ? (
          <Card>
            <View style={{ gap: spacing.sm }}>
              <Text variant="heading" tone="strong">
                Order not found
              </Text>
              <Text tone="muted">We could not find that order on your account.</Text>
            </View>
          </Card>
        ) : (
          <ErrorState error={order.error} onRetry={onRefresh} />
        )}
      </Screen>
    );
  }

  const data = order.data;
  const milestones = data.milestones ?? [];

  return (
    <>
    <Screen onRefresh={onRefresh} refreshing={refreshing}>
      <View style={{ flexDirection: "row", alignItems: "center", gap: spacing.sm }}>
        <View style={{ flex: 1 }}>
          <Text variant="display" tone="strong" accessibilityRole="header">
            {data.order_number}
          </Text>
          <Text variant="caption" tone="muted">
            Placed {formatISTDateTime(data.placed_at)}
          </Text>
        </View>
        <StatusBadge status={data.status} />
      </View>

      {/* An unpaid order is the one state on this screen with something to DO.
          It sits above the timeline because someone who opened this from an
          abandoned checkout came here to finish paying, and the stock is only
          held for RESERVATION_TTL_MINUTES. */}
      {data.status === "pending_payment" && (
        <Card>
          <View style={{ gap: spacing.sm }}>
            <Text variant="heading" tone="strong">
              {payment.phase === "paid"
                ? "Payment received"
                : "Finish paying for this order"}
            </Text>
            <Text variant="caption" tone="body" accessibilityLiveRegion="polite">
              {paymentCopy(payment.phase) ??
                payment.message ??
                `Your produce is reserved for a short while. Pay ${data.total_display} to confirm this order — if we don’t hear from you, the reservation is released and the stock returns to today’s catalogue.`}
            </Text>
            {payment.phase !== "paid" && payment.phase !== "slow" && (
              <Button
                label={payment.busy ? "Please wait…" : `Pay ${data.total_display}`}
                onPress={() =>
                  payment.pay(data, {
                    name: user?.name ?? undefined,
                    email: user?.email ?? undefined,
                    contact: data.address?.phone ?? user?.phone ?? undefined,
                  })
                }
                loading={payment.busy}
                disabled={payment.busy || data.razorpay_order_id === undefined}
                block
              />
            )}
            {/* No provider order means Razorpay was unreachable at placement.
                Saying so beats a button that cannot work. */}
            {data.razorpay_order_id === undefined && (
              <Text variant="caption" tone="muted">
                We could not reach the payment provider for this order. Write to{" "}
                {config.supportEmail} and we’ll sort it out.
              </Text>
            )}
          </View>
        </Card>
      )}

      {/* An order that stopped has no timeline — the server sends none for a
          terminal status — so the card explains what happened instead of
          leaving the screen silent about it. Same copy as the web. */}
      {TERMINAL_COPY[data.status] !== undefined && (
        <Card>
          <View style={{ gap: spacing.sm }}>
            <Text variant="heading" tone="strong">
              {STATUS_COPY[data.status]?.label ?? "This order stopped"}
            </Text>
            <Text tone="muted">{TERMINAL_COPY[data.status]}</Text>
          </View>
        </Card>
      )}

      {milestones.length > 0 && (
        <Card>
          <View style={{ gap: spacing.lg }}>
            <Text variant="heading" tone="strong">
              Timeline
            </Text>
            <OrderTimeline
              milestones={milestones}
              // Server strings, with a local fallback only if an older build of
              // the API ever answers without them.
              expectedDeliveryText={
                data.expected_delivery_text ??
                `Expected delivery: ${formatISTDate(data.expected_delivery_date)}`
              }
              courierNotice={
                data.courier_notice ??
                "We hand your order to professional courier partners after processing, so live tracking isn't available. Dates shown are estimates."
              }
              supportNotice={
                data.support_notice ?? `Any issues? Write to us at ${config.supportEmail}`
              }
            />
          </View>
        </Card>
      )}

      <Card>
        <View style={{ gap: spacing.sm }}>
          <Text variant="heading" tone="strong">
            Items
          </Text>
          {(data.items ?? []).map((item, index) => (
            <View
              // Snapshots have no id of their own; the index is stable because
              // an order's lines never reorder once placed.
              key={`${item.product_name}-${index}`}
              style={{ flexDirection: "row", justifyContent: "space-between", gap: spacing.md }}
            >
              <View style={{ flex: 1 }}>
                {/* Snapshots, so this renders correctly even after the product
                    is edited or removed (CLAUDE.md §5.3). */}
                {/* The GRADE the pack was bought at, ahead of the product's
                    quality label: two lines of one produce at different grades
                    are different goods at different prices, and an order that
                    shows them identically cannot be checked against what
                    arrived. */}
                <Text variant="bodyStrong" tone="strong">
                  {gradedName(item.product_name, item.size_code)}
                  {item.grade !== undefined && item.grade !== "" ? ` · ${item.grade}` : ""}
                </Text>
                <Text variant="caption" tone="muted">
                  {item.unit_label} × {item.qty} · {item.unit_price_display} each
                </Text>
              </View>
              <Text variant="bodyStrong" tone="strong" tabular>
                {item.line_total_display}
              </Text>
            </View>
          ))}
        </View>
      </Card>

      <Card>
        <View style={{ gap: spacing.xs }}>
          <Text variant="heading" tone="strong" style={{ marginBottom: spacing.xs }}>
            Payment
          </Text>
          <PaymentRow label="Subtotal" value={data.subtotal_display} />
          <PaymentRow label="Platform fee" value={data.platform_fee_display} />
          <PaymentRow label="Delivery charge" value={data.delivery_fee_display} />
          <View
            style={{
              flexDirection: "row",
              justifyContent: "space-between",
              borderTopWidth: 1,
              borderTopColor: colors.surface.border,
              paddingTop: spacing.sm,
              marginTop: spacing.xs,
            }}
          >
            <Text variant="bodyStrong" tone="strong">
              Total
            </Text>
            <Text variant="bodyStrong" tone="strong" tabular>
              {data.total_display}
            </Text>
          </View>
        </View>
      </Card>

      {data.address !== undefined && <DeliveringTo address={data.address} />}
    </Screen>
    <RazorpayCheckout request={payment.request} onResult={payment.handleResult} />
    </>
  );
}

function PaymentRow({ label, value }: { label: string; value: string }): ReactElement {
  return (
    <View style={{ flexDirection: "row", justifyContent: "space-between" }}>
      <Text tone="muted">{label}</Text>
      <Text tone="body" tabular>
        {value}
      </Text>
    </View>
  );
}

/**
 * The address as it was at placement, not the current address book: editing an
 * address later must not silently rewrite where a past order went.
 */
function DeliveringTo({ address }: { address: AddressSnapshot }): ReactElement {
  const lines = [
    address.line1,
    address.line2,
    address.landmark,
    [address.city, address.state, address.pincode].filter(Boolean).join(", "),
    address.phone,
  ].filter((line): line is string => typeof line === "string" && line !== "");

  return (
    <Card>
      <View style={{ gap: spacing.xs }}>
        <Text variant="heading" tone="strong">
          Delivering to
        </Text>
        {address.recipient_name !== undefined && address.recipient_name !== "" && (
          <Text variant="bodyStrong" tone="strong">
            {address.recipient_name}
          </Text>
        )}
        {lines.map((line) => (
          <Text key={line} tone="muted">
            {line}
          </Text>
        ))}
      </View>
    </Card>
  );
}
