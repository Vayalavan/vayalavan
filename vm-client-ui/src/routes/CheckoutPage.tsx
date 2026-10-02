/**
 * Checkout: pick a delivery address, confirm the breakdown, pay.
 *
 * Placing the order and paying for it are two steps on the server and one step
 * for the customer: POST /orders reserves stock and registers a Razorpay order
 * (CLAUDE.md §6.4 step 1), and the payment sheet opens on top of the result.
 * Nothing here can mark an order paid — the sheet's success callback only
 * starts us polling, and the WEBHOOK decides (§6.4 step 4).
 *
 * An order whose sheet is dismissed stays in `pending_payment` with its stock
 * held for RESERVATION_TTL_MINUTES, and can be paid from the order page until
 * the sweeper releases it. That is why this screen never says "order complete"
 * before the server says so.
 */
import { useRef, useState, type ReactElement } from "react";
import { Link, useNavigate } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Card, PageHeading, gradedName, type ApiError } from "@vayal/ui-kit";
import { api } from "../lib/api.js";
import { AddressForm, type Address } from "../components/AddressForm.js";
import { SchedulePlanner } from "../components/SchedulePlanner.js";
import { useAuth } from "../lib/auth.js";
import { CART_KEY, useCart } from "../lib/cart.js";
import {
  paymentCopy,
  usePayment,
  type PayableOrder,
  type PaymentState,
} from "../lib/payment.js";

interface PlacedOrder extends PayableOrder {
  total_display?: string;
  total_paise?: number;
}

export function CheckoutPage(): ReactElement {
  const { cart } = useCart();
  const { user } = useAuth();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const payment = usePayment();

  const [addressId, setAddressId] = useState<string | null>(null);
  const [placed, setPlaced] = useState<PlacedOrder | null>(null);
  /**
   * Whether the new-address form is open here.
   *
   * Adding one from checkout rather than sending the customer to their account
   * page: they arrived with a full cart, and a round trip through another
   * route is where a checkout gets abandoned.
   */
  const [addingAddress, setAddingAddress] = useState(false);
  /**
   * Pay now for the next delivery, or schedule it — once on a later date, or
   * on repeat — paid from the wallet (CLAUDE.md §6.7).
   */
  const [mode, setMode] = useState<"now" | "schedule">("now");

  /**
   * One key per checkout attempt, generated before the first send and reused
   * on every retry (CLAUDE.md rule 6). Generating it inside the mutation would
   * defeat the purpose: a double-tap or a retry after a timeout would each get
   * a fresh key and place a second real order.
   */
  const idempotencyKey = useRef<string>(crypto.randomUUID());

  const addresses = useQuery<{ addresses: Address[] }, ApiError>({
    queryKey: ["addresses"],
    queryFn: () => api.get<{ addresses: Address[] }>("/addresses"),
  });

  const chosenId =
    addressId ??
    addresses.data?.addresses.find((a) => a.is_default)?.id ??
    addresses.data?.addresses[0]?.id ??
    null;

  const chosenAddress =
    addresses.data?.addresses.find((a) => a.id === chosenId) ?? null;

  const placeOrder = useMutation<PlacedOrder, ApiError, string>({
    mutationFn: (id) =>
      api.post<PlacedOrder>("/orders", { address_id: id }, {
        idempotencyKey: idempotencyKey.current,
      }),
    onSuccess: (order) => {
      setPlaced(order);
      // The server emptied the cart into the order; a stale cached cart would
      // otherwise still show a checkout button for items already committed.
      void queryClient.invalidateQueries({ queryKey: CART_KEY });

      // Straight into the payment sheet — the customer pressed "Pay", so a
      // second button between them and Razorpay would only be a step to miss.
      // Retries live on the result screen below.
      payment.pay(order, {
        // The recipient is who the produce is for; the account holder is who
        // is paying. Razorpay wants the payer, with the delivery phone as the
        // better contact guess (an account can be signed in on a shared
        // handset).
        name: user?.name ?? chosenAddress?.recipient_name ?? undefined,
        email: user?.email ?? undefined,
        contact: chosenAddress?.phone ?? user?.phone ?? undefined,
      });
    },
  });

  if (placed) {
    return (
      <OrderPlaced
        order={placed}
        payment={payment}
        onRetry={() =>
          payment.pay(placed, {
            name: user?.name ?? undefined,
            email: user?.email ?? undefined,
            contact: user?.phone ?? undefined,
          })
        }
      />
    );
  }

  const data = cart.data;

  if (cart.isPending || addresses.isPending) {
    return <p className="text-sm text-primary-900/60">Loading checkout…</p>;
  }

  if (!data || data.items.length === 0) {
    return (
      <Card>
        <h1 className="font-semibold text-primary-900">Your cart is empty</h1>
        <p className="mt-1 text-sm text-primary-900/70">
          Add something from today&rsquo;s produce to check out.
        </p>
        <Link
          to="/"
          className="mt-4 inline-block rounded-card bg-primary-600 px-4 py-2 text-sm font-medium text-white"
        >
          Browse today&rsquo;s produce
        </Link>
      </Card>
    );
  }

  const noAddresses = (addresses.data?.addresses.length ?? 0) === 0;

  return (
    <>
      <PageHeading
        title="Checkout"
        description="Confirm where this should go and what you're paying."
      />

      <div className="grid gap-6 lg:grid-cols-[1fr_360px]">
        <div className="space-y-6">
          <Card>
            <div className="flex items-center justify-between gap-3">
              <h2 className="font-semibold text-primary-900">Delivery address</h2>
              {!noAddresses && !addingAddress && (
                <button
                  type="button"
                  onClick={() => setAddingAddress(true)}
                  className="rounded-card border border-surface-border px-3 py-1.5 text-sm font-medium text-primary-800 hover:bg-primary-50"
                >
                  Add a new address
                </button>
              )}
            </div>

            {noAddresses && !addingAddress ? (
              <div className="mt-2">
                <p className="text-sm text-primary-900/70">
                  You have no saved addresses yet. Add one to continue.
                </p>
                <button
                  type="button"
                  onClick={() => setAddingAddress(true)}
                  className="mt-3 rounded-card bg-primary-600 px-4 py-2 text-sm font-medium text-white hover:bg-primary-700"
                >
                  Add a delivery address
                </button>
              </div>
            ) : (
              <fieldset className="mt-3 space-y-2">
                <legend className="sr-only">Choose a delivery address</legend>
                {addresses.data?.addresses.map((address) => (
                  <label
                    key={address.id}
                    className={`flex cursor-pointer gap-3 rounded-card border p-3 ${
                      chosenId === address.id
                        ? "border-primary-600 bg-primary-50"
                        : "border-surface-border hover:border-primary-400"
                    }`}
                  >
                    <input
                      type="radio"
                      name="address"
                      value={address.id}
                      checked={chosenId === address.id}
                      onChange={() => setAddressId(address.id)}
                      className="mt-1"
                    />
                    <span className="text-sm">
                      <span className="block font-medium text-primary-900">
                        {address.label ?? "Address"} &middot; {address.recipient_name}
                      </span>
                      <span className="block text-primary-900/70">
                        {[address.line1, address.line2, address.landmark]
                          .filter(Boolean)
                          .join(", ")}
                      </span>
                      <span className="block text-primary-900/70">
                        {address.city}, {address.state} {address.pincode}
                      </span>
                      <span className="block text-primary-900/60">{address.phone}</span>
                    </span>
                  </label>
                ))}
              </fieldset>
            )}

            {addingAddress && (
              <div className="mt-3 border-t border-surface-border pt-3">
                <AddressForm
                  framed={false}
                  heading="New delivery address"
                  submitLabel="Save and use this address"
                  onDone={() => setAddingAddress(false)}
                  // Selected as soon as it is saved: the customer added it to
                  // deliver to it, so making them pick it off the list after
                  // would be asking the same question twice.
                  onSaved={(address: Address) => setAddressId(address.id)}
                />
              </div>
            )}
          </Card>

          <Card>
            <h2 className="font-semibold text-primary-900">Your items</h2>
            <ul className="mt-3 divide-y divide-surface-border">
              {data.items.map((item) => (
                <li key={item.id} className="flex justify-between gap-3 py-2 text-sm">
                  <span className="min-w-0">
                    <span className="block truncate font-medium text-primary-900">
                      {/* The GRADE, beside the name: two lines of one produce
                          at different grades are different goods at different
                          prices, and a checkout that shows them identically is
                          asking the customer to pay for a guess. */}
                      {gradedName(item.product_name, item.size_code)}
                    </span>
                    <span className="text-primary-900/60">
                      {item.unit_label} &times; {item.qty}
                    </span>
                    {item.exceeds_stock && (
                      <span className="block text-xs font-medium text-accent-800">
                        {item.available_display
                          ? `Only ${item.available_display} left today`
                          : "More than is left today"}
                      </span>
                    )}
                  </span>
                  <span className="shrink-0 font-medium text-primary-900">
                    {item.line_total_display}
                  </span>
                </li>
              ))}
            </ul>
          </Card>
        </div>

        <div className="space-y-4 lg:sticky lg:top-24 lg:self-start">
          {/* Now or later. A segmented control rather than two buttons at the
              foot: the choice changes what the whole panel asks for. */}
          <div
            role="radiogroup"
            aria-label="When should this arrive?"
            className="grid grid-cols-2 gap-1 rounded-2xl bg-cream-200/70 p-1"
          >
            {(
              [
                ["now", "Deliver now", "Pay today"],
                ["schedule", "Schedule / Repeat", "Paid from wallet"],
              ] as const
            ).map(([value, label, hint]) => (
              <button
                key={value}
                type="button"
                role="radio"
                aria-checked={mode === value}
                onClick={() => setMode(value)}
                className={`rounded-xl px-3 py-2.5 text-left transition ${
                  mode === value
                    ? "bg-surface-raised shadow-card"
                    : "text-primary-900/60 hover:text-primary-900"
                }`}
              >
                <span className="block text-sm font-semibold text-primary-900">{label}</span>
                <span className="block text-[11px] text-primary-900/55">{hint}</span>
              </button>
            ))}
          </div>

          {mode === "schedule" ? (
            <Card className="!rounded-3xl !border-cream-300/70">
              <SchedulePlanner
                addressId={chosenId}
                totalPaise={data.total_paise}
                disabled={data.items.length === 0}
              />
            </Card>
          ) : (
          <Card>
            <h2 className="font-semibold text-primary-900">Order summary</h2>
            <dl className="mt-3 space-y-1 text-sm">
              <div className="flex justify-between">
                <dt className="text-primary-900/70">Subtotal</dt>
                <dd className="text-primary-900">{data.subtotal_display}</dd>
              </div>
              <div className="flex justify-between">
                <dt className="text-primary-900/70">Platform fee (3%)</dt>
                <dd className="text-primary-900">{data.platform_fee_display}</dd>
              </div>
              <div className="flex justify-between">
                <dt className="text-primary-900/70">Delivery charge</dt>
                <dd className="text-primary-900">{data.delivery_fee_display}</dd>
              </div>
              <div className="mt-2 flex justify-between border-t border-surface-border pt-2 text-base font-semibold">
                <dt className="text-primary-900">Total</dt>
                <dd className="text-primary-900">{data.total_display}</dd>
              </div>
            </dl>

            {placeOrder.isError && (
              <p role="alert" className="mt-3 rounded-card bg-accent-50 px-3 py-2 text-sm text-accent-800">
                {placeOrder.error.message}
              </p>
            )}

            {/* Why the button below is off. A disabled Place order with no
                explanation is the worst version of running out of stock: the
                customer can see the total and cannot see the problem. The
                messages are whole sentences from the server, naming the
                produce. */}
            {!data.checkoutable && data.changes.length > 0 && (
              <div
                role="alert"
                className="mt-3 rounded-card border border-accent-500/40 bg-accent-50 p-3"
              >
                <p className="text-sm font-medium text-accent-800">
                  Your cart needs a change before you can order
                </p>
                <ul className="mt-1 space-y-1">
                  {data.changes.map((change) => (
                    <li key={change.cart_item_id} className="text-sm text-accent-800/90">
                      {change.message}
                    </li>
                  ))}
                </ul>
              </div>
            )}

            <button
              type="button"
              disabled={
                !data.checkoutable ||
                !chosenId ||
                placeOrder.isPending ||
                placeOrder.isSuccess
              }
              onClick={() => chosenId && placeOrder.mutate(chosenId)}
              className="mt-4 w-full rounded-card bg-primary-600 px-4 py-2.5 font-medium text-white hover:bg-primary-700 disabled:cursor-not-allowed disabled:bg-surface-sunken disabled:text-primary-900/40"
            >
              {placeOrder.isPending
                ? "Placing order…"
                : `Pay ${data.total_display}`}
            </button>

            {/* Said before the click, not after: the next thing on screen is
                a payment sheet, and the amount on the button is the amount
                that will be charged. */}
            <p className="mt-3 text-xs text-primary-900/60">
              You&rsquo;ll pay securely via Razorpay. Your produce is reserved
              while you complete payment.
            </p>
          </Card>
          )}

          <button
            type="button"
            onClick={() => navigate("/")}
            className="w-full rounded-card border border-surface-border px-4 py-2 text-sm font-medium text-primary-800"
          >
            Keep shopping
          </button>
        </div>
      </div>
    </>
  );
}

/**
 * The result screen, which must not overstate what happened.
 *
 * Four outcomes reach here and only one of them is "done":
 *   paid       — the webhook confirmed it; say so.
 *   slow       — money captured, webhook late; reassure, do not ask to re-pay.
 *   confirming — still polling; a spinner, not a tick.
 *   idle/error — the sheet closed unpaid; offer the sheet again while the
 *                reservation lasts.
 * The tick mark is drawn only in the first two cases: a green check over
 * "awaiting payment" is the exact thing that makes someone stop watching for
 * a payment they still owe.
 */
function OrderPlaced({
  order,
  payment,
  onRetry,
}: {
  order: PlacedOrder;
  payment: PaymentState;
  onRetry: () => void;
}): ReactElement {
  const settled = payment.phase === "paid" || payment.phase === "slow";
  const working = payment.phase === "opening" || payment.phase === "confirming";

  return (
    <Card className="mx-auto max-w-lg text-center">
      {settled ? (
        <div className="mx-auto flex h-12 w-12 items-center justify-center rounded-full bg-primary-600 text-white">
          <svg viewBox="0 0 12 12" className="h-6 w-6" fill="none" aria-hidden="true">
            <path
              d="M2.5 6.5 5 9l4.5-5"
              stroke="currentColor"
              strokeWidth="2"
              strokeLinecap="round"
              strokeLinejoin="round"
            />
          </svg>
        </div>
      ) : (
        <div className="mx-auto flex h-12 w-12 items-center justify-center rounded-full bg-accent-100 text-accent-800">
          <svg viewBox="0 0 12 12" className="h-6 w-6" fill="none" aria-hidden="true">
            <circle cx="6" cy="6" r="4.5" stroke="currentColor" strokeWidth="1.5" />
            <path
              d="M6 3.5V6l1.75 1.25"
              stroke="currentColor"
              strokeWidth="1.5"
              strokeLinecap="round"
            />
          </svg>
        </div>
      )}

      <h1 className="mt-4 text-xl font-semibold text-primary-900">
        {payment.phase === "paid" ? "Payment received" : "Order placed"}
      </h1>
      <p className="mt-1 text-sm text-primary-900/70">
        Your order number is{" "}
        <span className="font-semibold text-primary-900">{order.order_number}</span>.
      </p>

      <div
        className="mt-4 rounded-card bg-surface-sunken p-4 text-left text-sm text-primary-900/70"
        aria-live="polite"
      >
        {working && (
          <p className="font-medium text-primary-900">
            {paymentCopy(payment.phase)}
          </p>
        )}

        {settled && (
          <p className="text-primary-900/80">{paymentCopy(payment.phase)}</p>
        )}

        {!settled && !working && (
          <>
            <p className="font-medium text-primary-900">Payment not completed</p>
            <p className="mt-1">
              {payment.message ??
                "You closed the payment window before it finished."}{" "}
              Your produce is reserved for a short while — pay now and the order
              goes through. If we don&rsquo;t hear from you the reservation is
              released and the stock returns to today&rsquo;s catalog.
            </p>
            <button
              type="button"
              onClick={onRetry}
              disabled={payment.busy}
              className="mt-3 w-full rounded-card bg-primary-600 px-4 py-2 text-sm font-medium text-white hover:bg-primary-700 disabled:bg-surface-sunken disabled:text-primary-900/40"
            >
              Pay now
            </button>
          </>
        )}
      </div>

      <div className="mt-5 flex flex-wrap justify-center gap-3">
        <Link
          to={`/orders/${order.id}`}
          className="rounded-card bg-primary-600 px-4 py-2 text-sm font-medium text-white hover:bg-primary-700"
        >
          View this order
        </Link>
        <Link
          to="/"
          className="rounded-card border border-surface-border px-4 py-2 text-sm font-medium text-primary-800"
        >
          Back to today&rsquo;s produce
        </Link>
      </div>
    </Card>
  );
}
