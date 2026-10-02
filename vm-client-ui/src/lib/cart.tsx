/**
 * Cart state.
 *
 * There is deliberately no client-side cart model. The cart lives server-side
 * (CLAUDE.md §5.3) and is re-priced from the catalog on every read, so the
 * server's response IS the state — mirroring it into React state would let the
 * two disagree about price or availability, and price is money.
 *
 * That makes this a thin TanStack Query wrapper: mutate, then refetch. The one
 * piece of genuinely local state is whether the drawer is open.
 */
import {
  createContext, useContext, useMemo, useState, type ReactElement, type ReactNode,
} from "react";
import { useMutation, useQuery, useQueryClient, type UseQueryResult } from "@tanstack/react-query";
import type { ApiError } from "@vayal/ui-kit";
import { api } from "./api.js";
import { useAuth } from "./auth.js";

export interface CartItem {
  id: string;
  product_id: string;
  product_unit_id: string;
  /**
   * The GRADE this pack belongs to. A cart can hold two lines of one produce
   * at different grades — different goods, different prices, different crates
   * — so every line has to say which (CLAUDE.md §5.3). "STD" is the implicit
   * grade of an ungraded listing and is never shown.
   */
  size_code: string;
  size_meta: string;
  product_name: string;
  unit_label: string;
  weight_grams: number;
  qty: number;
  unit_price_paise: number;
  unit_price_display: string;
  line_total_paise: number;
  line_total_display: string;
  /** False when today's stock cannot cover even ONE pack of this size. */
  purchasable: boolean;
  /**
   * False when the pack is not in TODAY'S catalogue: never declared for
   * today, archived, deleted, or its supplier suspended. The line still has
   * to be shown, because it blocks checkout until the customer removes it.
   */
  available: boolean;
  /** True when the QUANTITY on this line asks for more than is left today. */
  exceeds_stock: boolean;
  /**
   * The most packs this line could hold, given what is left and what earlier
   * lines of the same produce have already claimed. A ceiling, not the current
   * quantity — the + button stops here.
   */
  max_qty: number;
  /** What is left of the product ("10 kg"). Sent only when the line exceeds. */
  available_display?: string;
}

/**
 * A re-pricing difference the customer must see before paying.
 *
 * CLAUDE.md §5.3: carts never store prices, so a supplier edit between adding
 * and checking out shows up here rather than silently changing the total.
 */
export interface CartChange {
  cart_item_id: string;
  product_name: string;
  kind: string;
  message: string;
  old_price_paise?: number;
  new_price_paise?: number;
}

export interface Cart {
  cart_id: string;
  items: CartItem[];
  changes: CartChange[];
  subtotal_paise: number;
  subtotal_display: string;
  platform_fee_paise: number;
  platform_fee_display: string;
  delivery_fee_paise: number;
  delivery_fee_display: string;
  total_paise: number;
  total_display: string;
  /** Server's verdict. The UI must not compute its own. */
  checkoutable: boolean;
}

export const CART_KEY = ["cart"] as const;

interface CartContextValue {
  cart: UseQueryResult<Cart, ApiError>;
  /** Total units in the cart, for the header badge. */
  count: number;
  isOpen: boolean;
  open: () => void;
  close: () => void;
}

const CartContext = createContext<CartContextValue | null>(null);

export function CartProvider({ children }: { children: ReactNode }): ReactElement {
  const { user } = useAuth();
  const [isOpen, setIsOpen] = useState(false);

  const cart = useQuery<Cart, ApiError>({
    queryKey: CART_KEY,
    queryFn: () => api.get<Cart>("/cart"),
    // Anonymous visitors have no cart; asking would 401 on every page load.
    enabled: Boolean(user),
    // Someone else may buy the last of a product while this cart sits open,
    // so a stale cart must not survive long enough to be checked out.
    staleTime: 15_000,
  });

  const value = useMemo<CartContextValue>(
    () => ({
      cart,
      count: cart.data?.items.reduce((sum, item) => sum + item.qty, 0) ?? 0,
      isOpen,
      open: () => setIsOpen(true),
      close: () => setIsOpen(false),
    }),
    [cart, isOpen],
  );

  return <CartContext.Provider value={value}>{children}</CartContext.Provider>;
}

export function useCart(): CartContextValue {
  const ctx = useContext(CartContext);
  if (!ctx) throw new Error("useCart must be used inside a CartProvider");
  return ctx;
}

/**
 * Every mutation refetches the cart rather than patching the cache.
 *
 * The server recomputes fees and purchasability on each read, so a locally
 * patched cache would show a stale total for the moment before the refetch
 * lands — on a screen whose entire job is telling the customer what they owe.
 */
function useCartMutation<TArgs>(
  fn: (args: TArgs) => Promise<unknown>,
): ReturnType<typeof useMutation<unknown, ApiError, TArgs>> {
  const queryClient = useQueryClient();
  return useMutation<unknown, ApiError, TArgs>({
    mutationFn: fn,
    onSuccess: () => queryClient.invalidateQueries({ queryKey: CART_KEY }),
  });
}

export function useAddToCart() {
  return useCartMutation<{ product_unit_id: string; qty: number }>((body) =>
    api.post("/cart/items", body),
  );
}

export function useUpdateCartQty() {
  return useCartMutation<{ id: string; qty: number }>(({ id, qty }) =>
    api.patch(`/cart/items/${id}`, { qty }),
  );
}

export function useRemoveCartItem() {
  return useCartMutation<string>((id) => api.delete(`/cart/items/${id}`));
}
