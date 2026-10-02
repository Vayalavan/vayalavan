import { useEffect, useMemo, useState, type ReactElement } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Card, PageHeading, ApiError } from "@vayal/ui-kit";
import { api } from "../lib/api.js";
import { formatGrams } from "../lib/money.js";

/**
 * One SIZE CODE's declaration for the day.
 *
 * A grower declares per grade, not per product: M and XL are separate crates
 * with separate gram pools, so a product with three grades is three rows here.
 */
interface SheetRow {
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

interface Sheet {
  date: string;
  /** The SERVER decides what today is, in IST. The browser's clock is not
   *  trusted for this — see CLAUDE.md rule 2. */
  is_today: boolean;
  products: SheetRow[];
  declared_count: number;
  undeclared_count: number;
}

/** Grams the supplier has typed, keyed by SIZE CODE id. "" means untouched. */
type Draft = Record<string, string>;

/**
 * One product and every grade of it declared on this sheet.
 *
 * The declaration itself is still per grade — this only decides what shares a
 * card, never what is saved.
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

/** kg in the box, grams on the wire. Returns null while unparseable. */
function kgToGrams(value: string): number | null {
  const trimmed = value.trim();
  if (trimmed === "") return null;
  if (!/^\d*\.?\d*$/.test(trimmed)) return null;
  const kg = Number(trimmed);
  if (!Number.isFinite(kg) || kg < 0) return null;
  return Math.round(kg * 1000);
}

function gramsToKg(grams: number): string {
  if (grams === 0) return "0";
  return String(Number((grams / 1000).toFixed(3)));
}

/**
 * Today's availability — the supplier's daily driver.
 *
 * Optimised for one job done fast on a phone at 6am: every active product in
 * one list, a big number box per row, and one Save. Undeclared products are
 * shown first and marked, because a product with no declaration is not on
 * sale — the single most costly thing to overlook.
 */
export function AvailabilityPage(): ReactElement {
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
    if (!sheet.data) return;
    setDraft((current) => {
      const next = { ...current };
      for (const row of sheet.data.products) {
        if (next[row.size_code_id] === undefined) {
          next[row.size_code_id] = row.declared ? gramsToKg(row.total_grams) : "";
        }
      }
      return next;
    });
  }, [sheet.data]);

  const save = useMutation<{ saved: number }, ApiError>({
    mutationFn: () =>
      api.put<{ saved: number }>("/supplier/availability", { entries: changedEntries }),
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

  const copyYesterday = useMutation<{ copied: number; skipped: number }, ApiError>({
    mutationFn: () =>
      api.post<{ copied: number; skipped: number }>(
        "/supplier/availability/copy-from-yesterday",
        {},
      ),
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

  /**
   * The sheet grouped into one card per PRODUCT, its grades inside it.
   *
   * The declaration is still per grade — M and XL are separate crates with
   * separate gram pools — but a grower with three grades of pomegranate was
   * reading three near-identical cards and had to compare the size chip to
   * tell them apart. One card, three boxes, keeps the grades together where
   * the difference between them is visible.
   *
   * Products a customer cannot buy come first, then those with a grade still
   * missing: an undeclared grade is the single most costly thing to overlook.
   * The grades inside keep the grower's own order, as the sheet returned them.
   */
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
   * This list already existed, but only to render "N unsaved changes": the
   * save sent every row that had a number in its box. Combined with an upsert
   * that forced status back to 'open', editing one product re-declared all the
   * others and put everything the supplier had closed that day back on sale.
   * The count on screen and the payload now come from the same place.
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

  const dirtyCount = changedEntries.length;

  // Product-level, matching the cards: one with nothing declared is off sale,
  // one with only some grades declared is on sale out of fewer crates.
  const undeclared = groups.filter((group) => groupRank(group) === 0);
  const partial = groups.filter((group) => groupRank(group) === 1);

  if (sheet.isPending) return <Card>Loading today's sheet…</Card>;
  if (sheet.isError) {
    return (
      <Card>
        <p role="alert" className="text-accent-800">
          {sheet.error.message}
        </p>
      </Card>
    );
  }

  const data = sheet.data;

  return (
    <>
      <PageHeading
        title="Today's availability"
        description={
          data.is_today
            ? `What you can sell today, ${data.date}. Customers only see produce declared for today.`
            : `Showing ${data.date}.`
        }
        actions={
          <button
            onClick={() => copyYesterday.mutate()}
            disabled={copyYesterday.isPending}
            className="rounded-card border border-surface-border bg-surface-raised px-4 py-2 text-sm font-medium text-primary-800 hover:bg-primary-50 disabled:opacity-60"
          >
            {copyYesterday.isPending ? "Copying…" : "Copy yesterday"}
          </button>
        }
      />

      {/* Counted over PRODUCTS, matching the cards below. The server's
          undeclared_count is per grade, which read as "5 products" to a grower
          who has five grades of one fruit. A product with only some of its
          grades declared is a separate, milder case: it IS on sale, just not
          in every crate the grower has. */}
      {(undeclared.length > 0 || partial.length > 0) && (
        <Card className="mb-4 border-accent-300 bg-accent-50">
          {undeclared.length > 0 && (
            <p className="text-sm font-medium text-accent-900">
              {undeclared.length} product{undeclared.length === 1 ? " is" : "s are"} not
              declared for today — {undeclared.length === 1 ? "it is" : "they are"} not on
              sale.
            </p>
          )}
          {partial.length > 0 && (
            <p className="mt-1 text-sm text-accent-900/80">
              {partial.length} more {partial.length === 1 ? "has a size" : "have sizes"} with
              no declaration.
            </p>
          )}
        </Card>
      )}

      {banner && (
        <Card className="mb-4 border-primary-300 bg-primary-50">
          <p className="text-sm text-primary-900">{banner}</p>
        </Card>
      )}
      {errorDetail && (
        <Card className="mb-4">
          <p role="alert" className="text-sm text-accent-800">
            {errorDetail}
          </p>
        </Card>
      )}

      <div className="grid gap-2">
        {groups.map((group) => (
          <ProductSheetCard
            key={group.productId}
            group={group}
            draft={draft}
            onChange={(sizeCodeId, value) =>
              setDraft((current) => ({ ...current, [sizeCodeId]: value }))
            }
            onClose={(availabilityId) => close.mutate(availabilityId)}
            closing={close.isPending}
            onReopen={(availabilityId) => reopen.mutate(availabilityId)}
            reopening={reopen.isPending}
          />
        ))}
      </div>

      {groups.length === 0 && (
        <Card>
          <p className="text-primary-900/70">
            You have no active products yet. Add produce before declaring availability.
          </p>
        </Card>
      )}

      {/* Sticky on mobile so Save is always in reach on a long list. */}
      {groups.length > 0 && (
        <div className="sticky bottom-0 z-20 mt-4 -mx-4 border-t border-surface-border bg-surface-raised/95 px-4 py-3 backdrop-blur sm:mx-0 sm:rounded-card sm:border">
          <div className="flex items-center justify-between gap-3">
            <p className="text-sm text-primary-900/70">
              {dirtyCount > 0
                ? `${dirtyCount} unsaved change${dirtyCount === 1 ? "" : "s"}`
                : "No unsaved changes"}
            </p>
            <button
              onClick={() => save.mutate()}
              disabled={save.isPending || dirtyCount === 0}
              className="rounded-card bg-primary-600 px-6 py-2.5 font-medium text-white hover:bg-primary-700 disabled:opacity-50"
            >
              {save.isPending ? "Saving…" : "Save all"}
            </button>
          </div>
        </div>
      )}
    </>
  );
}

/**
 * One product on the sheet: the produce at the top, a box per grade beneath.
 *
 * The grades are what actually carry a declaration, so each keeps its own
 * number box, its own stock line and its own close/reopen. What the card adds
 * is the context that was missing when they were separate rows — that these
 * three crates are one pomegranate.
 */
/**
 * One product on the sheet.
 *
 * A grower who does not grade sees what they always saw: the produce, a box,
 * Save. A grower with three grades of pomegranate sees one card with three
 * boxes in it — the declaration is still per grade, because M and XL are
 * separate crates with separate gram pools, but they no longer read as three
 * unrelated products distinguishable only by a size chip.
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
  const cover = group.rows.find((row) => row.image_url)?.image_url ?? null;

  // An ungraded product has exactly one implicit size code, and its box
  // belongs beside the name rather than on a row of its own under an empty
  // header — the same layout this screen has always had.
  const single = graded ? null : group.rows[0];
  const singleValue = single ? draft[single.size_code_id] ?? "" : "";

  return (
    <Card className={declaredCount === group.rows.length ? "" : "border-accent-200"}>
      <div className="flex flex-col gap-3 sm:flex-row sm:items-center">
        {cover ? (
          <img src={cover} alt="" className="h-14 w-14 shrink-0 rounded-card object-cover" />
        ) : (
          <div aria-hidden="true" className="h-14 w-14 shrink-0 rounded-card bg-surface-sunken" />
        )}

        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-2">
            <h2 className="font-semibold text-primary-900">{group.name}</h2>
            {group.grade && (
              <span className="rounded-full bg-surface-sunken px-2 py-0.5 text-xs text-primary-900/70">
                {group.grade}
              </span>
            )}
            {declaredCount === 0 && (
              <span className="rounded-full bg-accent-100 px-2 py-0.5 text-xs font-medium text-accent-900">
                Not declared
              </span>
            )}
            {single?.status === "closed" && single.declared && (
              <span className="rounded-full bg-surface-sunken px-2 py-0.5 text-xs text-primary-900/60">
                Closed
              </span>
            )}
            {/* Only worth saying on a graded product: "1 of 1 sizes declared"
                tells a grower who does not grade nothing at all. */}
            {graded && declaredCount > 0 && (
              <span
                className={`rounded-full px-2 py-0.5 text-xs font-medium ${
                  declaredCount === group.rows.length
                    ? "bg-primary-100 text-primary-800"
                    : "bg-accent-100 text-accent-900"
                }`}
              >
                {declaredCount} of {group.rows.length} sizes declared
              </span>
            )}
          </div>

          <p className="mt-0.5 text-xs capitalize text-primary-900/50">{group.type}</p>

          {single?.declared && <StockLine row={single} />}
        </div>

        {single && (
          <DeclarationControls
            row={single}
            sizeCodeNamed={false}
            productName={group.name}
            value={singleValue}
            onChange={(value) => onChange(single.size_code_id, value)}
            onClose={() => single.availability_id && onClose(single.availability_id)}
            closing={closing}
            onReopen={() => single.availability_id && onReopen(single.availability_id)}
            reopening={reopening}
          />
        )}
      </div>

      {single && <DeclarationErrors row={single} value={singleValue} />}

      {/* Divided rather than spaced: on a product with four grades the boxes
          need a line between them to read as four separate declarations. */}
      {graded && (
        <div className="mt-3 divide-y divide-surface-border border-t border-surface-border">
          {group.rows.map((row) => (
            <SizeCodeRow
              key={row.size_code_id}
              row={row}
              productName={group.name}
              value={draft[row.size_code_id] ?? ""}
              onChange={(value) => onChange(row.size_code_id, value)}
              onClose={() => row.availability_id && onClose(row.availability_id)}
              closing={closing}
              onReopen={() => row.availability_id && onReopen(row.availability_id)}
              reopening={reopening}
            />
          ))}
        </div>
      )}
    </Card>
  );
}

/** One grade's declaration: its crate, what is left of it, and its number box. */
function SizeCodeRow({
  row,
  productName,
  value,
  onChange,
  onClose,
  closing,
  onReopen,
  reopening,
}: {
  row: SheetRow;
  productName: string;
  value: string;
  onChange: (value: string) => void;
  onClose: () => void;
  closing: boolean;
  onReopen: () => void;
  reopening: boolean;
}): ReactElement {
  return (
    <div className="py-3">
      <div className="flex flex-col gap-3 sm:flex-row sm:items-center">
        {/* The grade's OWN photograph, small: the pictures are of the grade,
            and on a graded product they are the fastest way to tell the
            crates apart. */}
        {row.image_url ? (
          <img src={row.image_url} alt="" className="h-10 w-10 shrink-0 rounded-card object-cover" />
        ) : (
          <div aria-hidden="true" className="h-10 w-10 shrink-0 rounded-card bg-surface-sunken" />
        )}

        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-2">
            {/* The SIZE CODE, prominent: this row declares one grade's crate,
                and a grower looking at three of them needs to see at a glance
                which is which. */}
            <span className="rounded-full bg-primary-100 px-2 py-0.5 text-xs font-medium text-primary-800">
              {row.size_code}
              {row.size_meta ? ` · ${row.size_meta}` : ""}
            </span>
            {!row.declared && (
              <span className="rounded-full bg-accent-100 px-2 py-0.5 text-xs font-medium text-accent-900">
                Not declared
              </span>
            )}
            {row.status === "closed" && (
              <span className="rounded-full bg-surface-sunken px-2 py-0.5 text-xs text-primary-900/60">
                Closed
              </span>
            )}
          </div>

          {row.declared && <StockLine row={row} />}
        </div>

        <DeclarationControls
          row={row}
          sizeCodeNamed
          productName={productName}
          value={value}
          onChange={onChange}
          onClose={onClose}
          closing={closing}
          onReopen={onReopen}
          reopening={reopening}
        />
      </div>

      <DeclarationErrors row={row} value={value} />
    </div>
  );
}

/** What is left of one grade's crate, in the grower's own terms. */
function StockLine({ row }: { row: SheetRow }): ReactElement {
  const committed = row.reserved_grams + row.sold_grams;
  return (
    <p className="mt-0.5 text-xs text-primary-900/60">
      {/* Sellable, not remaining: a closed product has stock on the books but
          none a customer can buy, and saying "50 kg left" made closing look
          like it had not worked. The declaration is still shown alongside, so
          reopening holds no surprises. */}
      {row.status === "closed" ? (
        <>
          Not on sale · 0 kg sellable
          {row.total_grams > 0 && <> · {formatGrams(row.total_grams)} declared, held back</>}
        </>
      ) : (
        <>{formatGrams(row.sellable_grams)} left</>
      )}
      {committed > 0 && <> · {formatGrams(committed)} already ordered</>}
    </p>
  );
}

/** The number box for one grade, and the close/reopen beside it. */
function DeclarationControls({
  row,
  sizeCodeNamed,
  productName,
  value,
  onChange,
  onClose,
  closing,
  onReopen,
  reopening,
}: {
  row: SheetRow;
  /** Whether the product is graded, and so whether the size is worth naming. */
  sizeCodeNamed: boolean;
  productName: string;
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

  return (
    <div className="flex items-center gap-2">
      <div>
        <label htmlFor={`qty-${row.size_code_id}`} className="sr-only">
          {sizeCodeNamed
            ? `Kilograms available for ${productName}, size ${row.size_code}`
            : `Kilograms available for ${productName}`}
        </label>
        <div className="flex items-center gap-1">
          {/* inputMode=decimal brings up the number pad on a phone without the
              spinner and locale rounding a number input would add. */}
          <input
            id={`qty-${row.size_code_id}`}
            type="text"
            inputMode="decimal"
            value={value}
            onChange={(e) => onChange(e.target.value)}
            placeholder="0"
            aria-invalid={invalid || belowCommitted}
            className={`w-24 rounded-card border px-3 py-2.5 text-right text-lg tabular-nums ${
              invalid || belowCommitted
                ? "border-accent-500 bg-accent-50"
                : "border-surface-border bg-surface-raised"
            }`}
          />
          <span className="text-sm text-primary-900/60">kg</span>
        </div>
      </div>

      {row.declared && row.status === "closed" && (
        <button
          onClick={onReopen}
          disabled={reopening}
          title={
            sizeCodeNamed
              ? `Put size ${row.size_code} back on sale today`
              : "Put this back on sale today"
          }
          className="rounded-card border border-primary-300 px-3 py-2 text-sm font-medium text-primary-800 hover:bg-primary-50 disabled:opacity-50"
        >
          Reopen
        </button>
      )}

      {row.declared && row.status === "open" && (
        <button
          onClick={onClose}
          disabled={closing}
          title={
            sizeCodeNamed ? `Stop selling size ${row.size_code} today` : "Stop selling this today"
          }
          className="rounded-card border border-surface-border px-3 py-2 text-sm text-primary-900/70 hover:bg-surface-sunken disabled:opacity-50"
        >
          Close
        </button>
      )}
    </div>
  );
}

/** What is wrong with the number typed against one grade, if anything. */
function DeclarationErrors({ row, value }: { row: SheetRow; value: string }): ReactElement | null {
  const grams = kgToGrams(value);
  const invalid = value.trim() !== "" && grams === null;
  const committed = row.reserved_grams + row.sold_grams;
  const belowCommitted = grams !== null && grams < committed;

  if (invalid) {
    return (
      <p role="alert" className="mt-2 text-xs text-accent-800">
        Enter a number of kilograms, for example 12.5
      </p>
    );
  }
  if (belowCommitted) {
    return (
      <p role="alert" className="mt-2 text-xs text-accent-800">
        Customers have already ordered {formatGrams(committed)}. You cannot declare less than
        that.
      </p>
    );
  }
  return null;
}
