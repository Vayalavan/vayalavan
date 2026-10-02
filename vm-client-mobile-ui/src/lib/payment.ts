/**
 * Paying for an order, from either checkout or the order screen.
 *
 * The mirror of vm-client-ui's `src/lib/payment.ts`. The two apps are separate
 * packages and cannot share code (CLAUDE.md §2), but they must not disagree
 * about what a payment outcome MEANS — so the phases, the copy and the
 * webhook-polling rule are kept deliberately identical, and only the sheet
 * itself differs (a modal WebView here, a script there).
 *
 * The flow (CLAUDE.md §6.4):
 *   open the sheet -> verify the signature (a UX HINT) -> poll until the
 *   WEBHOOK has moved the order.
 * Nothing on the phone can mark an order paid.
 */
import { useCallback, useRef, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";

import { api } from "./client";
import { config } from "./config";
import { type PaymentPhase } from "./paymentCopy";
import type { CheckoutMessage, CheckoutPageOptions } from "./razorpayHtml";
import type { Order } from "./types";

/** How long to wait for the webhook before telling the customer to sit tight. */
const CONFIRM_POLL_INTERVAL_MS = 2000;
const CONFIRM_TIMEOUT_MS = 40000;



/** The order fields this flow needs. A subset of `Order`. */
export interface PayableOrder {
  id: string;
  order_number: string;
  status: string;
  razorpay_order_id?: string;
  razorpay_key_id?: string;
}

export interface PaymentPrefill {
  name?: string | undefined;
  email?: string | undefined;
  contact?: string | undefined;
}

export interface PaymentState {
  phase: PaymentPhase;
  /** Set when `phase` is "error"; safe to show to a customer. */
  message: string | null;
  /** True while the sheet is open or we are polling. */
  busy: boolean;
  /** Non-null while the sheet should be on screen. Pass to RazorpayCheckout. */
  request: Omit<CheckoutPageOptions, "themeColor"> | null;
  /** Hand RazorpayCheckout's onResult straight to this. */
  handleResult(message: CheckoutMessage): void;
  pay(order: PayableOrder, prefill?: PaymentPrefill): void;
  reset(): void;
}

const sleep = (ms: number): Promise<void> =>
  new Promise((resolve) => setTimeout(resolve, ms));

/** True once an order is no longer waiting for money. */
function settled(status: string): boolean {
  return status !== "pending_payment";
}

export function usePayment(): PaymentState {
  const [phase, setPhase] = useState<PaymentPhase>("idle");
  const [message, setMessage] = useState<string | null>(null);
  const [request, setRequest] =
    useState<Omit<CheckoutPageOptions, "themeColor"> | null>(null);
  const queryClient = useQueryClient();

  /** The order the open sheet belongs to, needed when its result arrives. */
  const paying = useRef<PayableOrder | null>(null);

  const refreshCaches = useCallback(
    (orderId: string) => {
      void queryClient.invalidateQueries({ queryKey: ["my-order", orderId] });
      void queryClient.invalidateQueries({ queryKey: ["my-orders"] });
    },
    [queryClient],
  );

  /**
   * Polls our own order until the webhook has moved it — the webhook is the
   * only thing that can, so this asks the server rather than believing the
   * sheet's success callback.
   */
  const awaitWebhook = useCallback(
    async (orderId: string): Promise<void> => {
      const deadline = Date.now() + CONFIRM_TIMEOUT_MS;

      while (Date.now() < deadline) {
        await sleep(CONFIRM_POLL_INTERVAL_MS);
        try {
          const order = await api.get<Order>(`/orders/${orderId}`);
          if (settled(order.status)) {
            setPhase("paid");
            refreshCaches(orderId);
            return;
          }
        } catch {
          // A blip on a phone network is not a payment failure. Keep trying
          // until the deadline; "slow" below is the honest fallback.
        }
      }

      setPhase("slow");
      refreshCaches(orderId);
    },
    [refreshCaches],
  );

  const handleResult = useCallback(
    (result: CheckoutMessage) => {
      const order = paying.current;
      // Closing the sheet is what every outcome has in common.
      setRequest(null);

      if (!order) return;

      switch (result.type) {
        case "success":
          setPhase("confirming");
          // Fire-and-forget: the verify endpoint is a hint, so a failure here
          // must not stop us waiting for the webhook that actually decides.
          void api
            .post(`/orders/${order.id}/verify-payment`, {
              razorpay_order_id: result.razorpay_order_id,
              razorpay_payment_id: result.razorpay_payment_id,
              razorpay_signature: result.razorpay_signature,
            })
            .catch(() => undefined)
            .then(() => awaitWebhook(order.id));
          break;

        case "dismiss":
          // Not an error. The order keeps its reservation and can be paid
          // from the order screen until the TTL runs out (CLAUDE.md §6.3).
          paying.current = null;
          setPhase("idle");
          break;

        case "failed":
          paying.current = null;
          setPhase("error");
          setMessage(result.message);
          break;
      }
    },
    [awaitWebhook],
  );

  const pay = useCallback((order: PayableOrder, prefill: PaymentPrefill = {}) => {
    if (!order.razorpay_order_id || !order.razorpay_key_id) {
      setPhase("error");
      setMessage(
        "We could not reach the payment provider when this order was placed. " +
          `Please write to ${config.supportEmail} and we'll sort it out.`,
      );
      return;
    }

    paying.current = order;
    setPhase("opening");
    setMessage(null);
    setRequest({
      // The server's key id wins over any build-time value: the order was
      // created under it, and opening the sheet with another id fails. It is
      // also why this app needs no MOBILE_RAZORPAY_KEY_ID — a key rotated at
      // the server reaches an already-installed binary on its own.
      keyId: order.razorpay_key_id,
      razorpayOrderId: order.razorpay_order_id,
      orderNumber: order.order_number,
      merchantName: config.appName,
      prefill,
    });
  }, []);

  const reset = useCallback(() => {
    paying.current = null;
    setRequest(null);
    setPhase("idle");
    setMessage(null);
  }, []);

  return {
    phase,
    message,
    busy: phase === "opening" || phase === "confirming",
    request,
    handleResult,
    pay,
    reset,
  };
}

// Re-exported so screens have one import for the whole payment surface.
export { PAYMENT_COPY, paymentCopy, type PaymentPhase } from "./paymentCopy";
