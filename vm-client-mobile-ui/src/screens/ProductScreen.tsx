/**
 * One product, on its own screen.
 *
 * What a card cannot do: the full description rather than two clamped lines, a
 * large photograph, who grew it, and every pack size with the grower's own
 * detail for it.
 *
 * A shared link to produce that has since sold out reaches this screen and is
 * told so, rather than 404ing — the catalogue detail endpoint deliberately has
 * no sold-out filter for exactly this reason.
 */
import { useCallback, useEffect, useState, type ReactElement } from "react";
import { Pressable, View, useWindowDimensions } from "react-native";
import { useNavigation } from "@react-navigation/native";
import type { NativeStackNavigationProp, NativeStackScreenProps } from "@react-navigation/native-stack";
import { useQuery } from "@tanstack/react-query";

import { Button } from "../components/Button";
import { Card } from "../components/Card";
import { ErrorState, Loading } from "../components/Feedback";
import { ProduceGallery } from "../components/ProduceGallery";
import { QuantityStepper } from "../components/QuantityStepper";
import { RarityShimmer } from "../components/RarityShimmer";
import { Screen } from "../components/Screen";
import { Text } from "../components/Text";
import type { ApiError } from "../lib/api";
import { useAuth } from "../lib/auth";
import { useAddToCart, useCart, useRemoveCartItem, useUpdateCartQty } from "../lib/cart";
import {
  harvestTier,
  harvestTierLabel,
  harvestShareSentence,
  type HarvestTier,
} from "../lib/harvest";
import { api } from "../lib/client";
import type { CatalogUnitDetail, ProductDetail } from "../lib/types";
import { usePullToRefresh } from "../lib/usePullToRefresh";
import type { RootStackParamList, ShopStackParamList } from "../navigation/types";
import { colors, radius, shadow, spacing } from "../theme/tokens";

type Props = NativeStackScreenProps<ShopStackParamList, "Product">;

/**
 * How each rarity tier is drawn.
 *
 * Token values, never hex (CLAUDE.md §9), and the same three metals the web
 * uses — tokens.test.ts asserts the ramps themselves still agree. Only the
 * UNSELECTED state is here: a selected chip keeps the brand green.
 */
/**
 * The size grid: TWO columns at every width, built as rows of pairs.
 *
 * Each row is a flex row whose two cells share it equally, so "two columns"
 * cannot be renegotiated by the screen width — the failure a percentage basis
 * with a maxWidth had, where a wide handset fitted three cells a row. The cells
 * are RECTANGLES that fill the card: the code against the share on one line,
 * the grower's size range against what the share is OF on the next.
 */
const SIZE_GAP = 10;

const METAL: Record<HarvestTier, { border: string; body: string }> = {
  // Three parts per tier: the cell's edge, its tinted body, and the filled
  // band across its foot. The BAND is where the colour does its work — a tint
  // alone, at the 50 step these started on, was invisible against an off-white
  // page.
  //
  // The band fills at 700, not 500: the band's text is `onPrimary`, which is
  // white, and white on a 500 is about 3:1 — under AA for a caption. Every 700
  // here clears 5.7:1.
  rare: { border: colors.gold[500], body: colors.gold[50] },
  uncommon: {
    border: colors.bronze[500],
    body: colors.bronze[50],
  },
  common: {
    border: colors.silver[500],
    body: colors.silver[50],
  },
};

export function ProductScreen({ route }: Props): ReactElement {
  const { productId } = route.params;
  const navigation = useNavigation<NativeStackNavigationProp<RootStackParamList>>();

  const { user } = useAuth();
  const { cart } = useCart();
  const addToCart = useAddToCart();
  const updateQty = useUpdateCartQty();
  const removeItem = useRemoveCartItem();

  const detail = useQuery<ProductDetail, ApiError>({
    queryKey: ["product", productId],
    queryFn: () => api.get<ProductDetail>(`/catalog/${productId}`),
  });

  const pull = usePullToRefresh(detail.refetch);

  // The gallery is SQUARE, the width of the content column. A fixed 280pt
  // height shrank square pictures — a grower's comparison chart above all —
  // to a strip with bars down both sides.
  const { width: windowWidth } = useWindowDimensions();
  const galleryHeight = Math.round(windowWidth - spacing.lg * 2);

  const [selectedSizeId, setSelectedSizeId] = useState("");
  /**
   * Each chip's measured width, keyed by size code.
   *
   * The sweep has to travel the width of the chip it is inside, and a chip is
   * as wide as its code and meta make it. A phone has no CSS percentage
   * translate, so the number has to come back from layout.
   */
  const [chipWidths, setChipWidths] = useState<Record<string, number>>({});
  const [selectedId, setSelectedId] = useState("");

  // Preselect the first pack that can actually be bought, once loaded.
  useEffect(() => {
    if (!detail.data || selectedSizeId !== "") return;
    // Open on the grade the server chose — the first with something buyable.
    setSelectedSizeId(
      detail.data.product.size_code_id ||
        (detail.data.product.size_codes?.[0]?.id ?? ""),
    );
  }, [detail.data, selectedSizeId]);

  const sizeCodes = detail.data?.product.size_codes ?? [];
  const activeSize = sizeCodes.find((sc) => sc.id === selectedSizeId) ?? sizeCodes[0];

  // The chosen grade's share, derived once — the sentence and the guard below
  // both need the tier, and deriving it twice invites the two disagreeing.
  const activeTier = harvestTier(activeSize?.harvest_share_pct);
  const activeShare =
    activeTier !== null && activeSize?.harvest_share_pct != null
      ? { pct: activeSize.harvest_share_pct, tier: activeTier }
      : null;

  // Preselect the first pack of the ACTIVE grade that can be bought. Re-runs
  // when the grade changes, because the packs change with it.
  useEffect(() => {
    if (!activeSize) return;
    if (activeSize.units.some((unit) => unit.id === selectedId)) return;
    const first = activeSize.units.find((unit) => unit.purchasable) ?? activeSize.units[0];
    setSelectedId(first?.id ?? "");
  }, [activeSize, selectedId]);

  const selected = activeSize?.units.find((unit) => unit.id === selectedId);
  const line = cart.data?.items.find((item) => item.product_unit_id === selectedId);

  const handleAdd = useCallback(() => {
    if (!selected) return;
    if (!user) {
      navigation.navigate("Login");
      return;
    }
    addToCart.mutate({ product_unit_id: selected.id, qty: 1 });
  }, [addToCart, navigation, selected, user]);

  if (detail.isPending) return <Loading />;

  if (detail.isError) {
    return (
      <Screen>
        {detail.error.status === 404 ? (
          <Card>
            <View style={{ gap: spacing.sm }}>
              <Text variant="heading" tone="strong">
                Produce not found
              </Text>
              <Text tone="muted">
                This produce may have been removed, or the link is wrong.
              </Text>
            </View>
          </Card>
        ) : (
          <ErrorState error={detail.error} onRetry={() => void detail.refetch()} />
        )}
      </Screen>
    );
  }

  const { product } = detail.data;
  // Everything below is scoped to the grade in hand: its packs, its gallery,
  // its stock hint. Switching grade is a local move — the whole tree came down
  // in one response, so the pictures swap with no round trip.
  const units = activeSize?.units ?? [];
  const soldOut = !product.any_unit_purchasable;
  const onSaleToday = product.available_today && !soldOut;

  // Rows of two, so the grid is two columns however wide the handset is. An
  // odd count leaves a null in the last pair, drawn as an inert hole.
  const sizeRows: (typeof sizeCodes[number] | null)[][] = [];
  for (let i = 0; i < sizeCodes.length; i += 2) {
    sizeRows.push([sizeCodes[i] ?? null, sizeCodes[i + 1] ?? null]);
  }

  return (
    <Screen
      refreshing={pull.refreshing}
      onRefresh={pull.onRefresh}
      footer={
        <View
          style={{
            flexDirection: "row",
            alignItems: "center",
            justifyContent: "space-between",
            gap: spacing.md,
          }}
        >
          <View style={{ flexShrink: 1 }}>
            <Text variant="title" tone="strong" tabular>
              {selected?.price_display ?? "—"}
            </Text>
            {selected !== undefined && (
              <Text variant="caption" tone="faint" numberOfLines={1}>
                {selected.label}
                {activeSize !== undefined && sizeCodes.length > 1
                  ? ` · size ${activeSize.code}`
                  : ""}
              </Text>
            )}
          </View>

          {line !== undefined ? (
            <QuantityStepper
              qty={line.qty}
              itemName={product.name}
              canIncrease={selected?.purchasable ?? false}
              onChange={(qty) => updateQty.mutate({ id: line.id, qty })}
              onRemove={() => removeItem.mutate(line.id)}
            />
          ) : (
            <View style={{ flex: 1, maxWidth: 220 }}>
              <Button
                label={soldOut ? "Sold out today" : "Add to cart"}
                onPress={handleAdd}
                disabled={soldOut || !selected?.purchasable}
                loading={addToCart.isPending}
              />
            </View>
          )}
        </View>
      }
    >
      <Card flush style={{ borderRadius: radius.panel }}>
        {/* Keyed on the grade so switching size resets the gallery to that
            grade's first picture rather than leaving it on an index into the
            previous one. */}
        <ProduceGallery
          key={activeSize?.id ?? product.id}
          media={activeSize?.media ?? []}
          coverUrl={activeSize?.image_url ?? product.image_url}
          name={
            activeSize !== undefined && sizeCodes.length > 1
              ? `${product.name}, size ${activeSize.code}`
              : product.name
          }
          height={galleryHeight}
          soldOut={soldOut}
          stockHint={activeSize?.stock_hint ?? product.stock_hint}
        />
      </Card>

      <View style={{ gap: spacing.sm, marginTop: spacing.xs }}>
        <View style={{ flexDirection: "row", flexWrap: "wrap", gap: spacing.xs }}>
          <Chip
            label={onSaleToday ? "Fresh today" : "Not on sale today"}
            background={onSaleToday ? colors.primary[50] : colors.surface.sunken}
            border={onSaleToday ? colors.primary[200] : colors.surface.border}
            color={onSaleToday ? colors.primary[700] : undefined}
            dot={onSaleToday ? colors.primary[500] : undefined}
          />
          <Chip
            label={product.type}
            background={colors.secondary[50]}
            border={colors.secondary[200]}
            color={colors.secondary[700]}
            capitalize
          />
          {product.grade !== null && product.grade !== "" && (
            <Chip
              label={`Grade ${product.grade}`}
              background={colors.gold[50]}
              border={colors.gold[200]}
              color={colors.gold[700]}
            />
          )}
        </View>

        <Text
          variant="display"
          tone="strong"
          accessibilityRole="header"
          style={{ fontSize: 26, lineHeight: 32, letterSpacing: -0.3 }}
        >
          {product.name}
        </Text>

        {/* Who grew it. Named because a marketplace that hides its growers is
            just a shop, and the trading name is the only supplier detail that
            belongs on a public screen. */}
        {product.supplier_name !== "" && (
          <View style={{ flexDirection: "row", alignItems: "center", gap: spacing.sm }}>
            <View
              accessibilityElementsHidden
              importantForAccessibility="no-hide-descendants"
              style={{
                width: 30,
                height: 30,
                borderRadius: 15,
                alignItems: "center",
                justifyContent: "center",
                backgroundColor: colors.primary[600],
              }}
            >
              <Text variant="label" tone="onPrimary">
                {product.supplier_name.trim().charAt(0).toUpperCase()}
              </Text>
            </View>
            <Text tone="muted">
              Grown by <Text variant="bodyStrong" tone="strong">{product.supplier_name}</Text>
            </Text>
          </View>
        )}

        {/* The price of what is chosen, up top where the eye lands. */}
        {selected !== undefined && (
          <View
            style={{
              flexDirection: "row",
              alignItems: "baseline",
              flexWrap: "wrap",
              columnGap: spacing.sm,
              marginTop: spacing.sm,
            }}
          >
            <Text
              variant="title"
              tone="strong"
              tabular
              style={{ fontSize: 24, lineHeight: 30, fontWeight: "800" }}
            >
              {selected.price_display}
            </Text>
            <Text tone="muted">for {selected.label}</Text>
          </View>
        )}
      </View>

      {!product.available_today && (
        <Card tone="accent">
          <View style={{ gap: spacing.xs }}>
            <Text variant="bodyStrong" tone="strong">
              Not on sale today
            </Text>
            <Text tone="body">
              This grower hasn’t listed it for today, or has sold out. Produce is
              listed fresh each morning — check back tomorrow.
            </Text>
          </View>
        </Card>
      )}

      <Card style={{ borderRadius: radius.panel }}>
        <View style={{ gap: spacing.xl }}>
          {/* The size selector. Hidden when there is only one grade — a grower
              who does not grade should not be shown a choice of one. */}
          {sizeCodes.length > 1 && (
            <View style={{ gap: spacing.md }}>
              <View
                style={{
                  flexDirection: "row",
                  justifyContent: "space-between",
                  alignItems: "baseline",
                }}
              >
                <Text variant="heading" tone="strong">
                  Choose a size
                </Text>
                {activeSize !== undefined && (
                  <Text variant="caption" tone="faint">
                    Selected: <Text variant="label" tone="strong">{activeSize.code}</Text>
                  </Text>
                )}
              </View>
              <View
                accessibilityRole="radiogroup"
                accessibilityLabel={`Sizes for ${product.name}`}
                style={{ gap: SIZE_GAP }}
              >
                {sizeRows.map((pair, rowIndex) => (
                  <View key={rowIndex} style={{ flexDirection: "row", gap: SIZE_GAP }}>
                    {pair.map((sizeCode, cellIndex) => {
                      if (sizeCode === null) {
                        // An odd number of grades leaves a hole in the last
                        // row, and a half-empty row reads as a cell that failed
                        // to load. This is that hole, drawn: inert, and hidden
                        // from screen readers.
                        return (
                          <View
                            key={`hole-${cellIndex}`}
                            accessibilityElementsHidden
                            importantForAccessibility="no-hide-descendants"
                            style={{
                              flex: 1,
                              borderWidth: 1.5,
                              borderColor: colors.surface.border,
                              borderStyle: "dashed",
                              borderRadius: 16,
                              backgroundColor: colors.surface.sunken,
                              opacity: 0.5,
                            }}
                          />
                        );
                      }
                      return (
                        <SizeCell
                          key={sizeCode.id}
                          code={sizeCode.code}
                          meta={sizeCode.meta}
                          sharePct={sizeCode.harvest_share_pct}
                          outOfStock={!sizeCode.any_unit_purchasable}
                          selected={sizeCode.id === selectedSizeId}
                          width={chipWidths[sizeCode.id] ?? 0}
                          onWidth={(width) =>
                            setChipWidths((current) =>
                              // Same width, same object: a fresh object every
                              // layout pass re-renders the whole grid, and the
                              // shimmer reads this on every render.
                              current[sizeCode.id] === width
                                ? current
                                : { ...current, [sizeCode.id]: width },
                            )
                          }
                          onSelect={() => setSelectedSizeId(sizeCode.id)}
                        />
                      );
                    })}
                  </View>
                ))}
              </View>

              {/* What the share MEANS, for the grade actually chosen. Named,
                  because the line moves when the selection does and an
                  unattributed "About 2%" leaves the reader checking which cell
                  it belongs to. */}
              {activeShare !== null && (
                <View
                  style={{
                    backgroundColor: colors.surface.sunken,
                    borderRadius: radius.input,
                    paddingHorizontal: spacing.md,
                    paddingVertical: spacing.sm,
                  }}
                >
                  <Text variant="caption" tone="muted">
                    <Text variant="caption" tone="strong" style={{ fontWeight: "700" }}>
                      {activeSize?.code}
                    </Text>
                    {" — "}
                    {harvestShareSentence(activeShare.pct, activeShare.tier)}
                  </Text>
                </View>
              )}
            </View>
          )}

          <View style={{ gap: spacing.md }}>
            <Text variant="heading" tone="strong">
              Choose a pack
            </Text>
            <View
              accessibilityRole="radiogroup"
              accessibilityLabel={`Pack sizes for ${product.name}`}
              style={{ gap: spacing.sm }}
            >
              {units.map((unit) => (
                <PackRow
                  key={unit.id}
                  unit={unit}
                  selected={unit.id === selectedId}
                  onSelect={() => setSelectedId(unit.id)}
                />
              ))}
            </View>
          </View>
        </View>
      </Card>

      {addToCart.isError && (
        <Text variant="caption" tone="danger" accessibilityRole="alert">
          {addToCart.error.message}
        </Text>
      )}

      {/* Three facts, not slogans — the same three the shop front makes, each
          one something the platform actually does. */}
      <View style={{ flexDirection: "row", gap: spacing.sm }}>
        {[
          { glyph: "❦", title: "Listed today", body: "by the grower" },
          { glyph: "◷", title: "Order by 4 pm", body: "daily cutoff" },
          { glyph: "⌂", title: "Direct", body: "no cold store" },
        ].map((fact) => (
          <View
            key={fact.title}
            style={{
              flex: 1,
              alignItems: "center",
              gap: 4,
              paddingVertical: spacing.md,
              paddingHorizontal: spacing.xs,
              borderRadius: 16,
              borderWidth: 1,
              borderColor: colors.primary[100],
              backgroundColor: colors.primary[50],
            }}
          >
            <View
              accessibilityElementsHidden
              importantForAccessibility="no-hide-descendants"
              style={{
                width: 30,
                height: 30,
                borderRadius: 15,
                alignItems: "center",
                justifyContent: "center",
                backgroundColor: colors.surface.raised,
                ...shadow.card,
              }}
            >
              <Text variant="label" style={{ color: colors.primary[600] }}>
                {fact.glyph}
              </Text>
            </View>
            <Text variant="label" tone="strong" center>
              {fact.title}
            </Text>
            <Text variant="caption" tone="faint" center>
              {fact.body}
            </Text>
          </View>
        ))}
      </View>

      {product.description !== null && product.description !== "" && (
        <View style={{ gap: spacing.md, marginTop: spacing.sm }}>
          <View style={{ flexDirection: "row", alignItems: "center", gap: spacing.md }}>
            <Text
              variant="label"
              style={{
                color: colors.primary[700],
                textTransform: "uppercase",
                letterSpacing: 1.8,
                fontSize: 11,
              }}
            >
              About this produce
            </Text>
            <View style={{ flex: 1, height: 1, backgroundColor: colors.surface.border }} />
          </View>
          <Text tone="body" style={{ lineHeight: 23 }}>
            {product.description}
          </Text>
        </View>
      )}
    </Screen>
  );
}

/** A small rounded label in the header row, with an optional status dot. */
function Chip({
  label,
  background,
  border,
  color,
  dot,
  capitalize = false,
}: {
  label: string;
  background: string;
  border: string;
  color?: string | undefined;
  dot?: string | undefined;
  capitalize?: boolean;
}): ReactElement {
  return (
    <View
      style={{
        flexDirection: "row",
        alignItems: "center",
        gap: 6,
        paddingHorizontal: spacing.md,
        paddingVertical: 4,
        borderRadius: radius.pill,
        borderWidth: 1,
        borderColor: border,
        backgroundColor: background,
      }}
    >
      {dot !== undefined && (
        <View style={{ width: 7, height: 7, borderRadius: 4, backgroundColor: dot }} />
      )}
      <Text
        variant="caption"
        tone="muted"
        style={{
          fontWeight: "700",
          ...(color !== undefined && { color }),
          ...(capitalize && { textTransform: "capitalize" }),
        }}
      >
        {label}
      </Text>
    </View>
  );
}

/**
 * One grade: a rectangle, two rows split left and right — the code against
 * its share of the harvest, the grower's size range against what the share
 * is OF. Selection is a green edge plus a tick outside the corner, which does
 * not depend on telling two greens apart next to a gold edge.
 */
function SizeCell({
  code,
  meta,
  sharePct,
  outOfStock,
  selected,
  width,
  onWidth,
  onSelect,
}: {
  code: string;
  meta: string | null;
  sharePct: number | null;
  outOfStock: boolean;
  selected: boolean;
  width: number;
  onWidth: (width: number) => void;
  onSelect: () => void;
}): ReactElement {
  const tier = harvestTier(sharePct);
  const metal = tier === null || outOfStock ? null : METAL[tier];
  const hasMeta = meta !== null && meta !== "";

  return (
    <Pressable
      onPress={onSelect}
      accessibilityRole="radio"
      accessibilityState={{ selected }}
      accessibilityLabel={
        `Size ${code}` +
        (hasMeta ? `, ${meta}` : "") +
        (tier !== null && !outOfStock
          ? `, ${harvestTierLabel(tier)}, ${String(sharePct)} percent of the harvest`
          : "") +
        (outOfStock ? ", sold out" : "")
      }
      onLayout={(event) => {
        // Read the width HERE: React Native pools synthetic events and
        // nullifies `nativeEvent` once the handler returns, and the state
        // updater runs later.
        onWidth(event.nativeEvent.layout.width);
      }}
      style={{
        flex: 1,
        minHeight: 64,
        borderWidth: 1.5,
        borderColor: selected
          ? colors.primary[600]
          : (metal?.border ?? colors.surface.border),
        borderRadius: 16,
        backgroundColor: outOfStock ? colors.surface.sunken : colors.surface.raised,
        opacity: outOfStock ? 0.55 : 1,
        ...(selected && shadow.card),
      }}
    >
      <View
        style={{
          flex: 1,
          justifyContent: "center",
          gap: 6,
          paddingHorizontal: spacing.md,
          paddingVertical: spacing.sm,
          borderRadius: 14,
          // The sweep is inside the cell and must not paint over its
          // neighbours — clipped here, not on the Pressable, so the tick can
          // sit over the corner.
          overflow: "hidden",
          backgroundColor: metal?.body ?? "transparent",
        }}
      >
        {/* On the rarest grade only. A shimmer on every cell is a shimmer on
            none. */}
        {tier === "rare" && !outOfStock && width > 0 && <RarityShimmer width={width} />}

        <View
          style={{
            flexDirection: "row",
            alignItems: "center",
            justifyContent: "space-between",
            gap: spacing.xs,
          }}
        >
          <View style={{ flexDirection: "row", alignItems: "center", gap: 6, flexShrink: 1 }}>
            <Text variant="bodyStrong" tone="strong" numberOfLines={1} style={{ fontSize: 16, fontWeight: "700" }}>
              {code}
            </Text>
            {tier === "rare" && !outOfStock && (
              <View
                style={{
                  paddingHorizontal: 5,
                  paddingVertical: 1,
                  borderRadius: radius.pill,
                  backgroundColor: colors.gold[500],
                }}
              >
                <Text
                  variant="caption"
                  tone="onPrimary"
                  style={{ fontSize: 9, fontWeight: "800", letterSpacing: 0.6 }}
                >
                  {harvestTierLabel(tier).toUpperCase()}
                </Text>
              </View>
            )}
          </View>
          {tier !== null && !outOfStock && (
            <Text variant="label" tone="strong" tabular>
              {sharePct}%
            </Text>
          )}
          {outOfStock && (
            <Text variant="caption" tone="faint">
              Sold out
            </Text>
          )}
        </View>

        {(hasMeta || (tier !== null && !outOfStock)) && (
          <View
            style={{
              flexDirection: "row",
              alignItems: "center",
              justifyContent: "space-between",
              gap: spacing.xs,
            }}
          >
            <Text variant="caption" tone="muted" numberOfLines={1} style={{ flexShrink: 1, fontSize: 11 }}>
              {meta ?? ""}
            </Text>
            {tier !== null && !outOfStock && (
              <Text variant="caption" tone="faint" style={{ fontSize: 10 }}>
                of harvest
              </Text>
            )}
          </View>
        )}
      </View>

      {selected && (
        <View
          accessibilityElementsHidden
          importantForAccessibility="no-hide-descendants"
          style={{
            position: "absolute",
            top: -7,
            right: -7,
            width: 20,
            height: 20,
            borderRadius: 10,
            alignItems: "center",
            justifyContent: "center",
            borderWidth: 2,
            borderColor: colors.surface.raised,
            backgroundColor: colors.primary[600],
          }}
        >
          <Text variant="caption" tone="onPrimary" style={{ fontSize: 10, fontWeight: "800" }}>
            ✓
          </Text>
        </View>
      )}
    </Pressable>
  );
}

/**
 * One pack size: a drawn radio, the label, the grower's detail for it, price.
 *
 * A per-kilo figure sat under the label too. It read as clutter beside a pack
 * that already names its own weight, and it competed with the detail line the
 * grower actually wrote.
 */
function PackRow({
  unit,
  selected,
  onSelect,
}: {
  unit: CatalogUnitDetail;
  selected: boolean;
  onSelect: () => void;
}): ReactElement {
  const disabled = !unit.purchasable;
  const on = selected && !disabled;

  return (
    <Pressable
      onPress={onSelect}
      disabled={disabled}
      accessibilityRole="radio"
      accessibilityState={{ selected, disabled }}
      accessibilityLabel={
        disabled
          ? `${unit.label}, not available today`
          : `${unit.label}${
              unit.meta !== null && unit.meta !== "" ? `, ${unit.meta}` : ""
            }, ${unit.price_display}`
      }
      style={{
        flexDirection: "row",
        alignItems: "center",
        gap: spacing.md,
        minHeight: 64,
        paddingHorizontal: spacing.md,
        borderRadius: 16,
        borderWidth: 1.5,
        borderColor: on ? colors.primary[600] : colors.surface.border,
        backgroundColor: disabled
          ? colors.surface.sunken
          : on
            ? colors.primary[50]
            : colors.surface.raised,
      }}
    >
      <View
        style={{
          width: 20,
          height: 20,
          borderRadius: 10,
          borderWidth: 2,
          alignItems: "center",
          justifyContent: "center",
          borderColor: on ? colors.primary[600] : colors.surface.border,
          backgroundColor: on ? colors.primary[600] : colors.surface.raised,
        }}
      >
        {on && (
          <View
            style={{ width: 8, height: 8, borderRadius: 4, backgroundColor: colors.surface.raised }}
          />
        )}
      </View>
      <View style={{ flex: 1 }}>
        <Text
          variant="bodyStrong"
          tone={disabled ? "faint" : "strong"}
          style={disabled ? { textDecorationLine: "line-through" } : undefined}
        >
          {unit.label}
        </Text>
        {/* What the grower wanted to say about this pack and could not fit in
            its name. The only line under the label. */}
        {unit.meta !== null && unit.meta !== "" && (
          <Text variant="caption" tone={disabled ? "faint" : "muted"}>
            {unit.meta}
          </Text>
        )}
        {disabled && (
          <Text variant="caption" tone="faint">
            Not available today
          </Text>
        )}
      </View>
      <Text
        variant="bodyStrong"
        tone={disabled ? "faint" : "strong"}
        tabular
        style={{ fontWeight: "700" }}
      >
        {unit.price_display}
      </Text>
    </Pressable>
  );
}
