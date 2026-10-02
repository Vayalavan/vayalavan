/**
 * Cart state.
 *
 * There is deliberately no client-side cart model. The cart lives server-side
 * (CLAUDE.md §5.3) and is re-priced from the catalogue on every read, so the
 * server's response IS the state — mirroring it into React state would let the
 * two disagree about price or availability, and price is money.
 *
 * That makes this a thin TanStack Query wrapper: mutate, then refetch. Same
 * shape as vm-client-ui's cart.tsx, minus the drawer's open/closed flag — on a
 * phone the cart is a tab, not an overlay, so there is no local UI state left
 * for this module to hold.
 */
import { createContext, useContext, useMemo, type ReactElement, type ReactNode } from "react";
import {
  useMutation,
  useQuery,
  useQueryClient,
  type UseMutationResult,
  type UseQueryResult,
} from "@tanstack/react-query";

import type { ApiError } from "./api";
import { api } from "./client";
import { useAuth } from "./auth";
import type { Cart } from "./types";

export const CART_KEY = ["cart"] as const;

/**
 * How long to wait after the last tap before sending a quantity.
 *
 * Long enough that holding "+" to reach 15 is one request instead of fifteen,
 * short enough to feel immediate. Without it every tap fires a PATCH *and* a
 * cart refetch, so fifteen taps cost thirty requests and trip the gateway's
 * rate limit — the UI attacking its own API. The web app learned this the
 * same way; a phone, where the stepper is the primary control rather than a
 * fallback, hits it harder.
 */
export const QTY_DEBOUNCE_MS = 450;

interface CartContextValue {
  cart: UseQueryResult<Cart, ApiError>;
  /** Total units in the cart, for the tab badge. */
  count: number;
}

const CartContext = createContext<CartContextValue | null>(null);

export function CartProvider({ children }: { children: ReactNode }): ReactElement {
  const { user } = useAuth();

  const cart = useQuery<Cart, ApiError>({
    queryKey: CART_KEY,
    queryFn: () => api.get<Cart>("/cart"),
    // A signed-out visitor has no cart; asking would 401 on every launch and
    // hand the auth layer a refresh to attempt for a session that never was.
    enabled: Boolean(user),
    // Someone else may buy the last of a product while this cart sits open in
    // a backgrounded app, so a stale cart must not survive long enough to be
    // checked out.
    staleTime: 15_000,
  });

  const value = useMemo<CartContextValue>(
    () => ({
      cart,
      count: cart.data?.items.reduce((sum, item) => sum + item.qty, 0) ?? 0,
    }),
    [cart],
  );

  return <CartContext.Provider value={value}>{children}</CartContext.Provider>;
}

export function useCart(): CartContextValue {
  const context = useContext(CartContext);
  if (!context) throw new Error("useCart must be used inside a CartProvider");
  return context;
}

/**
 * Every mutation refetches the cart rather than patching the cache.
 *
 * The server recomputes fees and purchasability on each read, so a locally
 * patched cache would show a stale total for the moment before the refetch
 * lands — on the screen whose entire job is telling the customer what they owe.
 */
function useCartMutation<TArgs>(
  fn: (args: TArgs) => Promise<unknown>,
): UseMutationResult<unknown, ApiError, TArgs> {
  const queryClient = useQueryClient();
  return useMutation<unknown, ApiError, TArgs>({
    mutationFn: fn,
    onSuccess: () => queryClient.invalidateQueries({ queryKey: CART_KEY }),
  });
}

export function useAddToCart(): UseMutationResult<
  unknown,
  ApiError,
  { product_unit_id: string; qty: number }
> {
  return useCartMutation<{ product_unit_id: string; qty: number }>((body) =>
    api.post("/cart/items", body),
  );
}

export function useUpdateCartQty(): UseMutationResult<
  unknown,
  ApiError,
  { id: string; qty: number }
> {
  // The absolute quantity, never a delta: two quick taps would otherwise race
  // and land on a number the customer did not choose.
  return useCartMutation<{ id: string; qty: number }>(({ id, qty }) =>
    api.patch(`/cart/items/${id}`, { qty }),
  );
}

export function useRemoveCartItem(): UseMutationResult<unknown, ApiError, string> {
  return useCartMutation<string>((id) => api.delete(`/cart/items/${id}`));
}
