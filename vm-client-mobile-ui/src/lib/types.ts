/**
 * Wire types for the customer surface of the gateway.
 *
 * Mirrors the shapes vm-client-ui declares inline in its routes, collected
 * here instead: a phone app fetches the same catalogue on three screens, and
 * three private copies of the same interface is how one of them ends up
 * missing a field that the server started sending.
 *
 * Money is always integer paise; anything named `*_display` is a string the
 * SERVER formatted and this app renders as-is rather than re-deriving
 * (CLAUDE.md rule 1).
 */

export const PRODUCT_TYPES = ["fruit", "vegetable", "microgreen", "other"] as const;
export type ProductType = (typeof PRODUCT_TYPES)[number];

// ---------------------------------------------------------------------------
// Identity
// ---------------------------------------------------------------------------

export interface CustomerUser {
  id: string;
  email: string | null;
  phone: string | null;
  role: string;
  status: string;
  name?: string | null;
}

export interface Tokens {
  access_token: string;
  refresh_token: string;
}

export interface AuthResponse {
  user: CustomerUser;
  tokens: Tokens;
}

// ---------------------------------------------------------------------------
// Catalogue
// ---------------------------------------------------------------------------

export interface CatalogUnit {
  id: string;
  label: string;
  /**
   * The grower's own detail for this pack — "6-8 fruit", "ventilated carton"
   * — or null. The label is a chip; this is the sentence that would not fit
   * in it, and the product screen prints it under the label.
   */
  meta: string | null;
  weight_grams: number;
  price_paise: number;
  price_display: string;
  /** False when this pack is larger than the stock left today. */
  purchasable: boolean;
}

/** One item of a product's gallery. */
export interface ProductMedia {
  id: string;
  kind: "image" | "video";
  object_key: string;
  /** A short-lived presigned GET, minted per response. Do not cache. */
  url: string | null;
  content_type: string | null;
  sort_order: number;
}

/**
 * One SIZE CODE as a CARD offers it: a grade with its own cover, packs and
 * stock. Switching grade on a card swaps the picture and the prices.
 *
 * No gallery — a listing ships one thumbnail per grade, not every grade's
 * whole media set for pictures no card draws (CLAUDE.md §5.2).
 */
export interface CatalogCardSizeCode {
  id: string;
  code: string;
  meta: string | null;
  image_url: string | null;
  stock_hint: string;
  any_unit_purchasable: boolean;
  units: CatalogUnit[];
}

/**
 * The same grade on the DETAIL screen, where the whole gallery is drawn.
 */
export interface CatalogSizeCode extends CatalogCardSizeCode {
  media: ProductMedia[];
  /**
   * Roughly what percent of this product's harvest comes off as this grade —
   * 55 for the M that is most of the field, 3 for the XL2 that is a handful
   * of fruit a tree. Null when the grower has not estimated their split,
   * which draws no rarity treatment rather than guessing one.
   *
   * On the DETAIL grade only: a card shows one grade's thumbnail and has no
   * selector to colour.
   */
  harvest_share_pct: number | null;
  units: CatalogUnitDetail[];
}

export interface CatalogProduct {
  id: string;
  name: string;
  type: string;
  grade: string | null;
  description: string | null;
  /**
   * The COVER — the gallery's first image — as a short-lived presigned GET,
   * minted per response. Do not cache the URL. Null when the product has no
   * image, which includes one carrying only video.
   */
  image_url: string | null;
  /**
   * The GRADE a card OPENS on — its picture, its packs, its stock. The server
   * picks the first grade with something buyable, so a card never leads with a
   * sold-out M while the XL beside it is on sale. The customer switches from
   * there without leaving the list.
   */
  size_code_id: string;
  size_code: string;
  size_meta: string | null;
  /** How many grades the product has today. */
  size_code_count: number;
  /**
   * Every grade declared today, in the grower's order, each with its own
   * cover, packs and stock. Carried by the listing as well as the detail
   * endpoint, so switching grade is local — no round trip, no reload of the
   * list. The detail endpoint's entries are the richer CatalogSizeCode.
   */
  size_codes: CatalogCardSizeCode[];
  /** A coarse phrase or "". The API never exposes exact stock. */
  stock_hint: string;
  /**
   * False when nothing on this card can be bought — sold out, closed by the
   * grower, or every pack larger than the stock left. The card is still shown,
   * greyed out, rather than vanishing mid-session.
   */
  any_unit_purchasable: boolean;
  units: CatalogUnit[];
}

export interface Catalog {
  /** The business day, decided server-side in IST (CLAUDE.md rule 2). */
  date: string;
  products: CatalogProduct[];
  /** Every card listed today, sold-out ones included. */
  total: number;
  /** How many of those can actually be bought right now. */
  sellable_total: number;
  grades: string[];
}

/**
 * A pack size on the detail screen.
 *
 * The API also sends a per-kilo figure on this shape; nothing renders it any
 * more, so it is deliberately not declared here.
 */
export type CatalogUnitDetail = CatalogUnit;

export interface ProductDetail {
  product: Omit<CatalogProduct, "size_codes"> & {
    /** The full grades, galleries and all — the detail screen draws them. */
    size_codes: CatalogSizeCode[];
    supplier_name: string;
    /** False when the grower has not listed this for today, or has closed it. */
    available_today: boolean;
    date: string;
  };
  units: CatalogUnitDetail[];
}

// ---------------------------------------------------------------------------
// Cart
// ---------------------------------------------------------------------------

export interface CartItem {
  id: string;
  product_id: string;
  product_unit_id: string;
  /**
   * The GRADE this pack belongs to. A cart can hold two lines of one produce
   * at different grades, at different prices — so every line names its own.
   */
  size_code_id: string;
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
   * False when the pack is not in TODAY'S catalogue: never declared for today,
   * archived, deleted, or its supplier suspended. The line still has to be
   * shown, because it blocks checkout until the customer removes it.
   */
  available: boolean;
  /** True when the QUANTITY on this line asks for more than is left today. */
  exceeds_stock: boolean;
  /**
   * The most packs this line could hold, given what is left and what earlier
   * lines of the same produce have already claimed. A ceiling, not the current
   * quantity — the stepper stops here.
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
  /** The server's verdict. The app must not compute its own. */
  checkoutable: boolean;
}

// ---------------------------------------------------------------------------
// Addresses
// ---------------------------------------------------------------------------

export interface Address {
  id: string;
  label: string | null;
  recipient_name: string;
  phone: string;
  line1: string;
  line2: string | null;
  landmark: string | null;
  city: string;
  state: string;
  pincode: string;
  is_default: boolean;
}

/** The mutable half of an address, as the form holds it. */
export interface AddressDraft {
  label: string;
  recipient_name: string;
  phone: string;
  line1: string;
  line2: string;
  landmark: string;
  city: string;
  state: string;
  pincode: string;
}

// ---------------------------------------------------------------------------
// Orders
// ---------------------------------------------------------------------------

/**
 * One step of the three-milestone timeline (CLAUDE.md §6.1).
 *
 * `completed` is derived by the SERVER from timestamps at read time, never
 * stored and never recomputed here — a phone clock must not be able to tick an
 * order forward.
 */
export interface TimelineMilestone {
  name: string;
  at: string;
  completed: boolean;
}

/**
 * A line on a placed order.
 *
 * The values ARE snapshots — orders-api stores them in `*_snapshot` columns so
 * an order still renders after the product is edited or archived (CLAUDE.md
 * §5.3) — but the JSON does not carry that suffix. Naming these
 * `product_name_snapshot` here made every field `undefined` at runtime, which
 * is how "undefined × 2" reached the order detail screen. Match the wire, not
 * the schema: `vm-orders-api/internal/api/api.go`, `orderItemResponse`.
 */
export interface OrderItem {
  product_id: string;
  product_name: string;
  unit_label: string;
  /**
   * Presigned at read time, and the one field here that is NOT a snapshot:
   * products store an object key, not a URL (CLAUDE.md §5.2). Absent when the
   * produce has no photograph, was deleted, or the catalogue could not be
   * reached — show the placeholder, never a blank box.
   */
  image_url?: string;
  /** `omitempty` server-side: absent, not null, when the product has no grade. */
  grade?: string;
  /**
   * The SIZE CODE this line was bought at, snapshotted when the order was
   * placed. Absent on ungraded listings and on every order placed before size
   * codes existed — render a missing one as no grade at all.
   */
  size_code?: string;
  size_meta?: string;
  weight_grams: number;
  qty: number;
  unit_price_display: string;
  line_total_display: string;
}

/** The address as it was at placement. Never the current address book. */
export interface AddressSnapshot {
  recipient_name?: string;
  phone?: string;
  line1?: string;
  line2?: string;
  landmark?: string;
  city?: string;
  state?: string;
  pincode?: string;
}

export interface Order {
  /** Set when a scheduled or repeat order placed this one (CLAUDE.md §6.7). */
  schedule_id?: string;
  id: string;
  order_number: string;
  status: string;
  placed_at: string;
  delivery_day: string;
  expected_delivery_date: string;
  subtotal_display: string;
  platform_fee_display: string;
  delivery_fee_display: string;
  total_display: string;
  items?: OrderItem[];
  milestones?: TimelineMilestone[];
  expected_delivery_text?: string;
  courier_notice?: string;
  support_notice?: string;
  address?: AddressSnapshot;
  /**
   * Sent by vm-orders-api only while the order is still `pending_payment`, so
   * an abandoned payment can be resumed. Absent on every other status — a paid
   * order has nothing left to pay.
   */
  razorpay_order_id?: string;
  razorpay_key_id?: string;
}

export interface OrderPage {
  orders: Order[];
  total: number;
}

// ---------------------------------------------------------------------------
// Cutoff
// ---------------------------------------------------------------------------

export interface Cutoff {
  /** Server time in IST — the reference point for the countdown. */
  server_now: string;
  cutoff_at: string;
  cutoff_hour: number;
  before_cutoff: boolean;
  /** Authoritative remaining seconds, computed server-side. */
  seconds_until_cutoff: number;
  expected_delivery_text: string;
}
