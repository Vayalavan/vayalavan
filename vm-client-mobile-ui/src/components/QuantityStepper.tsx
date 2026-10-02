/**
 * The −/qty/+ control, used on the catalogue card, the product screen and the
 * cart line.
 *
 * One component rather than three, because the debounce is the hard part and
 * getting it subtly different in three places is how a cart ends up with a
 * quantity nobody chose. The rule it encodes: the number on screen is what the
 * customer last tapped, the number sent is ABSOLUTE, and only one request goes
 * out per burst of taps.
 */
import { useCallback, useEffect, useRef, useState, type ReactElement } from "react";
import { Pressable, View } from "react-native";

import { QTY_DEBOUNCE_MS } from "../lib/cart";
import { colors, radius } from "../theme/tokens";
import { Text } from "./Text";

export interface QuantityStepperProps {
  /** The server's quantity. The stepper shows this unless a tap is pending. */
  qty: number;
  /** Called with the absolute quantity, once the taps stop. */
  onChange: (qty: number) => void;
  /**
   * Called instead of onChange(0) when the customer steps below one.
   *
   * Separate because removing is not "setting the quantity to zero": the
   * server has no zero-quantity line, and the cart endpoint for it is DELETE.
   */
  onRemove: () => void;
  /** Blocks the "+" — there is not enough stock left for another one. */
  canIncrease: boolean;
  /** For screen readers: "Increase Tomatoes quantity". */
  itemName: string;
  disabled?: boolean;
  size?: "md" | "sm";
}

export function QuantityStepper({
  qty,
  onChange,
  onRemove,
  canIncrease,
  itemName,
  disabled = false,
  size = "md",
}: QuantityStepperProps): ReactElement {
  /**
   * The quantity tapped but not yet confirmed by the server.
   *
   * Shown in place of the server's number so the counter responds instantly.
   * Cleared once the server agrees, at which point its value is authoritative
   * again — it may differ, if stock ran out mid-edit.
   */
  const [draft, setDraft] = useState<number | null>(null);
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);

  // A timer still pending when the screen unmounts would fire against a cart
  // the customer is no longer looking at.
  useEffect(
    () => () => {
      if (timer.current) clearTimeout(timer.current);
    },
    [],
  );

  // Once the server agrees with the draft, stop overriding it.
  useEffect(() => {
    if (draft !== null && qty === draft) setDraft(null);
  }, [qty, draft]);

  const shown = draft ?? qty;

  const step = useCallback(
    (next: number) => {
      if (next < 1) {
        if (timer.current) clearTimeout(timer.current);
        setDraft(null);
        onRemove();
        return;
      }
      setDraft(next);
      if (timer.current) clearTimeout(timer.current);
      timer.current = setTimeout(() => onChange(next), QTY_DEBOUNCE_MS);
    },
    [onChange, onRemove],
  );

  const button = size === "md" ? 44 : 36;

  return (
    <View
      style={{
        flexDirection: "row",
        alignItems: "center",
        borderWidth: 1,
        borderColor: colors.primary[600],
        borderRadius: radius.card,
        padding: 2,
      }}
    >
      <StepButton
        // "×" rather than "−" at one, because the next tap removes the line
        // rather than decrementing it. Saying so with the glyph stops the
        // surprise of an item vanishing from a cart.
        glyph={shown === 1 ? "×" : "−"}
        size={button}
        disabled={disabled}
        label={
          shown === 1
            ? `Remove ${itemName} from your cart`
            : `Decrease ${itemName} quantity`
        }
        onPress={() => step(shown - 1)}
      />

      <Text
        variant="bodyStrong"
        tone="strong"
        tabular
        accessibilityLiveRegion="polite"
        accessibilityLabel={`${shown} in your cart`}
        style={{ width: 32, textAlign: "center" }}
      >
        {shown}
      </Text>

      <StepButton
        glyph="+"
        size={button}
        disabled={disabled || !canIncrease}
        label={`Increase ${itemName} quantity`}
        onPress={() => step(shown + 1)}
      />
    </View>
  );
}

function StepButton({
  glyph,
  label,
  onPress,
  disabled,
  size,
}: {
  glyph: string;
  label: string;
  onPress: () => void;
  disabled: boolean;
  size: number;
}): ReactElement {
  return (
    <Pressable
      onPress={onPress}
      disabled={disabled}
      accessibilityRole="button"
      accessibilityLabel={label}
      accessibilityState={{ disabled }}
      style={({ pressed }) => ({
        width: size,
        height: size,
        alignItems: "center",
        justifyContent: "center",
        borderRadius: radius.card,
        backgroundColor: pressed ? colors.primary[50] : "transparent",
        opacity: disabled ? 0.35 : 1,
      })}
    >
      <Text variant="title" tone="strong" style={{ lineHeight: size, color: colors.primary[700] }}>
        {glyph}
      </Text>
    </Pressable>
  );
}
