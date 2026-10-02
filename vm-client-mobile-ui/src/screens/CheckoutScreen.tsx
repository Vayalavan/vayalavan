/**
 * Checkout: pick a delivery address, confirm the breakdown, pay.
 *
 * Placing the order and paying for it are two steps on the server and one tap
 * for the customer: POST /orders reserves stock and registers a Razorpay order
 * (CLAUDE.md §6.4 step 1), and the payment sheet opens on top of the result.
 *
 * The sheet is a modal WebView, not Razorpay's native SDK — the native SDK is
 * a native module and is not in Expo Go, so adopting it would cost the ability
 * to test payments on a real handset without an EAS development build. See
 * `lib/razorpayHtml.ts`.
 *
 * Nothing here can mark an order paid: the sheet's success callback only
 * starts polling, and the WEBHOOK decides (§6.4 step 4). An order whose sheet
 * is dismissed keeps its reservation and can be paid from the order screen.
 */
import { useCallback, useEffect, useRef, useState, type ReactElement } from "react";
import { Pressable, View } from "react-native";
import type { BottomTabNavigationProp } from "@react-navigation/bottom-tabs";
import {
  useFocusEffect,
  useNavigation,
  type CompositeNavigationProp,
} from "@react-navigation/native";
import type { NativeStackNavigationProp } from "@react-navigation/native-stack";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { Button } from "../components/Button";
import { Card } from "../components/Card";
import { Banner, EmptyState, ErrorState, Loading } from "../components/Feedback";
import { RazorpayCheckout } from "../components/RazorpayCheckout";
import { SchedulePlanner } from "../components/SchedulePlanner";
import { Screen } from "../components/Screen";
import { Text } from "../components/Text";
import type { ApiError } from "../lib/api";
import { useAuth } from "../lib/auth";
import { CART_KEY, useCart } from "../lib/cart";
import { api } from "../lib/client";
import { newIdempotencyKey } from "../lib/idempotency";
import { paymentCopy, usePayment, type PaymentState } from "../lib/payment";
import type { Address, Order } from "../lib/types";
import { gradedName } from "../lib/grade";
import type { CartStackParamList, MainTabParamList } from "../navigation/types";
import { colors, radius, spacing } from "../theme/tokens";
import { Totals } from "./CartScreen";
import { AddressChoice } from "./AddressesScreen";

/**
 * Composite, because "keep shopping" leaves this stack entirely: the Shop tab
 * is the tab navigator's, not the cart stack's.
 */
type CheckoutNavigation = CompositeNavigationProp<
  NativeStackNavigationProp<CartStackParamList, "Checkout">,
  BottomTabNavigationProp<MainTabParamList>
>;

export function CheckoutScreen(): ReactElement {
  const { cart } = useCart();
  const { user } = useAuth();
  const queryClient = useQueryClient();
  const navigation = useNavigation<CheckoutNavigation>();
  const payment = usePayment();
  const { reset: resetPayment } = payment;

  const [addressId, setAddressId] = useState<string | null>(null);
  /**
   * Pay now for the next delivery, or schedule it — once on a later date, or
   * on repeat — paid from the wallet (CLAUDE.md §6.7).
   */
  const [mode, setMode] = useState<"now" | "schedule">("now");
  const [placed, setPlaced] = useState<Order | null>(null);

  /**
   * The address ids that existed when the customer left to add one.
   *
   * The form is its own screen (see AddressFormScreen's note on why), so it
   * cannot hand a value back the way the web's inline form does. Remembering
   * what was on the list and selecting whatever is new on return does the same
   * job without threading a result through navigation params — and still works
   * if the address was added from the Account tab mid-checkout.
   *
   * A ref, not state: it is a comparison point, and re-rendering on it would
   * do nothing but re-render.
   */
  const idsBeforeAdding = useRef<Set<string> | null>(null);

  /**
   * One key per checkout attempt, generated before the first send and reused
   * on every retry (CLAUDE.md rule 6). A ref, not state: re-rendering must not
   * mint a new key, or a second tap would place a second real order.
   */
  const idempotencyKey = useRef(newIdempotencyKey());

  const addresses = useQuery<{ addresses: Address[] }, ApiError>({
    queryKey: ["addresses"],
    queryFn: () => api.get<{ addresses: Address[] }>("/addresses"),
  });

  useEffect(() => {
    const known = idsBeforeAdding.current;
    const list = addresses.data?.addresses;
    if (known === null || list === undefined) return;
    const added = list.find((address) => !known.has(address.id));
    if (added === undefined) return;
    // The customer added it in order to deliver to it. Asking them to then
    // pick it off the list is asking the same question twice.
    setAddressId(added.id);
    idsBeforeAdding.current = null;
  }, [addresses.data]);

  const chosenId =
    addressId ??
    addresses.data?.addresses.find((address) => address.is_default)?.id ??
    addresses.data?.addresses[0]?.id ??
    null;

  /**
   * Opens the address form inside THIS stack, so Back returns to the order
   * being placed rather than dropping the customer in the Account tab.
   */
  const addAddress = useCallback(() => {
    idsBeforeAdding.current = new Set(
      (addresses.data?.addresses ?? []).map((address) => address.id),
    );
    navigation.navigate("AddressForm");
  }, [addresses.data, navigation]);

  /**
   * Who is paying, for the sheet's prefilled fields.
   *
   * The recipient on the address is who the produce is FOR; the account holder
   * is who is paying. The delivery phone is still the better contact guess —
   * an account is often signed in on a shared handset.
   */
  const prefillFor = useCallback(
    (order: Order) => ({
      name: user?.name ?? order.address?.recipient_name ?? undefined,
      email: user?.email ?? undefined,
      contact: order.address?.phone ?? user?.phone ?? undefined,
    }),
    [user],
  );

  const placeOrder = useMutation<Order, ApiError, string>({
    mutationFn: (id) =>
      api.post<Order>(
        "/orders",
        { address_id: id },
        { idempotencyKey: idempotencyKey.current },
      ),
    onSuccess: (order) => {
      setPlaced(order);
      // The server emptied the cart into the order; a stale cached cart would
      // otherwise still show a checkout button for items already committed.
      void queryClient.invalidateQueries({ queryKey: CART_KEY });
      void queryClient.invalidateQueries({ queryKey: ["my-orders"] });

      // Straight into the payment sheet — the customer tapped "Pay", so a
      // second button between them and Razorpay would only be a step to miss.
      // Retries live on the confirmation below.
      payment.pay(order, prefillFor(order));
    },
  });


  // The nested form: "Shop" is a tab holding a stack, so the target screen
  // inside it has to be named or React Navigation resolves to whatever that
  // stack was last showing — which, after browsing, is a product page.
  const backToShop = useCallback(
    () => navigation.navigate("Shop", { screen: "Catalog" }),
    [navigation],
  );

  /**
   * The confirmation is for the moment after paying, not a page of the app.
   *
   * A tab navigator keeps its screens mounted, so leaving the Cart tab does
   * not unmount this one — without this, tapping Cart again days later still
   * showed "Order placed" for an order long since delivered, with the real
   * (empty) cart hidden behind it. The web has never had the bug: a route
   * change there unmounts the page and the state goes with it.
   *
   * Three things have to be undone together, or the screen comes back subtly
   * broken:
   *   - `placed`, which is what is on screen;
   *   - the mutation, whose `isSuccess` keeps the Place order button disabled;
   *   - the idempotency key. It is deliberately stable across retries of ONE
   *     checkout (CLAUDE.md rule 6), but reusing it for the NEXT one makes the
   *     server return the previous order from its dedupe table — the customer
   *     would see a confirmation for an order they did not just place, and
   *     their new cart would still be sitting there;
   *   - the payment phase, which is the same bug wearing a different hat: left
   *     at "paid", the next checkout's confirmation would open already
   *     announcing a payment for an order nobody has paid for.
   *
   * Cleanup runs on blur, not on focus, so the confirmation survives being
   * backgrounded — the tap that leaves is what dismisses it.
   *
   * Both values are read through refs and the effect depends only on
   * `navigation`. useFocusEffect re-runs — cleanup included — whenever its
   * callback's identity changes, and `placeOrder` is a fresh object on every
   * render: as a dependency it would fire this cleanup on the render right
   * after the order was placed and dismiss the confirmation instantly.
   */
  const placedRef = useRef(false);
  placedRef.current = placed !== null;
  const resetMutation = useRef(placeOrder.reset);
  resetMutation.current = placeOrder.reset;

  useFocusEffect(
    useCallback(
      () => () => {
        if (!placedRef.current) return;
        setPlaced(null);
        resetMutation.current();
        resetPayment();
        idempotencyKey.current = newIdempotencyKey();
        // The stack is Cart -> Checkout. Left alone, the Cart tab reopens on
        // a checkout for a cart that no longer exists.
        if (navigation.canGoBack()) navigation.popToTop();
      },
      // `resetPayment` is stable (a useCallback with no dependencies), so
      // naming it here does not reintroduce the re-run this effect is written
      // to avoid — unlike `placeOrder`, a fresh object on every render.
      [navigation, resetPayment],
    ),
  );

  if (placed !== null) {
    return (
      <>
        <OrderPlaced
          order={placed}
          payment={payment}
          onRetry={() => payment.pay(placed, prefillFor(placed))}
          onDone={backToShop}
        />
        <RazorpayCheckout request={payment.request} onResult={payment.handleResult} />
      </>
    );
  }

  if (cart.isPending || addresses.isPending) return <Loading label="Loading checkout…" />;

  if (addresses.isError) {
    return (
      <Screen>
        <ErrorState error={addresses.error} onRetry={() => void addresses.refetch()} />
      </Screen>
    );
  }

  const data = cart.data;

  if (!data || data.items.length === 0) {
    return (
      <Screen>
        <EmptyState
          title="Your cart is empty"
          body="Add something from today’s produce to check out."
          action={<Button label="Browse today’s produce" onPress={backToShop} block />}
        />
      </Screen>
    );
  }

  const list = addresses.data.addresses;

  return (
    <Screen
      footer={
        mode === "schedule" ? undefined : (
        <View style={{ gap: spacing.sm }}>
          <Totals cart={data} />
          <Button
            label={
              placeOrder.isPending
                ? "Placing order…"
                : `Pay ${data.total_display}`
            }
            onPress={() => chosenId !== null && placeOrder.mutate(chosenId)}
            loading={placeOrder.isPending}
            disabled={!data.checkoutable || chosenId === null || placeOrder.isSuccess}
            block
          />
          {/* Said before the tap, not after: the next thing on screen is a
              payment sheet, and the amount on the button is the amount that
              will be charged. */}
          <Text variant="caption" tone="muted" center>
            You’ll pay securely via Razorpay. Your produce is reserved while you
            complete payment.
          </Text>
        </View>
        )
      }
    >
      {/* Now or later. The choice changes what the whole screen asks for. */}
      <View
        accessibilityRole="radiogroup"
        accessibilityLabel="When should this arrive?"
        style={{ flexDirection: "row", gap: 4, padding: 4, borderRadius: 18, backgroundColor: colors.cream[200] }}
      >
        {(
          [
            ["now", "Deliver now", "Pay today"],
            ["schedule", "Schedule / Repeat", "Paid from wallet"],
          ] as const
        ).map(([value, label, hint]) => {
          const on = mode === value;
          return (
            <Pressable
              key={value}
              onPress={() => setMode(value)}
              accessibilityRole="radio"
              accessibilityState={{ selected: on }}
              style={{
                flex: 1,
                paddingVertical: spacing.sm,
                paddingHorizontal: spacing.md,
                borderRadius: 14,
                backgroundColor: on ? colors.surface.raised : "transparent",
              }}
            >
              <Text variant="label" tone={on ? "strong" : "muted"}>
                {label}
              </Text>
              <Text variant="caption" tone="faint">
                {hint}
              </Text>
            </Pressable>
          );
        })}
      </View>

      <Card>
        <View style={{ gap: spacing.md }}>
          <Text variant="heading" tone="strong">
            Delivery address
          </Text>

          {list.length === 0 ? (
            <Text tone="muted">
              You have no saved addresses yet. Add one to continue.
            </Text>
          ) : (
            <View accessibilityRole="radiogroup" style={{ gap: spacing.sm }}>
              {list.map((address) => (
                <AddressChoice
                  key={address.id}
                  address={address}
                  selected={chosenId === address.id}
                  onSelect={() => setAddressId(address.id)}
                />
              ))}
            </View>
          )}

          <Button
            label={list.length === 0 ? "Add a delivery address" : "Add a new address"}
            onPress={addAddress}
            variant={list.length === 0 ? "primary" : "secondary"}
            size="sm"
            block
          />
        </View>
      </Card>

      {/* Why the button below is off. A disabled Place order with no
          explanation is the worst version of running out of stock: the
          customer can see the total and cannot see the problem. The messages
          are whole sentences from the server and name the produce. */}
      {!data.checkoutable && data.changes.length > 0 && (
        <Card tone="accent">
          <View style={{ gap: spacing.xs }}>
            <Text variant="bodyStrong" tone="danger">
              Your cart needs a change before you can order
            </Text>
            {data.changes.map((change) => (
              <Text key={change.cart_item_id} variant="caption" tone="body">
                {change.message}
              </Text>
            ))}
            <Button
              label="Back to cart"
              variant="secondary"
              size="sm"
              onPress={() => navigation.navigate("CartList")}
            />
          </View>
        </Card>
      )}

      <Card>
        <View style={{ gap: spacing.sm }}>
          <Text variant="heading" tone="strong">
            Your items
          </Text>
          {data.items.map((item) => (
            <View
              key={item.id}
              style={{ flexDirection: "row", justifyContent: "space-between", gap: spacing.md }}
            >
              <View style={{ flex: 1 }}>
                {/* The GRADE, in the name: a checkout that shows two grades
                    of one produce identically is asking the customer to pay
                    for a guess. */}
                <Text variant="bodyStrong" tone="strong">
                  {gradedName(item.product_name, item.size_code)}
                </Text>
                <Text variant="caption" tone="muted">
                  {item.unit_label} × {item.qty}
                </Text>
                {item.exceeds_stock && (
                  <Text variant="caption" tone="danger">
                    {item.available_display !== undefined
                      ? `Only ${item.available_display} left today`
                      : "More than is left today"}
                  </Text>
                )}
              </View>
              <Text variant="bodyStrong" tone="strong" tabular>
                {item.line_total_display}
              </Text>
            </View>
          ))}
        </View>
      </Card>

      {placeOrder.isError && (
        <Banner tone="error" message={placeOrder.error.message} />
      )}

      {mode === "schedule" && (
        <Card>
          <SchedulePlanner addressId={chosenId} totalPaise={data.total_paise} />
        </Card>
      )}

      <Button label="Keep shopping" variant="secondary" onPress={backToShop} block />
    </Screen>
  );
}

/**
 * The result screen, which must not overstate what happened.
 *
 * Four outcomes reach here and only one is "done":
 *   paid       — the webhook confirmed it; say so.
 *   slow       — money captured, webhook late; reassure, do not ask to re-pay.
 *   confirming — still polling; a wait, not a tick.
 *   idle/error — the sheet closed unpaid; offer it again while the
 *                reservation lasts.
 * The tick is drawn only in the first two cases. A green check above
 * "awaiting payment" is exactly what makes someone stop watching for a payment
 * they still owe.
 */
function OrderPlaced({
  order,
  payment,
  onRetry,
  onDone,
}: {
  order: Order;
  payment: PaymentState;
  onRetry: () => void;
  onDone: () => void;
}): ReactElement {
  const settled = payment.phase === "paid" || payment.phase === "slow";
  const working = payment.phase === "opening" || payment.phase === "confirming";
  const status = paymentCopy(payment.phase);

  return (
    <Screen contentStyle={{ flexGrow: 1, justifyContent: "center" }}>
      <Card>
        <View style={{ gap: spacing.md, alignItems: "center" }}>
          <View
            style={{
              width: 48,
              height: 48,
              borderRadius: radius.pill,
              backgroundColor: settled ? colors.primary[600] : colors.accent[100],
              alignItems: "center",
              justifyContent: "center",
            }}
          >
            <Text variant="title" tone={settled ? "onPrimary" : "danger"}>
              {settled ? "✓" : "⏳"}
            </Text>
          </View>

          <Text variant="title" tone="strong" center accessibilityRole="header">
            {payment.phase === "paid" ? "Payment received" : "Order placed"}
          </Text>
          <Text tone="muted" center>
            Your order number is{" "}
            <Text variant="bodyStrong" tone="strong" selectable>
              {order.order_number}
            </Text>
            .
          </Text>

          <View
            style={{
              alignSelf: "stretch",
              backgroundColor: colors.surface.sunken,
              borderRadius: radius.card,
              padding: spacing.md,
              gap: spacing.xs,
            }}
            accessibilityLiveRegion="polite"
          >
            {(working || settled) && status !== null ? (
              <Text variant={settled ? "caption" : "bodyStrong"} tone={settled ? "body" : "strong"}>
                {status}
              </Text>
            ) : (
              <>
                <Text variant="bodyStrong" tone="strong">
                  Payment not completed
                </Text>
                <Text variant="caption" tone="body">
                  {payment.message ??
                    "You closed the payment window before it finished."}{" "}
                  Your produce is reserved for a short while — pay now and the
                  order goes through. If we don’t hear from you the reservation
                  is released and the stock returns to today’s catalogue.
                </Text>
                <Button
                  label="Pay now"
                  onPress={onRetry}
                  disabled={payment.busy}
                  block
                />
              </>
            )}
          </View>

          <Button
            label="Back to today’s produce"
            variant={settled ? "primary" : "secondary"}
            onPress={onDone}
            block
          />
        </View>
      </Card>
    </Screen>
  );
}
