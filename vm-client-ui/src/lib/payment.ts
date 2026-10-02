/**
 * Paying for an order, from either the checkout flow or the order page.
 *
 * One hook rather than two copies: a customer who dismisses the sheet at
 * checkout pays from their order page later, and both paths have to treat
 * "paid" identically or the two screens would disagree about the same order.
 *
 * The shape of the flow (CLAUDE.md §6.4):
 *   open Checkout -> verify signature (a UX HINT) -> poll until the WEBHOOK
 *   has moved the order.
 * The verify call cannot mark an order paid — the browser is
 * attacker-controlled — so success here means "keep waiting", not "done".
 */
import { useCallback, useRef, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { api } from "./api.js";
import { config } from "./config.js";
import { openCheckout, type CheckoutSuccess } from "./razorpay.js";

/** How long to wait for the webhook before telling the customer to sit tight. */
const CONFIRM_POLL_INTERVAL_MS = 2000;
const CONFIRM_TIMEOUT_MS = 40000;

export type PaymentPhase =
  /** Nothing started, or the customer closed the sheet without paying. */
  | "idle"
  /** Loading Checkout and opening the sheet. */
  | "opening"
  /** Paid at Razorpay; we are waiting for the webhook to confirm. */
  | "confirming"
  /** The order left `pending_payment` — the webhook landed. */
  | "paid"
  /**
   * The payment went through but the webhook has not arrived yet. NOT a
   * failure: the money is captured and the order will move on its own.
   */
  | "slow"
  /** Checkout could not open, or the attempt failed. Retryable. */
  | "error";

/** The order fields this flow needs. Both screens already fetch them. */
export interface PayableOrder {
  id: string;
  order_number: string;
  status: string;
  razorpay_order_id?: string;
  razorpay_key_id?: string;
}

interface OrderStatusResponse {
  status: string;
  payment_status?: string;
}

/**
 * What to prefill the sheet with. Every field is optional — Razorpay asks for
 * whatever is missing, and a wrong guess costs the customer more typing than
 * an empty field does.
 */
export interface PaymentPrefill {
  name?: string | undefined;
  email?: string | undefined;
  contact?: string | undefined;
}

export interface PaymentState {
  phase: PaymentPhase;
  /** Set when `phase` is "error"; safe to show to a customer. */
  message: string | null;
  /** True while the sheet is open or we are polling — disables the button. */
  busy: boolean;
  pay(order: PayableOrder, prefill?: PaymentPrefill): void;
  reset(): void;
}

const sleep = (ms: number): Promise<void> =>
  new Promise((resolve) => setTimeout(resolve, ms));

/**
 * True once an order is no longer waiting for money. `paid` is the happy
 * path; the rest are terminal and the caller's status copy explains them.
 */
function settled(status: string): boolean {
  return status !== "pending_payment";
}

export function usePayment(): PaymentState {
  const [phase, setPhase] = useState<PaymentPhase>("idle");
  const [message, setMessage] = useState<string | null>(null);
  const queryClient = useQueryClient();

  /**
   * Guards against a second sheet. A ref, not state: two clicks in the same
   * tick would both read the old state value and both open Checkout.
   */
  const inFlight = useRef(false);

  const refreshCaches = useCallback(
    (orderId: string) => {
      void queryClient.invalidateQueries({ queryKey: ["my-order", orderId] });
      void queryClient.invalidateQueries({ queryKey: ["my-orders"] });
    },
    [queryClient],
  );

  /**
   * Polls our own order until the webhook has moved it. The webhook is the
   * only thing that can, so this asks the server rather than believing the
   * browser's success callback.
   */
  const awaitWebhook = useCallback(
    async (orderId: string): Promise<void> => {
      const deadline = Date.now() + CONFIRM_TIMEOUT_MS;

      while (Date.now() < deadline) {
        await sleep(CONFIRM_POLL_INTERVAL_MS);
        try {
          const order = await api.get<OrderStatusResponse>(`/orders/${orderId}`);
          if (settled(order.status)) {
            setPhase("paid");
            refreshCaches(orderId);
            return;
          }
        } catch {
          // A blip while polling is not a payment failure. Keep trying until
          // the deadline; the "slow" copy below is the honest fallback.
        }
      }

      setPhase("slow");
      refreshCaches(orderId);
    },
    [refreshCaches],
  );

  const pay = useCallback(
    (order: PayableOrder, prefill: PaymentPrefill = {}) => {
      if (inFlight.current) return;

      if (!order.razorpay_order_id) {
        setPhase("error");
        setMessage(
          "We could not reach the payment provider when this order was placed. " +
            `Please write to ${config.supportEmail} and we'll sort it out.`,
        );
        return;
      }

      inFlight.current = true;
      setPhase("opening");
      setMessage(null);

      void openCheckout({
        // The server's key id wins over the build-time one: they are the same
        // value in a correct deployment, but the order was created under the
        // server's, and opening Checkout with any other id fails.
        keyId: order.razorpay_key_id ?? config.razorpayKeyId,
        razorpayOrderId: order.razorpay_order_id,
        orderNumber: order.order_number,
        merchantName: config.appName,
        prefill,
        onSuccess: (response: CheckoutSuccess) => {
          setPhase("confirming");
          // Fire-and-forget: the verify endpoint is a hint, so a failure here
          // must not stop us waiting for the webhook that actually decides.
          void api
            .post(`/orders/${order.id}/verify-payment`, response)
            .catch(() => undefined)
            .then(() => awaitWebhook(order.id))
            .finally(() => {
              inFlight.current = false;
            });
        },
        onDismiss: () => {
          inFlight.current = false;
          setPhase("idle");
        },
        onError: (text: string) => {
          inFlight.current = false;
          setPhase("error");
          setMessage(text);
        },
      });
    },
    [awaitWebhook],
  );

  const reset = useCallback(() => {
    setPhase("idle");
    setMessage(null);
  }, []);

  return {
    phase,
    message,
    busy: phase === "opening" || phase === "confirming",
    pay,
    reset,
  };
}

/** Shared copy, so checkout and the order page word the same state alike. */
export const PAYMENT_COPY: Record<Exclude<PaymentPhase, "idle" | "error">, string> = {
  opening: "Opening the payment sheet…",
  confirming: "Confirming your payment with the bank…",
  paid: "Payment confirmed. Your order is on its way through.",
  slow:
    "Your payment went through. The confirmation is taking a little longer " +
    "than usual — this order will update on its own, and we'll email you.",
};

/**
 * The line to show for a phase, or null when there is nothing to say — `idle`
 * has no message and `error` carries its own. A function rather than a direct
 * index so the two phases without copy are handled at the type level instead
 * of by every caller remembering.
 */
export function paymentCopy(phase: PaymentPhase): string | null {
  return phase === "idle" || phase === "error" ? null : PAYMENT_COPY[phase];
}
