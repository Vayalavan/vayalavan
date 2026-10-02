/**
 * Wire types for the supplier surface of the gateway.
 *
 * Mirrors vm-supplier-ui/src/lib/types.ts, extended with the availability and
 * sales shapes the web app declares inline in its screens. Money is always
 * integer paise; anything named `*_display` is a string the SERVER formatted
 * and this app must render as-is rather than re-derive (CLAUDE.md rule 1).
 */

export const PRODUCT_TYPES = ["fruit", "vegetable", "microgreen", "other"] as const;
export type ProductType = (typeof PRODUCT_TYPES)[number];

export const PRODUCT_STATUSES = ["draft", "active", "archived"] as const;
export type ProductStatus = (typeof PRODUCT_STATUSES)[number];

export interface SupplierUser {
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
  user: SupplierUser;
  tokens: Tokens;
}

// ---------------------------------------------------------------------------
// Catalogue
// ---------------------------------------------------------------------------

export interface ProductUnit {
  id: string;
  label: string;
  /**
   * Optional detail shown under the label wherever the pack is chosen —
   * "6-8 fruit", "ventilated carton". The label itself has to stay short
   * enough to be a chip, so this is where the rest goes.
   */
  meta: string | null;
  weight_grams: number;
  /** Integer paise — the authoritative value. */
  price_paise: number;
  /** Server-formatted rupees, for display only. */
  price_display: string;
  is_active: boolean;
  sort_order: number;
}

export type MediaKind = "image" | "video";

/** One item of a product's gallery. */
export interface ProductMedia {
  id: string;
  kind: MediaKind;
  /** The stored object key — what gets sent back on the next save. */
  object_key: string;
  /** Short-lived presigned GET, minted per response. Do not persist. */
  url: string | null;
  content_type: string | null;
  sort_order: number;
}

/**
 * One SIZE CODE — a grade of the product, e.g. "M2" meaning 150 g - 200 g.
 *
 * The grade is where the gallery, the pack prices and the daily stock all
 * live: M and XL are separate crates at separate prices, photographed
 * separately. Every product has at least one, and a grower who does not grade
 * simply has one.
 */
export interface SizeCode {
  id: string;
  code: string;
  meta: string | null;
  is_active: boolean;
  sort_order: number;
  /**
   * Roughly what percent of this product's harvest comes off as this grade.
   * Null when the grower has not said, which is the normal starting state.
   *
   * The storefront draws each grade's chip by it — gold for the grade there is
   * barely any of, silver for the one that is most of the harvest — and
   * nothing else reads it. No price, stock or payout depends on this number.
   */
  harvest_share_pct: number | null;
  /** THIS grade's cover — the first image of its gallery. */
  image_url: string | null;
  /** This grade's whole gallery, images and videos, in the grower's order. */
  media: ProductMedia[];
  /** The pack sizes this grade is sold in, each with its own price. */
  packs: ProductUnit[];
}

export interface Product {
  id: string;
  supplier_id: string;
  name: string;
  type: ProductType;
  grade: string | null;
  description: string | null;
  /**
   * The product's thumbnail: the cover of the first grade that has one, as a
   * short-lived presigned GET. Null when no grade has an image — which
   * includes a listing carrying only video. Do not persist.
   */
  image_url: string | null;
  /**
   * The PRODUCT's own gallery — the field, the packing shed, the grower —
   * kept separate from the grades because the form edits it separately. A
   * customer sees it appended to whichever grade's pictures they are viewing.
   */
  media: ProductMedia[];
  /** Every grade, in the grower's order. */
  size_codes: SizeCode[];
  status: ProductStatus;
  created_at: string;
  updated_at: string;
}

export interface ProductPage {
  products: Product[];
  total: number;
  limit: number;
  offset: number;
}

export interface PresignResponse {
  /** Which of image or video the server resolved the content type to. */
  kind: MediaKind;
  upload_url: string;
  key: string;
  content_type: string;
  expires_in: number;
}

// ---------------------------------------------------------------------------
// Daily availability
// ---------------------------------------------------------------------------

/**
 * One SIZE CODE's declaration for the day.
 *
 * A grower declares per grade, not per product: M and XL are separate crates
 * with separate gram pools, so a product with three grades is three rows.
 */
export interface SheetRow {
  product_id: string;
  name: string;
  type: string;
  grade: string | null;
  size_code_id: string;
  size_code: string;
  size_meta: string | null;
  image_url: string | null;
  availability_id: string | null;
  declared: boolean;
  total_grams: number;
  reserved_grams: number;
  sold_grams: number;
  remaining_grams: number;
  /** What a customer can buy right now — zero once the product is closed. */
  sellable_grams: number;
  status: string;
}

export interface Sheet {
  date: string;
  /**
   * The SERVER decides what today is, in IST. The device clock is not trusted
   * for this — CLAUDE.md rule 2.
   */
  is_today: boolean;
  products: SheetRow[];
  declared_count: number;
  undeclared_count: number;
}

export interface SaveAvailabilityResult {
  saved: number;
}

export interface CopyYesterdayResult {
  copied: number;
  skipped: number;
}

// ---------------------------------------------------------------------------
// Sales and settlement
// ---------------------------------------------------------------------------

export interface SaleLine {
  product_name: string;
  unit_label: string;
  grade: string | null;
  /**
   * The GRADE this pack was sold at — "XL2". Null on an ungraded listing and
   * on orders placed before size codes existed. Distinct from `grade`, which
   * is the product-level quality label the grower types.
   */
  size_code: string | null;
  size_meta: string | null;
  weight_grams: number;
  qty: number;
  unit_price_display: string;
  line_total_display: string;
}

export interface SupplierOrder {
  order_id: string;
  order_number: string;
  placed_at: string;
  delivery_day: string;
  status: string;
  units: number;
  amount_display: string;
  payout_status: string;
  payout_reference: string;
  payout_paid_at: string | null;
  items: SaleLine[];
}

export interface SalesProductRow {
  product_name: string;
  grade: string | null;
  units: number;
  grams: number;
  amount_display: string;
}

export interface Charges {
  commission_rate: string;
  gross_display: string;
  commission_display: string;
  net_display: string;
}

export interface Sales {
  charges: Charges;
  summary: {
    order_count: number;
    gross_display: string;
    pending_display: string;
    settled_display: string;
  };
  products: SalesProductRow[];
  orders: SupplierOrder[];
  orders_total: number;
  limit: number;
  offset: number;
  /**
   * The window every figure above was computed over.
   *
   * Resolved server-side in IST: the app asks for a PERIOD ("7d"), never for
   * dates it worked out from the handset's clock, which may be in another
   * timezone or simply wrong (CLAUDE.md rule 2).
   */
  range: SalesRange;
}

export interface SalesRange {
  key: string;
  label: string;
  all_time: boolean;
  /** Absent for all time. Both are IST calendar dates, "YYYY-MM-DD". */
  from?: string;
  to?: string;
}

// ---------------------------------------------------------------------------
// CSV import
// ---------------------------------------------------------------------------

/** One row of the CSV preview. */
export interface ImportRow {
  line: number;
  status: "valid" | "error";
  errors?: string[];
  parsed?: {
    name: string;
    type: string;
    grade: string;
    unit_label: string;
    weight_grams: number;
    price_paise: number;
  };
}

export interface ImportDraftProduct {
  name: string;
  type: string;
  grade: string;
  units: { label: string; weight_grams: number; price_paise: number }[];
}

export interface ImportPreview {
  import_id: string;
  filename: string;
  status: string;
  total_rows: number;
  valid_rows: number;
  error_rows: number;
  rows: ImportRow[];
  products: ImportDraftProduct[];
}

export interface ImportResult {
  import_id: string;
  status: string;
  products_created: number;
  rows_imported: number;
  rows_skipped: number;
  images_downloaded: number;
}

/** One day on the insights screen's charts. Amounts are the grower's own. */
export interface SupplierAnalyticsDay {
  date: string;
  orders: number;
  units: number;
  grams: number;
  amount_paise: number;
  amount_display: string;
}

export interface SupplierAnalyticsProduce {
  product_name: string;
  grade: string | null;
  units: number;
  grams: number;
  amount_paise: number;
  amount_display: string;
}

export interface SupplierAnalyticsPack {
  unit_label: string;
  weight_grams: number;
  units: number;
  grams: number;
  amount_paise: number;
  amount_display: string;
  orders: number;
}

/**
 * GET /supplier/analytics — the grower's own trend screen.
 *
 * Every amount is what the payout pays them: their listed price with our
 * markup already taken out, the same subtraction the sales screen makes.
 */
export interface SupplierAnalytics {
  range: { key: string; label: string; all_time: boolean; from?: string; to?: string };
  summary: {
    orders: number;
    gross_paise: number;
    gross: string;
    pending_paise: number;
    pending: string;
    settled_paise: number;
    settled: string;
    average_order_paise: number;
    average_order: string;
    units: number;
    grams: number;
  };
  daily: SupplierAnalyticsDay[];
  /** The top rows only; `_total` is how many there were. */
  by_produce: SupplierAnalyticsProduce[];
  by_produce_total: number;
  by_produce_truncated: boolean;
  by_pack: SupplierAnalyticsPack[];
  by_pack_total: number;
  by_pack_truncated: boolean;
  /** Graded lines only. Empty for a grower who does not grade. */
  by_size_code: SupplierAnalyticsSizeCode[];
  by_size_code_total: number;
  by_size_code_truncated: boolean;
}

/**
 * One GRADE of one produce: "Pomegranate XL2".
 *
 * Named by both, because a grower's M2 pomegranate and M2 tomato are different
 * crates and a row reading "M2" alone would be summing two of them.
 */
export interface SupplierAnalyticsSizeCode {
  product_name: string;
  size_code: string;
  /** The grower's own words for the grade — "250 g - 300 g", or "". */
  size_meta: string;
  units: number;
  grams: number;
  amount_paise: number;
  amount_display: string;
  orders: number;
}
