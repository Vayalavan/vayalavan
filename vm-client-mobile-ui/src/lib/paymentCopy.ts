/**
 * Payment phases and the words for them.
 *
 * Split out of `payment.ts` so it can be imported without React Native: the
 * test that binds this wording to vm-client-ui's runs in plain Node, and the
 * hook next door pulls in the whole app. Nothing here has a dependency.
 */
export type PaymentPhase =
  /** Nothing started, or the customer closed the sheet without paying. */
  | "idle"
  /** The sheet is open. */
  | "opening"
  /** Paid at Razorpay; waiting for the webhook to confirm. */
  | "confirming"
  /** The order left `pending_payment` — the webhook landed. */
  | "paid"
  /**
   * The payment went through but the webhook has not arrived. NOT a failure:
   * the money is captured and the order moves on its own.
   */
  | "slow"
  /** The sheet could not open, or the attempt failed. Retryable. */
  | "error";

/** Shared copy, so checkout and the order screen word the same state alike. */
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
 * has no message and `error` carries its own.
 */
export function paymentCopy(phase: PaymentPhase): string | null {
  return phase === "idle" || phase === "error" ? null : PAYMENT_COPY[phase];
}
