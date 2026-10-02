/**
 * The row of produce thumbnails on an order card.
 *
 * An order list of nothing but numbers and dates is hard to scan: "VM-260817-
 * 0042" tells a customer nothing about which order was the one with the
 * mangoes. Four small photographs do, at a glance, before any text is read.
 *
 * Deliberately capped. Four is what fits beside the total at 360px without the
 * row wrapping, and beyond that a strip of thumbnails stops being a glance and
 * becomes a list — which is what the order page itself is for. The rest are
 * summarised as "+3", which is honest about there being more without
 * pretending to show it.
 *
 * Counted by PRODUCT, not by line: an order holding a 1 kg and a 5 kg pack of
 * the same tomatoes is one tomato, and showing the same photo twice looks like
 * a bug rather than a quantity.
 */
import type { ReactElement } from "react";
import { ProducePlaceholder } from "@vayal/ui-kit";

/** How many photographs to show before collapsing the rest into "+N". */
const MAX_THUMBNAILS = 4;

export interface OrderThumbnailItem {
  product_id?: string;
  product_name: string;
  image_url?: string;
}

/** One entry per distinct product, in the order the items appear. */
function distinctProducts(items: readonly OrderThumbnailItem[]): OrderThumbnailItem[] {
  const seen = new Set<string>();
  const out: OrderThumbnailItem[] = [];
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

export function OrderThumbnails({
  items,
}: {
  // `undefined` is spelled out rather than left to a default parameter: the
  // list endpoint may omit `items` entirely, and exactOptionalPropertyTypes
  // will not let that value through an optional prop otherwise.
  items: readonly OrderThumbnailItem[] | undefined;
}): ReactElement | null {
  const products = distinctProducts(items ?? []);
  if (products.length === 0) return null;

  const shown = products.slice(0, MAX_THUMBNAILS);
  const extra = products.length - shown.length;

  return (
    <ul
      className="flex items-center gap-1.5"
      // The names are already in the order page behind this card, and each
      // thumbnail carries its own label, so the list itself needs no heading.
      aria-label={`${products.length} product${products.length === 1 ? "" : "s"} in this order`}
    >
      {shown.map((item, index) => (
        <li
          key={item.product_id ?? `${item.product_name}-${index}`}
          title={item.product_name}
          className="h-10 w-10 shrink-0 overflow-hidden rounded-card border border-surface-border bg-surface-raised"
        >
          {item.image_url ? (
            <img
              src={item.image_url}
              alt={item.product_name}
              loading="lazy"
              // contain, matching the catalogue cards: a crop this small takes
              // the part that identifies the produce.
              className="h-full w-full object-contain p-0.5"
            />
          ) : (
            // No caption at 40px — there is no room for one, and the title
            // attribute and alt text above already name the produce.
            <ProducePlaceholder label="" />
          )}
        </li>
      ))}

      {extra > 0 && (
        <li
          className="flex h-10 min-w-10 shrink-0 items-center justify-center rounded-card border border-surface-border bg-surface-sunken px-1.5 text-xs font-medium text-primary-900/70"
          title={products
            .slice(MAX_THUMBNAILS)
            .map((item) => item.product_name)
            .join(", ")}
        >
          +{extra}
        </li>
      )}
    </ul>
  );
}
