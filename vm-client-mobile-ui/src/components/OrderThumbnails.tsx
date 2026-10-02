/**
 * The row of produce thumbnails on an order card.
 *
 * An order list of nothing but numbers and dates is hard to scan: "VM-260817-
 * 0042" tells a customer nothing about which order was the one with the
 * mangoes. Four small photographs do, at a glance, before any text is read.
 *
 * Deliberately capped. Four is what fits beside the total on a 360px phone
 * without the row wrapping, and beyond that a strip of thumbnails stops being a
 * glance and becomes a list — which is what the order page itself is for. The
 * rest are summarised as "+3", which is honest about there being more without
 * pretending to show it.
 *
 * Counted by PRODUCT, not by line: an order holding a 1 kg and a 5 kg pack of
 * the same tomatoes is one tomato, and showing the same photo twice looks like
 * a bug rather than a quantity.
 *
 * The web card is the same four-then-count treatment
 * (vm-client-ui/src/components/OrderThumbnails.tsx), and `orderThumbnails.ts`
 * holds the counting rule both sides follow.
 */
import { useState, type ReactElement } from "react";
import { Image, View } from "react-native";

import { distinctProducts, MAX_THUMBNAILS, type ThumbnailItem } from "../lib/orderThumbnails";
import { colors, radius, spacing } from "../theme/tokens";
import { Text } from "./Text";

const PLACEHOLDER = require("../../assets/produce-placeholder.png") as number;

/** Big enough to recognise produce, small enough for four plus a total. */
const SIZE = 40;

export function OrderThumbnails({
  items,
}: {
  items: readonly ThumbnailItem[] | undefined;
}): ReactElement | null {
  const products = distinctProducts(items ?? []);
  if (products.length === 0) return null;

  const shown = products.slice(0, MAX_THUMBNAILS);
  const extra = products.length - shown.length;

  return (
    <View
      style={{ flexDirection: "row", alignItems: "center", gap: spacing.xs }}
      accessible
      accessibilityLabel={`${products.length} product${
        products.length === 1 ? "" : "s"
      } in this order: ${products.map((item) => item.product_name).join(", ")}`}
    >
      {shown.map((item, index) => (
        <Thumbnail
          key={item.product_id ?? `${item.product_name}-${index}`}
          uri={item.image_url ?? ""}
        />
      ))}

      {extra > 0 && (
        <View
          style={{
            height: SIZE,
            minWidth: SIZE,
            paddingHorizontal: spacing.xs,
            borderRadius: radius.card,
            borderWidth: 1,
            borderColor: colors.surface.border,
            backgroundColor: colors.surface.sunken,
            alignItems: "center",
            justifyContent: "center",
          }}
        >
          <Text variant="caption" tone="muted">
            +{extra}
          </Text>
        </View>
      )}
    </View>
  );
}

/**
 * One photograph, falling back to the placeholder.
 *
 * The fallback covers a load failure as well as a missing URL, for the same
 * reason as ProduceImage: a presigned URL that has expired, or a dead spot,
 * otherwise leaves an empty box that reads as a broken app.
 */
function Thumbnail({ uri }: { uri: string }): ReactElement {
  const [failed, setFailed] = useState(false);

  return (
    <View
      style={{
        height: SIZE,
        width: SIZE,
        borderRadius: radius.card,
        borderWidth: 1,
        borderColor: colors.surface.border,
        backgroundColor: colors.surface.raised,
        overflow: "hidden",
      }}
    >
      <Image
        source={uri === "" || failed ? PLACEHOLDER : { uri }}
        onError={() => setFailed(true)}
        // contain, matching the catalogue cards: a crop this small takes the
        // part that identifies the produce.
        resizeMode="contain"
        style={{ width: "100%", height: "100%", padding: 2 }}
        // The row above announces every product by name, so each image
        // repeating one would triple what a screen reader has to get through.
        accessible={false}
        importantForAccessibility="no-hide-descendants"
      />
    </View>
  );
}
