/**
 * The counting rule behind an order card's thumbnail strip.
 *
 * Kept out of the component so it can be tested under plain Node — anything
 * importing react-native cannot be loaded by the test runner — and because the
 * rule, not the layout, is what has to match the web card
 * (vm-client-ui/src/components/OrderThumbnails.tsx).
 */

/** How many photographs to show before collapsing the rest into "+N". */
export const MAX_THUMBNAILS = 4;

export interface ThumbnailItem {
  product_id?: string;
  product_name: string;
  image_url?: string;
}

/**
 * One entry per distinct product, in the order the items appear.
 *
 * An order may hold two pack sizes of the same produce, which is one product
 * bought twice, not two products: showing the same photograph twice reads as a
 * bug, and counting it twice makes "+2" a lie.
 */
export function distinctProducts(items: readonly ThumbnailItem[]): ThumbnailItem[] {
  const seen = new Set<string>();
  const out: ThumbnailItem[] = [];
  for (const item of items) {
    // Falls back to the name when the id is absent, so two different products
    // never collapse into one just because a field is missing.
    const key = item.product_id ?? `name:${item.product_name}`;
    if (seen.has(key)) continue;
    seen.add(key);
    out.push(item);
  }
  return out;
}
