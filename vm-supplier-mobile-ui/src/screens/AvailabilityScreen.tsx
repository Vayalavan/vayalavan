/**
 * Today's availability — the supplier's daily driver, and the screen this whole
 * app exists for.
 *
 * One job done fast on a phone at 6am: every active product in one list, a big
 * number box per GRADE, one Save. Undeclared products are shown first and
 * marked, because a product with no declaration is not on sale — the single
 * most costly thing to overlook.
 *
 * A card is a product; the boxes inside it are its grades. The declaration is
 * per grade — M and XL are separate crates with separate gram pools — but
 * three near-identical cards for one pomegranate made the grower compare size
 * chips to tell them apart.
 */
import { useCallback, useEffect, useMemo, useState, type ReactElement } from "react";
import { Image, View } from "react-native";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { Button } from "../components/Button";
import { Card } from "../components/Card";
import { Badge, Banner, EmptyState, ErrorState, Loading } from "../components/Feedback";
import { TextField } from "../components/Field";
import { PageHeading, Screen } from "../components/Screen";
import { Text } from "../components/Text";
import type { ApiError } from "../lib/api";
import { api } from "../lib/client";
import { formatISTDate } from "../lib/datetime";
import { formatGrams, gramsToKg, kgToGrams } from "../lib/money";
import type {
  CopyYesterdayResult,
  SaveAvailabilityResult,
  Sheet,
  SheetRow,
} from "../lib/types";
import { colors, radius, spacing } from "../theme/tokens";

/** Grams the supplier has typed, keyed by SIZE CODE id. Absent means untouched. */
type Draft = Record<string, string>;

/**
 * One product and every grade of it on this sheet.
 *
 * Only decides what shares a card. What is saved is still per grade.
 */
interface ProductGroup {
  productId: string;
  name: string;
  type: string;
  grade: string | null;
  rows: SheetRow[];
}

/**
 * Sort key: nothing declared first, then a grade still missing, then the rest.
 * A product with no declaration is not on sale at all, which is the costliest
 * thing on this screen to scroll past.
 */
function groupRank(group: ProductGroup): number {
  const declared = group.rows.filter((row) => row.declared).length;
  if (declared === 0) return 0;
  if (declared < group.rows.length) return 1;
  return 2;
}

export function AvailabilityScreen(): ReactElement {
  const queryClient = useQueryClient();
  const [draft, setDraft] = useState<Draft>({});
  const [banner, setBanner] = useState<string | null>(null);
  const [errorDetail, setErrorDetail] = useState<string | null>(null);

  const sheet = useQuery<Sheet, ApiError>({
    queryKey: ["availability"],
    queryFn: () => api.get<Sheet>("/supplier/availability"),
    // The sheet changes as customers buy; keep it fresh without hammering.
    staleTime: 15_000,
  });

  // Seed the boxes from what is already declared, without clobbering edits in
  // progress: only rows the supplier has not touched are refilled.
  useEffect(() => {
    const data = sheet.data;
    if (!data) return;
    setDraft((current) => {
      const next = { ...current };
      for (const row of data.products) {
        if (next[row.size_code_id] === undefined) {
          next[row.size_code_id] = row.declared ? gramsToKg(row.total_grams) : "";
        }
      }
      return next;
    });
  }, [sheet.data]);

  // One card per product, its grades inside it, undeclared produce first.
  // The grades keep the grower's own order, as the sheet returned them.
  const groups = useMemo<ProductGroup[]>(() => {
    if (!sheet.data) return [];

    const byProduct = new Map<string, ProductGroup>();
    for (const row of sheet.data.products) {
      let group = byProduct.get(row.product_id);
      if (group === undefined) {
        group = {
          productId: row.product_id,
          name: row.name,
          type: row.type,
          grade: row.grade,
          rows: [],
        };
        byProduct.set(row.product_id, group);
      }
      group.rows.push(row);
    }

    return [...byProduct.values()].sort((a, b) => {
      const rankA = groupRank(a);
      const rankB = groupRank(b);
      if (rankA !== rankB) return rankA - rankB;
      return a.name.localeCompare(b.name);
    });
  }, [sheet.data]);

  // Flat, for the save payload and the unsaved-changes count: what is declared
  // is still a grade, whatever the cards group it into.
  const rows = useMemo(() => groups.flatMap((group) => group.rows), [groups]);

  /**
   * The rows the supplier actually edited — and the ONLY rows a save submits.
   *
   * The web app learned this the hard way: it sent every row that had a number
   * in its box, and combined with an upsert that forced status back to 'open',
   * editing one product re-declared all the others and put everything the
   * supplier had closed that day back on sale. The count on screen and the
   * payload come from the same place, so they cannot disagree.
   *
   * Compared in grams rather than as typed text, so "50" and "50.0" are the
   * same declaration and neither counts as a change.
   */
  const changedEntries = useMemo(
    () =>
      rows.flatMap((row) => {
        const typed = draft[row.size_code_id];
        if (typed === undefined) return [];
        const grams = kgToGrams(typed);
        // A blank box means "no declaration", not "zero".
        if (grams === null) return [];
        if (row.declared && grams === row.total_grams) return [];
        return [{ size_code_id: row.size_code_id, total_grams: grams }];
      }),
    [rows, draft],
  );

  const save = useMutation<SaveAvailabilityResult, ApiError>({
    mutationFn: () =>
      api.put<SaveAvailabilityResult>("/supplier/availability", {
        entries: changedEntries,
      }),
    onSuccess: (result) => {
      setBanner(`Saved ${result.saved} product${result.saved === 1 ? "" : "s"}.`);
      setErrorDetail(null);
      void queryClient.invalidateQueries({ queryKey: ["availability"] });
    },
    onError: (err) => {
      setBanner(null);
      // The reduce-below-committed case names the product and the floor.
      setErrorDetail(err.message);
    },
  });

  const copyYesterday = useMutation<CopyYesterdayResult, ApiError>({
    mutationFn: () =>
      api.post<CopyYesterdayResult>("/supplier/availability/copy-from-yesterday", {}),
    onSuccess: (result) => {
      setBanner(
        `Copied ${result.copied} product${result.copied === 1 ? "" : "s"} from yesterday` +
          (result.skipped > 0
            ? `. ${result.skipped} skipped — they already have orders today.`
            : "."),
      );
      setErrorDetail(null);
      // Discard local edits so the copied numbers show.
      setDraft({});
      void queryClient.invalidateQueries({ queryKey: ["availability"] });
    },
    onError: (err) => {
      setBanner(null);
      setErrorDetail(err.message);
    },
  });

  const close = useMutation<unknown, ApiError, string>({
    mutationFn: (availabilityId) =>
      api.post(`/supplier/availability/${availabilityId}/close`),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ["availability"] }),
    onError: (err) => setErrorDetail(err.message),
  });

  const reopen = useMutation<unknown, ApiError, string>({
    mutationFn: (availabilityId) =>
      api.post(`/supplier/availability/${availabilityId}/reopen`),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ["availability"] }),
    onError: (err) => setErrorDetail(err.message),
  });

  const setRow = useCallback((sizeCodeId: string, value: string) => {
    setDraft((current) => ({ ...current, [sizeCodeId]: value }));
  }, []);

  if (sheet.isPending) return <Loading label="Loading today's sheet…" />;
  if (sheet.isError) {
    return (
      <Screen>
        <ErrorState error={sheet.error} onRetry={() => void sheet.refetch()} />
      </Screen>
    );
  }

  const data = sheet.data;
  const dirtyCount = changedEntries.length;

  // Product-level, matching the cards: one with nothing declared is off sale,
  // one with only some grades declared is on sale out of fewer crates.
  const undeclared = groups.filter((group) => groupRank(group) === 0);
  const partial = groups.filter((group) => groupRank(group) === 1);

  return (
    <Screen
      onRefresh={() => void sheet.refetch()}
      refreshing={sheet.isRefetching}
      footer={
        groups.length === 0 ? undefined : (
          <View
            style={{
              flexDirection: "row",
              alignItems: "center",
              justifyContent: "space-between",
              gap: spacing.md,
            }}
          >
            <Text variant="caption" tone="muted" style={{ flex: 1 }}>
              {dirtyCount > 0
                ? `${dirtyCount} unsaved change${dirtyCount === 1 ? "" : "s"}`
                : "No unsaved changes"}
            </Text>
            <Button
              label={save.isPending ? "Saving…" : "Save all"}
              onPress={() => save.mutate()}
              loading={save.isPending}
              disabled={dirtyCount === 0}
            />
          </View>
        )
      }
    >
      <PageHeading
        title="Today's availability"
        description={
          data.is_today
            ? `What you can sell today, ${formatISTDate(data.date)}. Customers only see produce declared for today.`
            : `Showing ${formatISTDate(data.date)}.`
        }
        actions={
          <Button
            label={copyYesterday.isPending ? "Copying…" : "Copy yesterday"}
            onPress={() => copyYesterday.mutate()}
            loading={copyYesterday.isPending}
            variant="secondary"
            size="sm"
            accessibilityHint="Declares the same quantities you declared yesterday"
          />
        }
      />

      {/* Counted over PRODUCTS, matching the cards below. The server's
          undeclared_count is per grade, which read as "5 products" to a grower
          who has five grades of one fruit. */}
      {undeclared.length > 0 && (
        <Banner
          tone="warning"
          message={
            `${undeclared.length} product${undeclared.length === 1 ? " is" : "s are"} not ` +
            `declared for today — ${undeclared.length === 1 ? "it is" : "they are"} not on sale.`
          }
        />
      )}

      {/* A product with only some grades declared IS on sale, just out of
          fewer crates — a milder thing to say, said separately. */}
      {partial.length > 0 && (
        <Banner
          tone="warning"
          message={
            `${partial.length} product${partial.length === 1 ? " has a size" : "s have sizes"} ` +
            `with no declaration.`
          }
        />
      )}

      {banner !== null && (
        <Banner tone="success" message={banner} onDismiss={() => setBanner(null)} />
      )}
      {errorDetail !== null && (
        <Banner tone="error" message={errorDetail} onDismiss={() => setErrorDetail(null)} />
      )}

      {groups.length === 0 ? (
        <EmptyState
          title="No active produce yet"
          body="Add produce on the Produce tab before declaring what you have today."
        />
      ) : (
        groups.map((group) => (
          <ProductSheetCard
            key={group.productId}
            group={group}
            draft={draft}
            onChange={setRow}
            onClose={(availabilityId) => close.mutate(availabilityId)}
            closing={close.isPending}
            onReopen={(availabilityId) => reopen.mutate(availabilityId)}
            reopening={reopen.isPending}
          />
        ))
      )}
    </Screen>
  );
}

/**
 * One product on the sheet: the produce at the top, a box per grade beneath.
 *
 * The grades are what carry a declaration, so each keeps its own number box,
 * its own stock line and its own close/reopen. What the card adds is the
 * context that was missing when they were separate cards — that these three
 * crates are one pomegranate.
 */
function ProductSheetCard({
  group,
  draft,
  onChange,
  onClose,
  closing,
  onReopen,
  reopening,
}: {
  group: ProductGroup;
  draft: Draft;
  onChange: (sizeCodeId: string, value: string) => void;
  onClose: (availabilityId: string) => void;
  closing: boolean;
  onReopen: (availabilityId: string) => void;
  reopening: boolean;
}): ReactElement {
  const graded = group.rows.length > 1;
  const declaredCount = group.rows.filter((row) => row.declared).length;
  // The produce's own picture: the first grade that has one. Each grade's
  // photographs are its own, so a graded product also shows them per row.
  const cover = group.rows.find((row) => row.image_url !== null)?.image_url ?? null;
  // An ungraded product has exactly one implicit size code. Its status belongs
  // in the header beside the name, not repeated on a row of its own.
  const single = graded ? undefined : group.rows[0];

  return (
    <Card tone={declaredCount === group.rows.length ? "default" : "accent"}>
      <View style={{ gap: spacing.md }}>
        <View style={{ flexDirection: "row", gap: spacing.md, alignItems: "flex-start" }}>
          <Thumbnail uri={cover} size={56} />

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
                {group.name}
              </Text>
              {group.grade !== null && <Badge label={group.grade} />}
              {declaredCount === 0 && <Badge label="Not declared" tone="accent" />}
              {single !== undefined && single.declared && single.status === "closed" && (
                <Badge label="Closed" />
              )}
              {/* Only worth saying on a graded product: "1 of 1 sizes
                  declared" tells a grower who does not grade nothing. */}
              {graded && declaredCount > 0 && (
                <Badge
                  label={`${declaredCount} of ${group.rows.length} sizes declared`}
                  tone={declaredCount === group.rows.length ? "primary" : "accent"}
                />
              )}
            </View>

            <Text variant="caption" tone="faint" style={{ textTransform: "capitalize" }}>
              {group.type}
            </Text>
          </View>
        </View>

        {group.rows.map((row, index) => (
          <SizeCodeRow
            key={row.size_code_id}
            row={row}
            // A grower who does not grade has one implicit size code and
            // should not be shown a chip reading "STD" against a choice of
            // one — the same rule the storefront's selector follows.
            showSizeCode={graded}
            productName={group.name}
            // A line between the grades, not above the first: on a product
            // with four crates the boxes have to read as four declarations.
            divided={index > 0}
            value={draft[row.size_code_id] ?? ""}
            onChange={(value) => onChange(row.size_code_id, value)}
            onClose={() => {
              if (row.availability_id !== null) onClose(row.availability_id);
            }}
            closing={closing}
            onReopen={() => {
              if (row.availability_id !== null) onReopen(row.availability_id);
            }}
            reopening={reopening}
          />
        ))}
      </View>
    </Card>
  );
}

/** A square produce thumbnail, or its empty placeholder at the same size. */
function Thumbnail({ uri, size }: { uri: string | null; size: number }): ReactElement {
  const style = {
    width: size,
    height: size,
    borderRadius: radius.card,
    backgroundColor: colors.surface.sunken,
  };
  if (uri === null) return <View accessible={false} style={style} />;
  return <Image source={{ uri }} style={style} accessibilityLabel="" />;
}

/** One grade's declaration: the crate, what is left of it, and its number box. */
function SizeCodeRow({
  row,
  showSizeCode,
  productName,
  divided,
  value,
  onChange,
  onClose,
  closing,
  onReopen,
  reopening,
}: {
  row: SheetRow;
  showSizeCode: boolean;
  productName: string;
  divided: boolean;
  value: string;
  onChange: (value: string) => void;
  onClose: () => void;
  closing: boolean;
  onReopen: () => void;
  reopening: boolean;
}): ReactElement {
  const grams = kgToGrams(value);
  const invalid = value.trim() !== "" && grams === null;
  // The floor the server will enforce, shown before they hit Save.
  const committed = row.reserved_grams + row.sold_grams;
  const belowCommitted = grams !== null && grams < committed;

  const error = invalid
    ? "Enter a number of kilograms, for example 12.5"
    : belowCommitted
      ? `Customers have already ordered ${formatGrams(committed)}. You cannot declare less than that.`
      : undefined;

  return (
    <View
      style={{
        gap: spacing.sm,
        ...(divided
          ? {
              borderTopWidth: 1,
              borderTopColor: colors.surface.border,
              paddingTop: spacing.md,
            }
          : {}),
      }}
    >
      {showSizeCode && (
        <View
          style={{
            flexDirection: "row",
            flexWrap: "wrap",
            alignItems: "center",
            gap: spacing.sm,
          }}
        >
          {/* The grade's OWN photograph, small: the pictures are of the grade,
              and on a graded product they are the fastest way to tell the
              crates apart. */}
          <Thumbnail uri={row.image_url} size={32} />
          <Badge
            label={
              row.size_meta !== null && row.size_meta !== ""
                ? `${row.size_code} · ${row.size_meta}`
                : row.size_code
            }
            tone="primary"
          />
          {!row.declared && <Badge label="Not declared" tone="accent" />}
          {row.status === "closed" && <Badge label="Closed" />}
        </View>
      )}

      {row.declared && (
        <Text variant="caption" tone="muted">
          {/* Sellable, not remaining: a closed product has stock on the books
              but none a customer can buy, and saying "50 kg left" made closing
              look like it had not worked. The declaration is still shown
              alongside, so reopening holds no surprises. */}
          {row.status === "closed"
            ? `Not on sale · 0 kg sellable${
                row.total_grams > 0
                  ? ` · ${formatGrams(row.total_grams)} declared, held back`
                  : ""
              }`
            : `${formatGrams(row.sellable_grams)} left`}
          {committed > 0 ? ` · ${formatGrams(committed)} already ordered` : ""}
        </Text>
      )}

      <View style={{ flexDirection: "row", gap: spacing.md, alignItems: "flex-end" }}>
        <TextField
          style={{ flex: 1 }}
          label="Available today"
          accessibilityLabel={
            showSizeCode
              ? `Kilograms available for ${productName}, size ${row.size_code}`
              : `Kilograms available for ${productName}`
          }
          value={value}
          onChangeText={onChange}
          placeholder="0"
          // "decimal-pad" brings up the number pad without the spinner and
          // locale rounding a numeric keyboard type would add.
          keyboardType="decimal-pad"
          alignRight
          suffix="kg"
          error={error}
          returnKeyType="done"
        />

        {row.declared && row.status === "closed" && (
          <Button
            label="Reopen"
            onPress={onReopen}
            disabled={reopening}
            variant="secondary"
            size="sm"
            accessibilityHint={
              showSizeCode
                ? `Puts size ${row.size_code} back on sale today`
                : "Puts this back on sale today"
            }
            style={{ marginBottom: spacing.xs }}
          />
        )}

        {row.declared && row.status === "open" && (
          <Button
            label="Close"
            onPress={onClose}
            disabled={closing}
            variant="secondary"
            size="sm"
            accessibilityHint={
              showSizeCode
                ? `Stops selling size ${row.size_code} today`
                : "Stops selling this today"
            }
            style={{ marginBottom: spacing.xs }}
          />
        )}
      </View>
    </View>
  );
}
