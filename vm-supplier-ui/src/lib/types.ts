/** Wire types shared by the supplier screens. */

export const PRODUCT_TYPES = ["fruit", "vegetable", "microgreen", "other"] as const;
export type ProductType = (typeof PRODUCT_TYPES)[number];

export const PRODUCT_STATUSES = ["draft", "active", "archived"] as const;
export type ProductStatus = (typeof PRODUCT_STATUSES)[number];

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
