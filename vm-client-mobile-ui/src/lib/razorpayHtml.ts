/**
 * The page the payment WebView loads (CLAUDE.md §6.4 step 2, on a phone).
 *
 * Why a WebView and not Razorpay's native SDK: the native SDK is a native
 * module, and native modules are not in Expo Go. Adopting it would cost the
 * ability to test payments on a real handset without an EAS development build
 * — the exact trade CLAUDE.md §3 pins SDK 54 to avoid. Standard Checkout in a
 * WebView is the same sheet the web storefront opens, so the two surfaces also
 * cannot drift in what a customer is shown while paying.
 *
 * This module is deliberately pure — a string in, a string out — so the
 * escaping below can be tested without a renderer.
 */

/** Where the sheet's script comes from, and the origin the page runs as. */
export const CHECKOUT_ORIGIN = "https://checkout.razorpay.com";
const CHECKOUT_SCRIPT_URL = `${CHECKOUT_ORIGIN}/v1/checkout.js`;

/** The message types the WebView posts back. Mirrored by the component. */
export type CheckoutMessage =
  | {
      type: "success";
      razorpay_order_id: string;
      razorpay_payment_id: string;
      razorpay_signature: string;
    }
  | { type: "dismiss" }
  | { type: "failed"; message: string };

export interface CheckoutPageOptions {
  /** Public key id — the one the SERVER returned with the order. */
  keyId: string;
  razorpayOrderId: string;
  orderNumber: string;
  merchantName: string;
  /** The brand colour for the sheet, from theme tokens — never a literal. */
  themeColor: string;
  prefill: {
    name?: string | undefined;
    email?: string | undefined;
    contact?: string | undefined;
  };
}

/**
 * Serialises a value for embedding inside a <script> block.
 *
 * JSON.stringify alone is NOT safe here: a value containing `</script>` would
 * close the block and everything after it would be parsed as HTML. The order
 * number and merchant name are ours, but the prefill carries a customer's own
 * name — user-controlled text on its way into a script tag, which is precisely
 * the case that has to be escaped rather than trusted.
 *
 * U+2028/U+2029 are escaped too: they are valid in JSON strings but are line
 * terminators in JavaScript, so an unescaped one is a syntax error.
 */
export function jsonForScript(value: unknown): string {
  return JSON.stringify(value ?? null)
    .replace(/</g, "\\u003c")
    .replace(/>/g, "\\u003e")
    .replace(/&/g, "\\u0026")
    .replace(/\u2028/g, "\\u2028")
    .replace(/\u2029/g, "\\u2029");
}

/**
 * Builds the page.
 *
 * Amount and currency are deliberately absent: Checkout reads them from the
 * provider order, so nothing on the phone can ask to be charged a different
 * number than the one vm-orders-api registered.
 */
export function buildCheckoutPage(options: CheckoutPageOptions): string {
  const payload = jsonForScript({
    key: options.keyId,
    order_id: options.razorpayOrderId,
    name: options.merchantName,
    description: `Order ${options.orderNumber}`,
    prefill: {
      name: options.prefill.name ?? "",
      email: options.prefill.email ?? "",
      contact: options.prefill.contact ?? "",
    },
    notes: { order_number: options.orderNumber },
    theme: { color: options.themeColor },
  });

  return `<!doctype html>
<html>
  <head>
    <meta charset="utf-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1, maximum-scale=1, user-scalable=no" />
    <style>
      /* The sheet draws its own surface; this only avoids a white flash
         behind it while the script loads. */
      html, body { margin: 0; height: 100%; background: ${options.themeColor}; }
    </style>
  </head>
  <body>
    <script src="${CHECKOUT_SCRIPT_URL}"></script>
    <script>
      (function () {
        var options = ${payload};

        function post(message) {
          if (window.ReactNativeWebView) {
            window.ReactNativeWebView.postMessage(JSON.stringify(message));
          }
        }

        /* One outcome per attempt. Razorpay can fire payment.failed and then
           ondismiss as the sheet closes; without this the app would show an
           error and then silently reset to "not paid". */
        var done = false;
        function settle(message) {
          if (done) return;
          done = true;
          post(message);
        }

        if (!window.Razorpay) {
          settle({
            type: "failed",
            message: "We could not reach the payment provider. Check your connection and try again."
          });
          return;
        }

        options.handler = function (response) {
          settle({
            type: "success",
            razorpay_order_id: response.razorpay_order_id,
            razorpay_payment_id: response.razorpay_payment_id,
            razorpay_signature: response.razorpay_signature
          });
        };
        options.modal = {
          escape: false,
          ondismiss: function () { settle({ type: "dismiss" }); }
        };

        try {
          var checkout = new window.Razorpay(options);
          checkout.on("payment.failed", function (payload) {
            var error = payload && payload.error;
            settle({
              type: "failed",
              message: (error && error.description) ||
                "That payment did not go through. You can try again."
            });
          });
          checkout.open();
        } catch (err) {
          settle({
            type: "failed",
            message: "The payment sheet could not be opened. Please try again."
          });
        }
      })();
    </script>
  </body>
</html>`;
}

/**
 * Parses a message posted by the page above.
 *
 * Returns null for anything unrecognised. A WebView can be handed content we
 * did not author — a bank's 3-D Secure page posting its own messages, say — so
 * this validates rather than casts.
 */
export function parseCheckoutMessage(raw: string): CheckoutMessage | null {
  let value: unknown;
  try {
    value = JSON.parse(raw);
  } catch {
    return null;
  }
  if (typeof value !== "object" || value === null) return null;

  const message = value as Record<string, unknown>;
  switch (message.type) {
    case "success":
      if (
        typeof message.razorpay_order_id === "string" &&
        typeof message.razorpay_payment_id === "string" &&
        typeof message.razorpay_signature === "string"
      ) {
        return {
          type: "success",
          razorpay_order_id: message.razorpay_order_id,
          razorpay_payment_id: message.razorpay_payment_id,
          razorpay_signature: message.razorpay_signature,
        };
      }
      return null;
    case "dismiss":
      return { type: "dismiss" };
    case "failed":
      return {
        type: "failed",
        message:
          typeof message.message === "string" && message.message !== ""
            ? message.message
            : "That payment did not go through. You can try again.",
      };
    default:
      return null;
  }
}
