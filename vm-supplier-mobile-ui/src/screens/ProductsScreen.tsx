import { useEffect, useLayoutEffect, useState, type ReactElement } from "react";
import { Image, View } from "react-native";
import type { NativeStackScreenProps } from "@react-navigation/native-stack";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { Button } from "../components/Button";
import { Card } from "../components/Card";
import { Badge, EmptyState, ErrorState, Loading } from "../components/Feedback";
import { ChoiceField, TextField } from "../components/Field";
import { PageHeading, Screen } from "../components/Screen";
import { Text } from "../components/Text";
import type { ApiError } from "../lib/api";
import { api } from "../lib/client";
import { formatGrams } from "../lib/money";
import {
  PRODUCT_STATUSES,
  PRODUCT_TYPES,
  type Product,
  type ProductPage,
} from "../lib/types";
import type { ProductsStackParamList } from "../navigation/types";
import { colors, radius, spacing } from "../theme/tokens";

const PAGE_SIZE = 20;

/** Debounces a value so typing in the search box is not one request per key. */
function useDebounced<T>(value: T, delayMs: number): T {
  const [debounced, setDebounced] = useState(value);
  useEffect(() => {
    const timer = setTimeout(() => setDebounced(value), delayMs);
    return () => clearTimeout(timer);
  }, [value, delayMs]);
  return debounced;
}

type Props = NativeStackScreenProps<ProductsStackParamList, "ProductList">;

const TYPE_OPTIONS = [
  { value: "", label: "All" },
  ...PRODUCT_TYPES.map((t) => ({ value: t, label: t[0]!.toUpperCase() + t.slice(1) })),
] as const;

const STATUS_OPTIONS = [
  { value: "", label: "All" },
  ...PRODUCT_STATUSES.map((s) => ({ value: s, label: s[0]!.toUpperCase() + s.slice(1) })),
] as const;

export function ProductsScreen({ navigation }: Props): ReactElement {
  const [search, setSearch] = useState("");
  const [type, setType] = useState("");
  const [status, setStatus] = useState("");
  const [page, setPage] = useState(0);
  const [filtersOpen, setFiltersOpen] = useState(false);

  const debouncedSearch = useDebounced(search, 300);

  // Any filter change invalidates the current page number.
  useEffect(() => setPage(0), [debouncedSearch, type, status]);

  // "Import CSV" lives in the header rather than on a tab: it is a once-a-season
  // job, and a fifth tab would shrink the four done daily.
  useLayoutEffect(() => {
    navigation.setOptions({
      headerRight: () => (
        <Button
          label="Import"
          onPress={() => navigation.navigate("Import")}
          variant="ghost"
          size="sm"
        />
      ),
    });
  }, [navigation]);

  const query = useQuery<ProductPage, ApiError>({
    queryKey: ["products", debouncedSearch, type, status, page],
    queryFn: () =>
      api.get<ProductPage>("/products", {
        query: {
          search: debouncedSearch || undefined,
          type: type || undefined,
          status: status || undefined,
          limit: PAGE_SIZE,
          offset: page * PAGE_SIZE,
        },
      }),
    // Keeps the previous page on screen while the next loads, instead of
    // flashing an empty list and jumping the scroll position.
    placeholderData: (previous) => previous,
  });

  const total = query.data?.total ?? 0;
  const pageCount = Math.max(1, Math.ceil(total / PAGE_SIZE));
  const filtered = search !== "" || type !== "" || status !== "";

  return (
    <Screen onRefresh={() => void query.refetch()} refreshing={query.isRefetching}>
      <PageHeading
        title="Your produce"
        description="Everything you list on Vayalavan."
        actions={
          <Button
            label="Add product"
            onPress={() => navigation.navigate("ProductForm", {})}
          />
        }
      />

      <Card>
        <View style={{ gap: spacing.md }}>
          <TextField
            label="Search"
            value={search}
            onChangeText={setSearch}
            placeholder="Search by name…"
            autoCapitalize="none"
            returnKeyType="done"
          />

          <Button
            label={filtersOpen ? "Hide filters" : filtered ? "Filters (on)" : "Filters"}
            onPress={() => setFiltersOpen((open) => !open)}
            variant="ghost"
            size="sm"
          />

          {/* Collapsed by default: two chip rows push the first product below
              the fold on a 360px screen, and most visits are not filtered. */}
          {filtersOpen && (
            <View style={{ gap: spacing.md }}>
              <ChoiceField
                label="Type"
                value={type}
                options={TYPE_OPTIONS}
                onChange={setType}
              />
              <ChoiceField
                label="Status"
                value={status}
                options={STATUS_OPTIONS}
                onChange={setStatus}
              />
            </View>
          )}
        </View>
      </Card>

      {query.isError && <ErrorState error={query.error} onRetry={() => void query.refetch()} />}

      {query.isPending && <Loading label="Loading your products…" />}

      {query.data?.products.length === 0 && (
        <EmptyState
          title={filtered ? "Nothing matches those filters" : "No produce yet"}
          body={
            filtered
              ? "Try a different name, type or status."
              : "Add your first product and it will show up here."
          }
          action={
            filtered ? undefined : (
              <Button
                label="Add your first product"
                onPress={() => navigation.navigate("ProductForm", {})}
              />
            )
          }
        />
      )}

      {query.data?.products.map((product) => (
        <ProductRow
          key={product.id}
          product={product}
          onEdit={() => navigation.navigate("ProductForm", { productId: product.id })}
        />
      ))}

      {query.data !== undefined && query.data.products.length > 0 && (
        <View style={{ gap: spacing.md, marginTop: spacing.sm }}>
          <Text variant="caption" tone="muted" center>
            {total} product{total === 1 ? "" : "s"}
          </Text>
          {pageCount > 1 && (
            <View
              style={{
                flexDirection: "row",
                alignItems: "center",
                justifyContent: "space-between",
                gap: spacing.md,
              }}
            >
              <Button
                label="Previous"
                onPress={() => setPage((p) => Math.max(0, p - 1))}
                disabled={page === 0}
                variant="secondary"
                size="sm"
              />
              <Text variant="caption" tone="muted" tabular>
                {page + 1} / {pageCount}
              </Text>
              <Button
                label="Next"
                onPress={() => setPage((p) => Math.min(pageCount - 1, p + 1))}
                disabled={page + 1 >= pageCount}
                variant="secondary"
                size="sm"
              />
            </View>
          )}
        </View>
      )}
    </Screen>
  );
}

const STATUS_TONE = {
  active: "primary",
  draft: "neutral",
  archived: "secondary",
} as const;

function ProductRow({
  product,
  onEdit,
}: {
  product: Product;
  onEdit: () => void;
}): ReactElement {
  const queryClient = useQueryClient();

  const archive = useMutation<Product, ApiError>({
    mutationFn: () => api.post<Product>(`/products/${product.id}/archive`),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ["products"] }),
  });

  return (
    <Card>
      <View style={{ gap: spacing.md }}>
        <View style={{ flexDirection: "row", gap: spacing.md }}>
          {product.image_url !== null ? (
            <Image
              source={{ uri: product.image_url }}
              style={{
                width: 72,
                height: 72,
                borderRadius: radius.card,
                backgroundColor: colors.surface.sunken,
              }}
              accessibilityLabel=""
            />
          ) : (
            <View
              accessible={false}
              style={{
                width: 72,
                height: 72,
                borderRadius: radius.card,
                backgroundColor: colors.surface.sunken,
                alignItems: "center",
                justifyContent: "center",
              }}
            >
              <Text variant="caption" tone="faint">
                No photo
              </Text>
            </View>
          )}

          <View style={{ flex: 1, gap: spacing.xs }}>
            <View
              style={{
                flexDirection: "row",
                flexWrap: "wrap",
                alignItems: "center",
                gap: spacing.sm,
              }}
            >
              <Text variant="heading" tone="strong">
                {product.name}
              </Text>
              <Badge label={product.status} tone={STATUS_TONE[product.status]} />
              {product.grade !== null && <Badge label={`Grade ${product.grade}`} />}
            </View>

            <Text variant="caption" tone="muted" style={{ textTransform: "capitalize" }}>
              {product.type}
            </Text>

            {/* Grouped by grade: a flat list would show "1 kg" twice at two
                prices with nothing to explain the difference. */}
            {product.size_codes.map((sizeCode) => (
              <View key={sizeCode.id} style={{ gap: 2 }}>
                <Text variant="label" tone="strong">
                  {sizeCode.code}
                  {sizeCode.meta !== null && sizeCode.meta !== ""
                    ? ` · ${sizeCode.meta}`
                    : ""}
                </Text>
                {sizeCode.packs.map((pack) => (
                  <Text
                    key={pack.id}
                    variant="caption"
                    tone="body"
                    tabular
                    style={{ paddingLeft: spacing.md }}
                  >
                    {pack.label} ({formatGrams(pack.weight_grams)}) — {pack.price_display}
                  </Text>
                ))}
              </View>
            ))}
          </View>
        </View>

        <View style={{ flexDirection: "row", gap: spacing.sm }}>
          <Button label="Edit" onPress={onEdit} variant="secondary" size="sm" />
          {product.status !== "archived" && (
            <Button
              label={archive.isPending ? "Archiving…" : "Archive"}
              onPress={() => archive.mutate()}
              disabled={archive.isPending}
              variant="secondary"
              size="sm"
            />
          )}
        </View>

        {archive.isError && (
          <Text variant="caption" tone="danger" accessibilityRole="alert">
            {archive.error.message}
          </Text>
        )}
      </View>
    </Card>
  );
}
