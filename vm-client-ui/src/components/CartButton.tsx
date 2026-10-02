/**
 * Header cart control with a live item count.
 *
 * Hidden entirely for signed-out visitors: the cart is server-side and keyed
 * by customer, so there is nothing to show and a zero badge would only invite
 * a click that leads to a sign-in wall.
 */
import type { ReactElement } from "react";
import { useCart } from "../lib/cart.js";
import { useAuth } from "../lib/auth.js";

export function CartButton(): ReactElement | null {
  const { user } = useAuth();
  const { count, open } = useCart();

  if (!user) return null;

  return (
    <button
      type="button"
      onClick={open}
      // The count is in the accessible name rather than only in the badge,
      // so it is announced instead of read as a stray number.
      aria-label={count === 0 ? "Cart, empty" : `Cart, ${count} item${count === 1 ? "" : "s"}`}
      className="relative flex items-center gap-1.5 rounded-full border border-cream-300 bg-surface-raised px-4 py-2 text-sm font-medium text-primary-800 shadow-card transition hover:border-primary-300 hover:shadow-lift"
    >
      {/* Basket, drawn rather than an icon font so it renders identically
          everywhere and costs no extra request. */}
      <svg viewBox="0 0 20 20" aria-hidden="true" className="h-4 w-4" fill="none">
        <path
          d="M3 7h14l-1.4 8.3a2 2 0 0 1-2 1.7H6.4a2 2 0 0 1-2-1.7L3 7Z"
          stroke="currentColor"
          strokeWidth="1.5"
          strokeLinejoin="round"
        />
        <path
          d="M7 7V5.5a3 3 0 0 1 6 0V7"
          stroke="currentColor"
          strokeWidth="1.5"
          strokeLinecap="round"
        />
      </svg>
      <span>Cart</span>
      {count > 0 && (
        <span
          aria-hidden="true"
          className="absolute -right-1.5 -top-1.5 flex h-5 min-w-5 items-center justify-center rounded-full bg-accent-500 px-1 text-xs font-semibold text-white"
        >
          {count}
        </span>
      )}
    </button>
  );
}
