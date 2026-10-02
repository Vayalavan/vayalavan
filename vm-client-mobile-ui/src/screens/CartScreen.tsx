/**
 * The cart.
 *
 * A tab rather than the web's slide-over panel: a phone has no room for an
 * overlay that covers the thing you were reading, and the tab bar gives the
 * count a permanent home. Adding an item still never costs the customer their
 * place in the catalogue — the card turns into a stepper in place, and nothing
 * navigates.
 *
 * Every total on this screen is the SERVER's string. The fee breakdown is
 * shown here as well as at checkout so the platform fee and delivery charge
 * are never a surprise one screen later (CLAUDE.md §6.2).
 */
import type { ReactElement } from "react";
import { View } from "react-native";
import { useNavigation } from "@react-navigation/native";
import type { NativeStackNavigationProp } from "@react-navigation/native-stack";

import { Button } from "../components/Button";
import { Card } from "../components/Card";
import { Badge, EmptyState, ErrorState, Loading } from "../components/Feedback";
import { QuantityStepper } from "../components/QuantityStepper";
import { Screen } from "../components/Screen";
import { SignInPrompt } from "../components/SignInPrompt";
import { Text } from "../components/Text";
import { useAuth } from "../lib/auth";
import { useCart, useRemoveCartItem, useUpdateCartQty } from "../lib/cart";
import type { Cart, CartItem } from "../lib/types";
import { usePullToRefresh } from "../lib/usePullToRefresh";
import type { CartStackParamList } from "../navigation/types";
import { colors, spacing } from "../theme/tokens";

export function CartScreen(): ReactElement {
  const { user } = useAuth();
  const { cart } = useCart();
  const navigation = useNavigation<NativeStackNavigationProp<CartStackParamList>>();

  const updateQty = useUpdateCartQty();
  const removeItem = useRemoveCartItem();

  const { refreshing, onRefresh } = usePullToRefresh(cart.refetch);

  if (!user) {
    return (
      <SignInPrompt
        title="Your cart lives with your account"
        body="Sign in and your basket follows you between this app and the website — it is kept on our servers, not on this phone."
      />
    );
  }

  if (cart.isPending) return <Loading label="Loading your cart…" />;

  if (cart.isError) {
    return (
      <Screen>
        <ErrorState error={cart.error} onRetry={onRefresh} />
      </Screen>
    );
  }

  const data = cart.data;

  if (data.items.length === 0) {
    return (
      <Screen onRefresh={onRefresh} refreshing={refreshing}>
        <EmptyState
          title="Your cart is empty"
          body="Add something from today’s produce. What’s listed changes every morning."
        />
      </Screen>
    );
  }

  // Totals on screen belong to the server's quantities. While an edit is in
  // flight they are stale, and checking out against a stale total would show
  // one number and charge another.
  const syncing = updateQty.isPending || removeItem.isPending;
  const error = updateQty.error ?? removeItem.error;

  return (
    <Screen
      onRefresh={onRefresh}
      refreshing={refreshing}
      footer={
        <View style={{ gap: spacing.sm }}>
          <Totals cart={data} muted={syncing} />
          <Button
            label={syncing ? "Updating…" : "Proceed to checkout"}
            onPress={() => navigation.navigate("Checkout")}
            disabled={!data.checkoutable || syncing}
            block
          />
          {!data.checkoutable && (
            <Text variant="caption" tone="danger" center>
              Fix the highlighted items above to continue.
            </Text>
          )}
        </View>
      }
    >
      {/* Re-pricing notice. Shown before the items, because it explains them
          (CLAUDE.md §5.3): the cart stores no prices, so a grower's edit
          between adding and checking out surfaces here rather than silently
          changing the total. */}
      {data.changes.length > 0 && (
        <Card tone="accent">
          <View style={{ gap: spacing.xs }}>
            <Text variant="bodyStrong" tone="danger">
              Some items changed since you added them
            </Text>
            {data.changes.map((change) => (
              <Text key={change.cart_item_id} variant="caption" tone="body">
                {change.message}
              </Text>
            ))}
          </View>
        </Card>
      )}

      {error !== null && (
        <Card tone="accent">
          <Text tone="danger" accessibilityRole="alert">
            {error.status === 429
              ? "That was a lot of changes at once. Give it a moment and try again."
              : error.message}
          </Text>
        </Card>
      )}

      {data.items.map((item) => (
        <CartLine
          key={item.id}
          item={item}
          busy={removeItem.isPending}
          onChangeQty={(qty) => updateQty.mutate({ id: item.id, qty })}
          onRemove={() => removeItem.mutate(item.id)}
        />
      ))}
    </Screen>
  );
}

function CartLine({
  item,
  busy,
  onChangeQty,
  onRemove,
}: {
  item: CartItem;
  busy: boolean;
  onChangeQty: (qty: number) => void;
  onRemove: () => void;
}): ReactElement {
  // Two different problems, and the customer can act on only one of them.
  // Not purchasable means this pack size does not fit at all today — the fix
  // is to remove it. Exceeding means the pack is fine and there are simply too
  // many, and the number that fixes it is max_qty.
  const overLimit = item.exceeds_stock && item.purchasable;
  // A pack that is not on today's shelf at all — undeclared, archived, or
  // gone. Nothing about it can be priced or counted, so the line offers one
  // action: remove.
  const gone = !item.available;
  const flagged = gone || !item.purchasable || item.exceeds_stock;
  // "STD" is the implicit grade of an ungraded listing — a real choice of
  // one, which is not a choice worth printing.
  const grade = item.size_code !== "" && item.size_code !== "STD" ? item.size_code : "";

  return (
    <Card tone={flagged ? "accent" : "default"}>
      <View style={{ gap: spacing.md }}>
        <View style={{ flexDirection: "row", gap: spacing.md }}>
          <View style={{ flex: 1, gap: 2 }}>
            <View
              style={{ flexDirection: "row", alignItems: "center", gap: spacing.sm }}
            >
              <Text variant="bodyStrong" tone="strong" style={{ flexShrink: 1 }}>
                {gone ? "Item not available today" : item.product_name}
              </Text>
              {/* The grade, beside the name: two lines reading "Pomegranate ·
                  1 Kg Box" are otherwise identical when they are in fact
                  different crates at different prices. */}
              {grade !== "" && <Badge label={grade} />}
            </View>
            {gone ? (
              <Text variant="caption" tone="danger">
                The grower has not listed this pack today. Remove it to check out.
              </Text>
            ) : (
              <Text variant="caption" tone="muted">
                {item.unit_label}
                {item.size_meta !== "" ? ` · ${item.size_meta}` : ""} ·{" "}
                {item.unit_price_display} each
              </Text>
            )}
            {!gone && !item.purchasable && (
              <Text variant="caption" tone="danger" style={{ marginTop: spacing.xs }}>
                Not enough stock left today. Reduce the quantity or remove it to
                check out.
              </Text>
            )}
            {overLimit && (
              <Text
                variant="caption"
                tone="danger"
                style={{ marginTop: spacing.xs }}
                accessibilityRole="alert"
              >
                {item.available_display !== undefined
                  ? `Only ${item.available_display} left today`
                  : "More than is left today"}
                {item.max_qty > 0
                  ? ` — keep at most ${item.max_qty} to check out.`
                  : " — remove this to check out."}
              </Text>
            )}
          </View>
          <Text variant="bodyStrong" tone="strong" tabular>
            {item.line_total_display}
          </Text>
        </View>

        <View
          style={{
            flexDirection: "row",
            alignItems: "center",
            justifyContent: "space-between",
          }}
        >
          {/* No stepper on a pack that no longer exists: there is nothing to
              have more or less of. */}
          {gone ? (
            <View />
          ) : (
          <QuantityStepper
            qty={item.qty}
            itemName={item.product_name}
            // The server has already decided whether this line fits today's
            // stock; a phone must not talk itself into one more. max_qty is
            // the ceiling it apportioned to THIS line, so + stops at the
            // limit instead of letting someone discover it at checkout.
            canIncrease={item.purchasable && item.qty < item.max_qty}
            disabled={busy}
            size="sm"
            onChange={onChangeQty}
            onRemove={onRemove}
          />
          )}
          <Button
            label="Remove"
            variant="ghost"
            size="sm"
            disabled={busy}
            onPress={onRemove}
            accessibilityHint={`Removes ${item.product_name} from your cart`}
          />
        </View>
      </View>
    </Card>
  );
}

/** Subtotal, platform fee, delivery, total — the CLAUDE.md §6.2 breakdown. */
export function Totals({ cart, muted = false }: { cart: Cart; muted?: boolean }): ReactElement {
  return (
    <View style={{ gap: 2 }}>
      <Row label="Subtotal" value={cart.subtotal_display} />
      <Row label="Platform fee (3%)" value={cart.platform_fee_display} />
      <Row label="Delivery charge" value={cart.delivery_fee_display} />
      <View
        style={{
          flexDirection: "row",
          justifyContent: "space-between",
          borderTopWidth: 1,
          borderTopColor: colors.surface.border,
          paddingTop: spacing.xs,
          marginTop: spacing.xs,
        }}
      >
        <Text variant="bodyStrong" tone="strong">
          Total
        </Text>
        <Text variant="bodyStrong" tone={muted ? "faint" : "strong"} tabular>
          {cart.total_display}
        </Text>
      </View>
    </View>
  );
}

function Row({ label, value }: { label: string; value: string }): ReactElement {
  return (
    <View style={{ flexDirection: "row", justifyContent: "space-between" }}>
      <Text variant="caption" tone="muted">
        {label}
      </Text>
      <Text variant="caption" tone="body" tabular>
        {value}
      </Text>
    </View>
  );
}
