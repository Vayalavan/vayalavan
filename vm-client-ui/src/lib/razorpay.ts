/**
 * Razorpay Standard Checkout, loaded on demand (CLAUDE.md §6.4 step 2).
 *
 * The script is fetched the first time someone actually pays rather than from
 * index.html: it is ~100KB of third-party JavaScript that every visitor
 * browsing today's produce would otherwise download, on the mid-range Android
 * handsets most of our customers shop from.
 *
 * Nothing here is a secret. The key ID is public by design and the key SECRET
 * never leaves vm-orders-api — the browser proves nothing, which is why the
 * webhook, not this file, is what marks an order paid.
 */

const CHECKOUT_SCRIPT_URL = "https://checkout.razorpay.com/v1/checkout.js";

/** What Checkout hands back on a successful payment. */
export interface CheckoutSuccess {
  razorpay_order_id: string;
  razorpay_payment_id: string;
  razorpay_signature: string;
}

/** The subset of Razorpay's failure payload we can act on. */
interface CheckoutFailure {
  error?: { description?: string; reason?: string };
}

interface RazorpayInstance {
  open(): void;
  on(event: "payment.failed", handler: (payload: CheckoutFailure) => void): void;
}

interface RazorpayConstructor {
  new (options: Record<string, unknown>): RazorpayInstance;
}

declare global {
  interface Window {
    Razorpay?: RazorpayConstructor;
  }
}

/**
 * Cached across calls so a customer who dismisses Checkout and retries does
 * not re-download the script — and so two rapid clicks cannot inject two
 * script tags.
 */
let loader: Promise<RazorpayConstructor> | null = null;

function loadCheckoutScript(): Promise<RazorpayConstructor> {
  if (window.Razorpay) return Promise.resolve(window.Razorpay);
  if (loader) return loader;

  loader = new Promise<RazorpayConstructor>((resolve, reject) => {
    const script = document.createElement("script");
    script.src = CHECKOUT_SCRIPT_URL;
    script.async = true;
    script.onload = () => {
      if (window.Razorpay) {
        resolve(window.Razorpay);
        return;
      }
      reject(new Error("Razorpay Checkout loaded but did not initialise."));
    };
    script.onerror = () => {
      // Let the next attempt retry from scratch: a rejected promise cached
      // forever would make one flaky network moment permanent for the session.
      loader = null;
      script.remove();
      reject(
        new Error(
          "We could not reach the payment provider. Check your connection and try again.",
        ),
      );
    };
    document.head.appendChild(script);
  });

  return loader;
}

export interface OpenCheckoutArgs {
  /** Public key id. Prefer the one the SERVER returned with the order. */
  keyId: string;
  /** The provider order the payment is made against. */
  razorpayOrderId: string;
  orderNumber: string;
  /** The name shown at the top of the sheet. From config, not a literal. */
  merchantName: string;
  /** Prefills the sheet so the customer retypes as little as possible. */
  prefill: {
    name?: string | undefined;
    email?: string | undefined;
    contact?: string | undefined;
  };
  onSuccess(response: CheckoutSuccess): void;
  /** The customer closed the sheet without paying. Not an error. */
  onDismiss(): void;
  /** Checkout could not open, or the payment attempt itself failed. */
  onError(message: string): void;
}

/**
 * The brand colour for the payment sheet.
 *
 * Read from the CSS custom property the ui-kit sets (`--vayal-primary`) rather
 * than written as a hex here: the sheet is Razorpay's DOM, so a Tailwind class
 * cannot reach it, but CLAUDE.md §9 still says a brand change must be one edit
 * in the preset. The fallback is only for the case where the stylesheet has
 * not applied — a wrong-coloured sheet beats no sheet.
 */
function brandColor(): string {
  const value = getComputedStyle(document.documentElement)
    .getPropertyValue("--vayal-primary")
    .trim();
  return value === "" ? "#2b6446" : value;
}

/**
 * Opens the payment sheet.
 *
 * Exactly one of the three callbacks fires per attempt. `onDismiss` is not a
 * failure — the order stays in `pending_payment` with its stock still
 * reserved, and the customer can pay from the order page until the TTL runs
 * out (CLAUDE.md §6.3).
 */
export async function openCheckout(args: OpenCheckoutArgs): Promise<void> {
  let Razorpay: RazorpayConstructor;
  try {
    Razorpay = await loadCheckoutScript();
  } catch (error) {
    args.onError(
      error instanceof Error ? error.message : "Payment could not be started.",
    );
    return;
  }

  // Amount and currency deliberately omitted: Checkout takes them from the
  // provider order, so the browser cannot ask to be charged a different
  // number than the one vm-orders-api registered.
  const checkout = new Razorpay({
    key: args.keyId,
    order_id: args.razorpayOrderId,
    name: args.merchantName,
    description: `Order ${args.orderNumber}`,
    prefill: args.prefill,
    notes: { order_number: args.orderNumber },
    theme: { color: brandColor() },
    // Razorpay calls this on the customer's own success path.
    handler: (response: CheckoutSuccess) => args.onSuccess(response),
    modal: { ondismiss: () => args.onDismiss() },
  });

  checkout.on("payment.failed", (payload) => {
    args.onError(
      payload.error?.description ??
        "That payment did not go through. You can try again.",
    );
  });

  checkout.open();
}
