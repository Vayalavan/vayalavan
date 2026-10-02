/**
 * Today's produce — the screen this app exists for.
 *
 * A FlatList rather than the shared <Screen>, and it is the only screen that
 * breaks that rule. <Screen> is a ScrollView, and a ScrollView renders every
 * child up front: on a morning where forty growers have listed, that is forty
 * presigned image fetches before the customer has scrolled past the third
 * card. The filters and hero ride along as ListHeaderComponent so the whole
 * thing still scrolls as one page.
 *
 * The date is never sent to the server: what counts as "today" is a server
 * decision in Asia/Kolkata (CLAUDE.md rule 2), and a phone with a skewed clock
 * must not be able to shop against another day's stock.
 */
import { useCallback, useEffect, useMemo, useState, type ReactElement } from "react";
import { FlatList, Pressable, RefreshControl, View } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { useNavigation } from "@react-navigation/native";
import type { NativeStackNavigationProp } from "@react-navigation/native-stack";
import { useQuery } from "@tanstack/react-query";

import { Button } from "../components/Button";
import { CategoryTiles } from "../components/CategoryTiles";
import { HomeHero } from "../components/HomeHero";
import { WhyVayal } from "../components/WhyVayal";
import { Card } from "../components/Card";
import { CutoffBanner } from "../components/CutoffBanner";
import { EmptyState, ErrorState, Loading } from "../components/Feedback";
import { ProduceImage } from "../components/ProduceImage";
import { QuantityStepper } from "../components/QuantityStepper";
import { Text } from "../components/Text";
import type { ApiError } from "../lib/api";
import { useAddToCart, useCart, useRemoveCartItem, useUpdateCartQty } from "../lib/cart";
import { summariseCatalogCounts } from "../lib/catalogCounts";
import { api } from "../lib/client";
import { formatISTDate } from "../lib/datetime";
import { usePullToRefresh } from "../lib/usePullToRefresh";
import { useAuth } from "../lib/auth";
import type {
  Catalog,
  CatalogCardSizeCode,
  CatalogProduct,
  CatalogUnit,
} from "../lib/types";
import type { RootStackParamList, ShopStackParamList } from "../navigation/types";
import { colors, radius, spacing } from "../theme/tokens";

/**
 * Category filters. "" is All, and the labels are what a customer would say —
 * "Vegetables", not the API's singular "vegetable".
 */
const CATEGORIES: ReadonlyArray<readonly [string, string]> = [
  ["", "All"],
  ["fruit", "Fruits"],
  ["vegetable", "Vegetables"],
  ["microgreen", "Microgreens"],
  ["other", "Other"],
];

type ShopNavigation = NativeStackNavigationProp<ShopStackParamList> &
  NativeStackNavigationProp<RootStackParamList>;

export function CatalogScreen(): ReactElement {
  const insets = useSafeAreaInsets();
  const [type, setType] = useState("");
  const [grade, setGrade] = useState("");

  const catalog = useQuery<Catalog, ApiError>({
    queryKey: ["catalog", type, grade],
    queryFn: () =>
      api.get<Catalog>("/catalog", {
        query: { type: type || undefined, grade: grade || undefined },
      }),
  });

  const { refreshing, onRefresh } = usePullToRefresh(catalog.refetch);

  const dateLine = catalog.data
    ? ` Available for ${formatISTDate(catalog.data.date)}.`
    : "";

  const header = useMemo(
    () => (
      <View style={{ gap: spacing.md, paddingBottom: spacing.md }}>
        <HomeHero dateLine={dateLine} />

        <CutoffBanner />

        {/* The category filter, as photographs. Same values and the same one
            piece of state the chips carried. */}
        <CategoryTiles categories={CATEGORIES} value={type} onChange={setType} />

        {/* Grade is a secondary filter, and only appears on days when growers
            have actually graded what they listed. */}
        {catalog.data && catalog.data.grades.length > 0 && (
          <FilterChips
            label="Filter by grade"
            options={[
              ["", "All grades"],
              ...catalog.data.grades.map((g) => [g, `Grade ${g}`] as const),
            ]}
            value={grade}
            onChange={setGrade}
          />
        )}
      </View>
    ),
    [catalog.data, dateLine, grade, type],
  );

  if (catalog.isPending) return <Loading label="Loading today’s produce…" />;

  if (catalog.isError) {
    return (
      <View style={{ flex: 1, padding: spacing.lg }}>
        <ErrorState error={catalog.error} onRetry={onRefresh} />
      </View>
    );
  }

  const products = catalog.data.products;

  return (
    <FlatList
      data={products}
      keyExtractor={(product) => product.id}
      renderItem={({ item }) => <ProductCard product={item} />}
      ListHeaderComponent={header}
      ListEmptyComponent={
        <EmptyState
          title="Nothing available right now"
          body={
            type !== "" || grade !== ""
              ? "No produce matches those filters today."
              : "Our growers have not listed produce for today yet. Please check back later this morning."
          }
        />
      }
      ListFooterComponent={
        <>
          {products.length > 0 && (
            <Text variant="caption" tone="muted" style={{ paddingTop: spacing.md }}>
              {summariseCatalogCounts(catalog.data.sellable_total, catalog.data.total)}
            </Text>
          )}
          {/* At the foot: someone who opened the app came to shop, and
              someone still deciding scrolls to the end and reads. */}
          <WhyVayal />
        </>
      }
      contentContainerStyle={{
        padding: spacing.lg,
        gap: spacing.md,
        paddingBottom: spacing.xxl + insets.bottom,
      }}
      style={{ flex: 1, backgroundColor: colors.cream[100] }}
      refreshControl={
        <RefreshControl
          refreshing={refreshing}
          onRefresh={onRefresh}
          tintColor={colors.primary[600]}
          colors={[colors.primary[600]]}
        />
      }
    />
  );
}

/** A scrolling row of choices — the phone's answer to the web's select. */
function FilterChips({
  label,
  options,
  value,
  onChange,
}: {
  label: string;
  options: ReadonlyArray<readonly [string, string]>;
  value: string;
  onChange: (next: string) => void;
}): ReactElement {
  return (
    <View
      accessibilityRole="tablist"
      accessibilityLabel={label}
      style={{ flexDirection: "row", flexWrap: "wrap", gap: spacing.sm }}
    >
      {options.map(([optionValue, optionLabel]) => {
        const selected = optionValue === value;
        return (
          <Pressable
            key={optionValue || "all"}
            onPress={() => onChange(optionValue)}
            accessibilityRole="tab"
            accessibilityState={{ selected }}
            accessibilityLabel={optionLabel}
            style={({ pressed }) => ({
              minHeight: 40,
              justifyContent: "center",
              paddingHorizontal: spacing.lg,
              borderRadius: radius.pill,
              borderWidth: 1,
              borderColor: selected ? colors.primary[600] : colors.surface.border,
              backgroundColor: selected ? colors.primary[600] : colors.surface.raised,
              opacity: pressed ? 0.85 : 1,
            })}
          >
            <Text variant="label" tone={selected ? "onPrimary" : "body"}>
              {optionLabel}
            </Text>
          </Pressable>
        );
      })}
    </View>
  );
}

/** The pack a card should open on: the first one that can actually be bought. */
function firstBuyableId(units: CatalogUnit[]): string {
  return (units.find((unit) => unit.purchasable) ?? units[0])?.id ?? "";
}

/**
 * One card.
 *
 * Sold-out produce is shown, not hidden: a grower listed it this morning and
 * ran out, and a customer who came looking for it should learn that rather
 * than conclude we never sell it (CLAUDE.md §5.2). The card is dimmed, its
 * pack sizes are struck through, and the button says so.
 */
function ProductCard({ product }: { product: CatalogProduct }): ReactElement {
  const navigation = useNavigation<ShopNavigation>();
  const { user } = useAuth();
  const { cart } = useCart();

  const addToCart = useAddToCart();
  const updateQty = useUpdateCartQty();
  const removeItem = useRemoveCartItem();

  // Every grade came down with the listing, so switching between them is a
  // local move: the photograph, the packs and the prices swap with no request.
  // The card opens on the grade the server chose — the first with something
  // buyable.
  // Defaulted, not assumed: an older gateway or a rolled-back catalog
  // service answers without the tree, and the card still has to render
  // from the flat fields rather than throw.
  const sizeCodes = product.size_codes ?? [];
  const [selectedSizeId, setSelectedSizeId] = useState(
    product.size_code_id || sizeCodes[0]?.id || "",
  );
  const activeSize = sizeCodes.find((sc) => sc.id === selectedSizeId) ?? sizeCodes[0];

  // Everything below is scoped to the grade in hand. The flat fields are the
  // fallback for a response without the tree, and describe the same grade.
  const units = activeSize?.units ?? product.units;
  const soldOut = !(activeSize?.any_unit_purchasable ?? product.any_unit_purchasable);
  const stockHint = activeSize?.stock_hint ?? product.stock_hint;
  const imageUri = activeSize?.image_url ?? product.image_url;

  // Preselect the first pack that can actually be bought, so the common case
  // is one tap.
  const [selectedId, setSelectedId] = useState(() => firstBuyableId(units));

  const selected = units.find((unit) => unit.id === selectedId);

  // Switching grade changes which packs exist — 1 kg of M and 1 kg of XL are
  // different goods with different ids — so the selection has to move to a
  // pack of the grade now showing. Also covers stock running out under the
  // selected pack on a refresh.
  useEffect(() => {
    if (units.some((unit) => unit.id === selectedId)) return;
    setSelectedId(firstBuyableId(units));
  }, [units, selectedId]);

  // Keyed on the UNIT, not the product: someone may hold both a 1 kg and a
  // 5 kg pack of the same tomatoes, and the stepper must edit the one shown.
  const line = cart.data?.items.find((item) => item.product_unit_id === selectedId);

  // One handler behind both targets — the photo and the name — so they cannot
  // drift into navigating to different places.
  const openProduct = useCallback(() => {
    navigation.navigate("Product", { productId: product.id, name: product.name });
  }, [navigation, product.id, product.name]);

  const handleAdd = useCallback(() => {
    if (!selected) return;
    // The cart is server-side and keyed by customer, so there is nothing to
    // add to until they are signed in. Opening the modal beats a 401 rendered
    // as a generic error under the button.
    if (!user) {
      navigation.navigate("Login");
      return;
    }
    addToCart.mutate({ product_unit_id: selected.id, qty: 1 });
  }, [addToCart, navigation, selected, user]);

  return (
    <Card flush style={{ opacity: soldOut ? 0.8 : 1 }}>
      <Pressable
        onPress={openProduct}
        style={({ pressed }) => ({ opacity: pressed ? 0.85 : 1 })}
        accessibilityRole="button"
        accessibilityHint="Opens the full description and pack prices"
      >
        {/* Keyed on the grade so switching size replaces the photograph
            rather than leaving the previous grade's on screen while the new
            one loads — the pictures are OF the grade, and a stale one
            misdescribes what is being priced. */}
        <ProduceImage
          key={activeSize?.id ?? product.id}
          uri={imageUri}
          name={
            activeSize !== undefined && sizeCodes.length > 1
              ? `${product.name}, size ${activeSize.code}`
              : product.name
          }
          height={180}
          soldOut={soldOut}
          stockHint={stockHint}
        />
      </Pressable>

      <View style={{ padding: spacing.lg, gap: spacing.sm }}>
        {/* The name opens the product too, matching the web card where the
            name — not the photo — is the link. A photo is not an obvious
            target on a phone, and a customer who taps the name of the thing
            they want should not have to discover that only the picture
            works.

            It stops at the description. Everything below is how you BUY, and
            a pack chip or a stepper inside a pressable that navigates away
            means an adjust-quantity tap can leave the screen instead. The gap
            is restated here because these three moved out of the parent's. */}
        <Pressable
          onPress={openProduct}
          style={({ pressed }) => ({ gap: spacing.sm, opacity: pressed ? 0.85 : 1 })}
          accessibilityRole="button"
          accessibilityHint="Opens the full description and pack prices"
        >
          <View style={{ flexDirection: "row", alignItems: "flex-start", gap: spacing.sm }}>
            <Text variant="heading" tone="strong" style={{ flex: 1 }}>
              {product.name}
            </Text>
            {product.grade !== null && product.grade !== "" && (
              <View
                style={{
                  backgroundColor: colors.surface.sunken,
                  borderRadius: radius.pill,
                  paddingHorizontal: spacing.sm,
                  paddingVertical: 2,
                }}
              >
                <Text variant="caption" tone="muted">
                  {product.grade}
                </Text>
              </View>
            )}
          </View>

          {/* The grade is chosen below rather than named here — the chips say
              which one is showing, and the product screen is no longer the
              only place to switch. */}
          <Text variant="caption" tone="faint" style={{ textTransform: "capitalize" }}>
            {product.type}
          </Text>

          {product.description !== null && product.description !== "" && (
            <Text variant="caption" tone="muted" numberOfLines={2}>
              {product.description}
            </Text>
          )}
        </Pressable>

        {/* The size selector. Hidden when there is only one grade — a grower
            who does not grade should not be shown a choice of one. Sold-out
            grades stay tappable, unlike packs: seeing the XL photograph and
            what it would have cost is worth more than a dead chip. */}
        {sizeCodes.length > 1 && (
          <SizeChooser
            sizeCodes={sizeCodes}
            selectedId={selectedSizeId}
            onSelect={setSelectedSizeId}
            productName={product.name}
          />
        )}

        {/* The chosen grade's own words for what it is — "150 g - 200 g".
            Under the chips rather than inside them, so a row of three stays a
            row of three. */}
        {sizeCodes.length > 1 &&
          activeSize?.meta !== null &&
          activeSize?.meta !== undefined &&
          activeSize.meta !== "" && (
            <Text variant="caption" tone="faint">
              {activeSize.meta}
            </Text>
          )}

        <PackChooser
          units={units}
          selectedId={selectedId}
          onSelect={setSelectedId}
          productName={product.name}
        />

        <View
          style={{
            flexDirection: "row",
            alignItems: "flex-end",
            justifyContent: "space-between",
            gap: spacing.sm,
            marginTop: spacing.xs,
          }}
        >
          <View>
            <Text variant="title" tone="strong" tabular>
              {selected?.price_display ?? "—"}
            </Text>
            {selected !== undefined && (
              <Text variant="caption" tone="faint">
                per {selected.label}
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
            <Button
              label={soldOut ? "Sold out" : "Add to cart"}
              onPress={handleAdd}
              disabled={soldOut || !selected?.purchasable}
              loading={addToCart.isPending}
              size="sm"
              {...(selected !== undefined && {
                // The size is named when there is a choice of them: two
                // cards' worth of "Adds one 1 kg box of tomatoes" would
                // otherwise be indistinguishable to a screen reader.
                accessibilityHint:
                  activeSize !== undefined && sizeCodes.length > 1
                    ? `Adds one ${selected.label} of ${product.name}, size ${activeSize.code}`
                    : `Adds one ${selected.label} of ${product.name}`,
              })}
            />
          )}
        </View>

        {/* Failures belong on the card that caused them — stock can run out
            between the list loading and the tap, and the customer needs to
            know it was this product. */}
        {addToCart.isError && (
          <Text variant="caption" tone="danger" accessibilityRole="alert">
            {addToCart.error.message}
          </Text>
        )}
      </View>
    </Card>
  );
}

/**
 * The grades, as a row of chips.
 *
 * A sold-out grade stays selectable — unlike a pack, which is disabled —
 * because choosing it is how a customer looks at that grade's photograph and
 * price. What it cannot do is be added to the cart, which the button below
 * already refuses.
 */
function SizeChooser({
  sizeCodes,
  selectedId,
  onSelect,
  productName,
}: {
  sizeCodes: CatalogCardSizeCode[];
  selectedId: string;
  onSelect: (id: string) => void;
  productName: string;
}): ReactElement {
  return (
    <View
      accessibilityRole="radiogroup"
      accessibilityLabel={`Choose a size for ${productName}`}
      style={{ flexDirection: "row", flexWrap: "wrap", gap: spacing.sm }}
    >
      {sizeCodes.map((sizeCode) => {
        const selected = sizeCode.id === selectedId;
        const soldOut = !sizeCode.any_unit_purchasable;
        return (
          <Pressable
            key={sizeCode.id}
            onPress={() => onSelect(sizeCode.id)}
            accessibilityRole="radio"
            accessibilityState={{ selected }}
            accessibilityLabel={
              soldOut
                ? `Size ${sizeCode.code}, sold out for today`
                : `Size ${sizeCode.code}`
            }
            style={{
              minHeight: 36,
              justifyContent: "center",
              paddingHorizontal: spacing.md,
              borderRadius: radius.card,
              borderWidth: 1,
              borderColor: selected ? colors.primary[600] : colors.surface.border,
              backgroundColor: selected
                ? colors.primary[50]
                : soldOut
                  ? colors.surface.sunken
                  : colors.surface.raised,
            }}
          >
            <Text
              variant="label"
              tone={selected ? "strong" : soldOut ? "faint" : "body"}
            >
              {sizeCode.code}
            </Text>
          </Pressable>
        );
      })}
    </View>
  );
}

/** The pack sizes, as a row of chips. Unbuyable ones stay visible. */
function PackChooser({
  units,
  selectedId,
  onSelect,
  productName,
}: {
  units: CatalogUnit[];
  selectedId: string;
  onSelect: (id: string) => void;
  productName: string;
}): ReactElement {
  return (
    <View
      accessibilityRole="radiogroup"
      accessibilityLabel={`Choose a pack size for ${productName}`}
      style={{ flexDirection: "row", flexWrap: "wrap", gap: spacing.sm }}
    >
      {units.map((unit) => {
        const selected = unit.id === selectedId;
        return (
          <Pressable
            key={unit.id}
            onPress={() => onSelect(unit.id)}
            disabled={!unit.purchasable}
            accessibilityRole="radio"
            accessibilityState={{ selected, disabled: !unit.purchasable }}
            accessibilityLabel={
              unit.purchasable
                ? `${unit.label}, ${unit.price_display}`
                : `${unit.label}, not available today`
            }
            style={{
              minHeight: 36,
              justifyContent: "center",
              paddingHorizontal: spacing.md,
              borderRadius: radius.card,
              borderWidth: 1,
              // Kept visible but unselectable: the customer should see the
              // 5 kg pack exists and is simply out today.
              borderColor: selected && unit.purchasable
                ? colors.primary[600]
                : colors.surface.border,
              backgroundColor: !unit.purchasable
                ? colors.surface.sunken
                : selected
                  ? colors.primary[50]
                  : colors.surface.raised,
            }}
          >
            <Text
              variant="label"
              tone={!unit.purchasable ? "faint" : selected ? "strong" : "body"}
              style={!unit.purchasable ? { textDecorationLine: "line-through" } : undefined}
            >
              {unit.label}
            </Text>
          </Pressable>
        );
      })}
    </View>
  );
}
