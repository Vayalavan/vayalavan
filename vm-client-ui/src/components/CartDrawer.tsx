/**
 * The cart, as a slide-over panel.
 *
 * A panel rather than a page so adding a second item never costs the customer
 * their place in the catalog — on a phone, losing scroll position after every
 * add is what makes people give up on a basket.
 */
import { useCallback, useEffect, useRef, useState, type ReactElement } from "react";
import { useNavigate } from "react-router-dom";
import { Skeleton } from "@vayal/ui-kit";
import { useCart, useRemoveCartItem, useUpdateCartQty } from "../lib/cart.js";

/**
 * How long to wait after the last tap before sending the quantity.
 *
 * Long enough that holding "+" to reach 15 is one request instead of fifteen,
 * short enough to feel immediate. Without this, every tap fired a PATCH *and*
 * a cart refetch, so fifteen taps cost thirty requests and tripped the
 * gateway's rate limit — the UI attacking its own API.
 */
const QTY_DEBOUNCE_MS = 450;

export function CartDrawer(): ReactElement | null {
  const { cart, isOpen, close } = useCart();
  const navigate = useNavigate();
  const panelRef = useRef<HTMLDivElement>(null);
  const closeRef = useRef<HTMLButtonElement>(null);

  const updateQty = useUpdateCartQty();
  const removeItem = useRemoveCartItem();

  /**
   * Quantities the customer has tapped but which the server has not confirmed.
   *
   * Shown in place of the server's number so the counter responds instantly.
   * Cleared once the cart has been refetched, at which point the server's value
   * is authoritative again — it may differ, if stock ran out mid-edit.
   */
  const [draftQty, setDraftQty] = useState<Record<string, number>>({});
  const timers = useRef<Record<string, ReturnType<typeof setTimeout>>>({});

  // Any timer still pending when the panel unmounts would fire against a
  // cart the customer is no longer looking at.
  useEffect(() => {
    const pending = timers.current;
    return () => {
      for (const timer of Object.values(pending)) clearTimeout(timer);
    };
  }, []);

  const changeQty = useCallback(
    (itemId: string, next: number) => {
      if (next < 1) return;
      setDraftQty((current) => ({ ...current, [itemId]: next }));

      clearTimeout(timers.current[itemId]);
      timers.current[itemId] = setTimeout(() => {
        // The absolute quantity, not a delta — so collapsing ten taps into one
        // request still lands on exactly the number the customer chose.
        updateQty.mutate(
          { id: itemId, qty: next },
          {
            onSettled: () =>
              setDraftQty((current) => {
                const { [itemId]: _sent, ...rest } = current;
                return rest;
              }),
          },
        );
      }, QTY_DEBOUNCE_MS);
    },
    [updateQty],
  );

  // Totals on screen belong to the server's quantities. While an edit is in
  // flight they are stale, and checking out against a stale total would charge
  // the wrong amount.
  const syncing = Object.keys(draftQty).length > 0 || updateQty.isPending;

  // Escape closes, and the page behind must not scroll while a modal covers
  // it — otherwise flicking the panel scrolls the catalog underneath.
  useEffect(() => {
    if (!isOpen) return;
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key === "Escape") close();
    };
    document.addEventListener("keydown", onKeyDown);
    const previousOverflow = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    // Move focus into the panel so a keyboard user is not left behind on the
    // catalog, tabbing through content they can no longer see.
    closeRef.current?.focus();
    return () => {
      document.removeEventListener("keydown", onKeyDown);
      document.body.style.overflow = previousOverflow;
    };
  }, [isOpen, close]);

  if (!isOpen) return null;

  const data = cart.data;
  const busy = removeItem.isPending;

  // One error region, not three. The cart query and both mutations fail for
  // the same reasons, so rendering each separately printed the identical
  // sentence twice on screen.
  const error = updateQty.error ?? removeItem.error ?? (cart.isError ? cart.error : null);

  return (
    <div className="fixed inset-0 z-50 flex justify-end">
      {/* Scrim. A button, not a div, so it is reachable without a mouse. */}
      <button
        type="button"
        aria-label="Close cart"
        onClick={close}
        className="absolute inset-0 bg-primary-900/40 backdrop-blur-[1px]"
      />

      <div
        ref={panelRef}
        role="dialog"
        aria-modal="true"
        aria-label="Your cart"
        className="relative flex h-full w-full max-w-md flex-col bg-surface-raised shadow-card"
      >
        <div className="flex items-center justify-between border-b border-surface-border px-5 py-4">
          <h2 className="text-lg font-semibold text-primary-900">Your cart</h2>
          <button
            ref={closeRef}
            type="button"
            onClick={close}
            className="rounded-card px-2 py-1 text-sm font-medium text-primary-800 hover:bg-primary-50"
          >
            Close
          </button>
        </div>

        <div className="flex-1 overflow-y-auto px-5 py-4">
          {cart.isPending && (
            <div className="space-y-3">
              <Skeleton className="h-16 w-full" />
              <Skeleton className="h-16 w-full" />
            </div>
          )}

          {data && data.items.length === 0 && (
            <div className="py-10 text-center">
              <p className="font-medium text-primary-900">Your cart is empty</p>
              <p className="mt-1 text-sm text-primary-900/60">
                Add something from today&rsquo;s produce.
              </p>
            </div>
          )}

          {/* Re-pricing notice. Shown before the totals, because it explains
              them (CLAUDE.md §5.3). */}
          {data && data.changes.length > 0 && (
            <div className="mb-4 rounded-card border border-accent-500/40 bg-accent-50 p-3">
              <p className="text-sm font-medium text-accent-800">
                Some items changed since you added them
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

          {error && (
            <p role="alert" className="mb-3 rounded-card bg-accent-50 px-3 py-2 text-sm text-accent-800">
              {error.status === 429
                ? "That was a lot of changes at once. Give it a moment and try again."
                : error.message}
            </p>
          )}

          <ul className="space-y-3">
            {data?.items.map((item) => {
              const shownQty = draftQty[item.id] ?? item.qty;
              // Two different problems. Not purchasable means this pack size
              // does not fit at all today and has to go; exceeding means the
              // pack is fine and there are simply too many, and max_qty is the
              // number that fixes it.
              const overLimit = item.exceeds_stock && item.purchasable;
              // A pack that is not on today's shelf at all — undeclared,
              // archived, or gone. Nothing about it can be priced or counted,
              // so the line offers one action: remove.
              const gone = !item.available;
              const flagged = gone || !item.purchasable || item.exceeds_stock;
              // "STD" is the implicit grade of an ungraded listing — a real
              // choice of one, which is not a choice worth printing.
              const grade =
                item.size_code && item.size_code !== "STD" ? item.size_code : "";
              return (
              <li
                key={item.id}
                className={`rounded-card border p-3 ${
                  flagged
                    ? "border-accent-500/40 bg-accent-50"
                    : "border-surface-border"
                }`}
              >
                <div className="flex items-start justify-between gap-3">
                  <div className="min-w-0">
                    <p className="truncate font-medium text-primary-900">
                      {gone ? "Item not available today" : item.product_name}
                      {/* The grade, beside the name: two lines reading
                          "Pomegranate · 1 Kg Box" are otherwise identical
                          when they are in fact different crates at different
                          prices. */}
                      {grade && (
                        <span className="ml-2 rounded-full bg-surface-sunken px-2 py-0.5 text-xs font-medium text-primary-800">
                          {grade}
                        </span>
                      )}
                    </p>
                    {gone ? (
                      <p className="mt-1 text-xs font-medium text-accent-800">
                        The grower has not listed this pack today. Remove it to
                        check out.
                      </p>
                    ) : (
                      <p className="text-xs text-primary-900/60">
                        {item.unit_label}
                        {item.size_meta ? ` · ${item.size_meta}` : ""} &middot;{" "}
                        {item.unit_price_display} each
                      </p>
                    )}
                    {!gone && !item.purchasable && (
                      <p className="mt-1 text-xs font-medium text-accent-800">
                        Not enough stock left today. Reduce the quantity or
                        remove it to check out.
                      </p>
                    )}
                    {overLimit && (
                      <p role="alert" className="mt-1 text-xs font-medium text-accent-800">
                        {item.available_display
                          ? `Only ${item.available_display} left today`
                          : "More than is left today"}
                        {item.max_qty > 0
                          ? ` — keep at most ${item.max_qty} to check out.`
                          : " — remove this to check out."}
                      </p>
                    )}
                  </div>
                  <p className="shrink-0 font-semibold text-primary-900">
                    {item.line_total_display}
                  </p>
                </div>

                <div className="mt-3 flex items-center justify-between">
                  {/* No stepper on a pack that no longer exists: there is
                      nothing to have more or less of. */}
                  <div className={`flex items-center gap-1 ${gone ? "invisible" : ""}`}>
                    {/* Quantity is sent as an absolute value, never a delta:
                        two quick taps would otherwise race and land on a
                        number the customer did not choose. */}
                    <button
                      type="button"
                      aria-label={`Decrease quantity of ${item.product_name}`}
                      disabled={busy || gone || shownQty <= 1}
                      onClick={() => changeQty(item.id, shownQty - 1)}
                      className="h-8 w-8 rounded-card border border-surface-border text-primary-800 disabled:opacity-40"
                    >
                      &minus;
                    </button>
                    <span
                      aria-live="polite"
                      aria-label={`Quantity: ${shownQty}`}
                      className="w-8 text-center text-sm font-medium text-primary-900"
                    >
                      {shownQty}
                    </span>
                    <button
                      type="button"
                      aria-label={`Increase quantity of ${item.product_name}`}
                      // Stops at the ceiling the server apportioned to this
                      // line, so the limit is discovered here rather than at
                      // checkout.
                      disabled={busy || gone || !item.purchasable || shownQty >= item.max_qty}
                      onClick={() => changeQty(item.id, shownQty + 1)}
                      className="h-8 w-8 rounded-card border border-surface-border text-primary-800 disabled:opacity-40"
                    >
                      +
                    </button>
                  </div>

                  <button
                    type="button"
                    disabled={busy}
                    onClick={() => removeItem.mutate(item.id)}
                    className="text-sm font-medium text-primary-900/60 underline underline-offset-2 hover:text-accent-800 disabled:opacity-40"
                  >
                    Remove
                  </button>
                </div>
              </li>
              );
            })}
          </ul>
        </div>

        {data && data.items.length > 0 && (
          <div className="border-t border-surface-border px-5 py-4">
            {/* The same breakdown CLAUDE.md §6.2 requires at checkout, shown
                here too so the fees are never a surprise one screen later. */}
            <dl className="space-y-1 text-sm">
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
              <div className="flex justify-between border-t border-surface-border pt-2 text-base font-semibold">
                <dt className="text-primary-900">Total</dt>
                <dd className={syncing ? "text-primary-900/40" : "text-primary-900"}>
                  {data.total_display}
                </dd>
              </div>
            </dl>

            <button
              type="button"
              disabled={!data.checkoutable || busy || syncing}
              onClick={() => {
                close();
                navigate("/checkout");
              }}
              className="mt-4 w-full rounded-card bg-primary-600 px-4 py-2.5 font-medium text-white hover:bg-primary-700 disabled:cursor-not-allowed disabled:bg-surface-sunken disabled:text-primary-900/40"
            >
              {syncing ? "Updating…" : "Proceed to checkout"}
            </button>

            {!data.checkoutable && (
              <p className="mt-2 text-center text-xs text-accent-800">
                Fix the highlighted items above to continue.
              </p>
            )}
          </div>
        )}
      </div>
    </div>
  );
}
