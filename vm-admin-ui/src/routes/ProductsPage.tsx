/**
 * Every product on the platform, from every grower, and what we add to it.
 *
 * The markup is the reason this screen exists. A supplier sets the price of
 * their produce; we add a flat amount per pack on top, and THAT is what a
 * customer pays. Both numbers belong on one row — a markup shown without the
 * price it sits on is impossible to sanity-check, and a customer price shown
 * without its parts is impossible to explain.
 *
 * What the grower sees is unchanged by anything on this page: the markup is
 * absent from every supplier response, their sales screen reports their own
 * prices, and their payout is computed from the markup snapshotted on each
 * order rather than from whatever is set here today. Repricing tomorrow cannot
 * restate what someone was owed yesterday.
 */
import { useEffect, useState, type FormEvent, type ReactElement } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Card, PageHeading, useToast, ApiError } from "@vayal/ui-kit";
import { api } from "../lib/api.js";

interface AdminUnit {
  id: string;
  /**
   * The GRADE this pack belongs to. Packs are flattened across grades on this
   * screen — an admin checking margins wants one row per sellable pack, not a
   * tree to expand — so each row names its own.
   */
  size_code_id: string;
  size_code: string;
  size_meta: string | null;
  label: string;
  /** THIS pack's own detail — "6-8 fruit" — as distinct from the grade's. */
  meta: string | null;
  weight_grams: number;
  is_active: boolean;
  supplier_price_paise: number;
  supplier_price_display: string;
  /** What the product's RATE came to on this pack — different per pack size. */
  markup_paise: number;
  markup_display: string;
  customer_price_paise: number;
  customer_price_display: string;
}

interface AdminProduct {
  id: string;
  supplier_id: string;
  supplier_name: string;
  name: string;
  type: string;
  grade: string | null;
  description: string | null;
  /** The COVER — the gallery's first image. Null when there is none. */
  image_url: string | null;
  /** How many images and videos the listing carries across every grade. */
  media_count: number;
  /** How many grades the listing has. */
  size_code_count: number;
  status: "draft" | "active" | "archived";
  /** The rate, in basis points: 3000 is 30%. */
  markup_bps: number;
  /** "30%" for reading, "30" for the edit box. */
  markup_display: string;
  markup_percent: string;
  units: AdminUnit[];
  created_at: string;
  updated_at: string;
}

interface ProductPage {
  products: AdminProduct[];
  total: number;
}

interface SupplierOption {
  id: string;
  business_name: string;
}

const PAGE_SIZE = 20;

const STATUSES: ReadonlyArray<readonly [string, string]> = [
  ["", "All statuses"],
  ["active", "Active"],
  ["draft", "Draft"],
  ["archived", "Archived"],
];

const TYPES: ReadonlyArray<readonly [string, string]> = [
  ["", "All produce"],
  ["fruit", "Fruit"],
  ["vegetable", "Vegetable"],
  ["microgreen", "Microgreen"],
  ["other", "Other"],
];

export function ProductsPage(): ReactElement {
  const [page, setPage] = useState(0);
  const [status, setStatus] = useState("");
  const [type, setType] = useState("");
  const [supplierId, setSupplierId] = useState("");
  const [search, setSearch] = useState("");

  const query = {
    limit: PAGE_SIZE,
    offset: page * PAGE_SIZE,
    ...(status ? { status } : {}),
    ...(type ? { type } : {}),
    ...(supplierId ? { supplier_id: supplierId } : {}),
    ...(search.trim() ? { search: search.trim() } : {}),
  };

  const products = useQuery<ProductPage, ApiError>({
    queryKey: ["admin-products", query],
    queryFn: () => api.get<ProductPage>("/admin/products", { query }),
    placeholderData: (previous) => previous,
  });

  // The supplier filter's options. Approved growers only — a pending
  // application has no produce to filter by.
  const suppliers = useQuery<{ suppliers: SupplierOption[] }, ApiError>({
    queryKey: ["admin-supplier-options"],
    queryFn: () =>
      api.get<{ suppliers: SupplierOption[] }>("/admin/suppliers", {
        query: { status: "approved", limit: 100 },
      }),
    staleTime: 5 * 60_000,
  });

  function resetPage<T>(setter: (value: T) => void) {
    return (value: T) => {
      setter(value);
      // A new filter is a different set of rows; staying on page 4 of the old
      // one lands on an empty table.
      setPage(0);
    };
  }

  const total = products.data?.total ?? 0;
  const pages = Math.max(1, Math.ceil(total / PAGE_SIZE));

  return (
    <>
      <PageHeading
        title="Products"
        description="Everything our growers list, and what we add on top. The markup is a percentage of the grower’s price, so it scales with pack size. It is ours — suppliers never see it, and are never paid or charged commission on it."
      />

      <Card className="mb-4">
        <div className="flex flex-wrap items-end gap-3">
          <label className="flex flex-col gap-1 text-sm">
            <span className="text-primary-900/70">Search</span>
            <input
              type="search"
              value={search}
              onChange={(e) => resetPage(setSearch)(e.target.value)}
              placeholder="Produce name"
              className="w-48 rounded-card border border-surface-border bg-surface-raised px-3 py-2 text-sm"
            />
          </label>

          <label className="flex flex-col gap-1 text-sm">
            <span className="text-primary-900/70">Supplier</span>
            <select
              value={supplierId}
              onChange={(e) => resetPage(setSupplierId)(e.target.value)}
              className="w-56 rounded-card border border-surface-border bg-surface-raised px-3 py-2 text-sm"
            >
              <option value="">All suppliers</option>
              {(suppliers.data?.suppliers ?? []).map((s) => (
                <option key={s.id} value={s.id}>
                  {s.business_name}
                </option>
              ))}
            </select>
          </label>

          <label className="flex flex-col gap-1 text-sm">
            <span className="text-primary-900/70">Type</span>
            <select
              value={type}
              onChange={(e) => resetPage(setType)(e.target.value)}
              className="rounded-card border border-surface-border bg-surface-raised px-3 py-2 text-sm"
            >
              {TYPES.map(([value, label]) => (
                <option key={value} value={value}>
                  {label}
                </option>
              ))}
            </select>
          </label>

          <label className="flex flex-col gap-1 text-sm">
            <span className="text-primary-900/70">Status</span>
            <select
              value={status}
              onChange={(e) => resetPage(setStatus)(e.target.value)}
              className="rounded-card border border-surface-border bg-surface-raised px-3 py-2 text-sm"
            >
              {STATUSES.map(([value, label]) => (
                <option key={value} value={value}>
                  {label}
                </option>
              ))}
            </select>
          </label>

          <p className="ml-auto text-sm text-primary-900/60">
            {total} product{total === 1 ? "" : "s"}
          </p>
        </div>
      </Card>

      {products.isPending && <Card>Loading products…</Card>}

      {products.isError && (
        <Card>
          <p role="alert" className="text-sm text-accent-800">
            {products.error.message}
          </p>
        </Card>
      )}

      {products.data && products.data.products.length === 0 && (
        <Card>
          <p className="text-primary-900/70">
            No products match these filters.
          </p>
        </Card>
      )}

      <div className="grid gap-3">
        {(products.data?.products ?? []).map((product) => (
          <ProductRow key={product.id} product={product} />
        ))}
      </div>

      {pages > 1 && (
        <div className="mt-4 flex items-center justify-between">
          <button
            type="button"
            disabled={page === 0}
            onClick={() => setPage((p) => Math.max(0, p - 1))}
            className="rounded-card border border-surface-border px-3 py-1.5 text-sm font-medium text-primary-800 disabled:opacity-40"
          >
            Previous
          </button>
          <span className="text-sm text-primary-900/60">
            Page {page + 1} of {pages}
          </span>
          <button
            type="button"
            disabled={page + 1 >= pages}
            onClick={() => setPage((p) => p + 1)}
            className="rounded-card border border-surface-border px-3 py-1.5 text-sm font-medium text-primary-800 disabled:opacity-40"
          >
            Next
          </button>
        </div>
      )}
    </>
  );
}

/**
 * One product: who grows it, what they charge, what we add, what it sells for.
 */
function ProductRow({ product }: { product: AdminProduct }): ReactElement {
  const queryClient = useQueryClient();
  const { showToast } = useToast();
  const [markup, setMarkup] = useState(product.markup_percent);

  // A refetch brings the server's value back; the box should follow it unless
  // the admin is mid-edit, which `dirty` below decides.
  useEffect(() => {
    setMarkup(product.markup_percent);
  }, [product.markup_percent]);

  const save = useMutation<AdminProduct, ApiError, number>({
    mutationFn: (bps) =>
      api.put<AdminProduct>(`/admin/products/${product.id}/markup`, {
        markup_bps: bps,
      }),
    onSuccess: (updated) => {
      showToast(
        updated.markup_bps === 0
          ? `Markup removed from ${updated.name}.`
          : `${updated.name} now carries a ${updated.markup_display} markup.`,
      );
      void queryClient.invalidateQueries({ queryKey: ["admin-products"] });
      // Today's storefront prices moved, so anything quoting them is stale.
      void queryClient.invalidateQueries({ queryKey: ["admin-dashboard"] });
    },
  });

  const trimmed = markup.trim();
  // Rounded, because 2.5% is 250 basis points exactly but a third of a percent
  // is not an integer, and a fractional basis point is not a thing. Same
  // conversion as the supplier commission field.
  const bps = trimmed === "" ? Number.NaN : Math.round(Number(trimmed) * 100);
  const valid = Number.isFinite(bps) && bps >= 0 && bps <= 10000;
  const dirty = valid && bps !== product.markup_bps;

  function submit(event: FormEvent): void {
    event.preventDefault();
    if (!dirty) return;
    save.mutate(bps);
  }

  return (
    <Card>
      <div className="flex flex-col gap-4 lg:flex-row lg:items-start lg:justify-between">
        <div className="flex min-w-0 gap-3">
          <div className="relative shrink-0">
            {product.image_url ? (
              <img
                src={product.image_url}
                alt=""
                loading="lazy"
                className="h-16 w-16 rounded-card border border-surface-border object-contain p-1"
              />
            ) : (
              <div className="flex h-16 w-16 items-center justify-center rounded-card border border-surface-border bg-surface-sunken text-center text-xs text-primary-900/40">
                {product.media_count > 0 ? "Video only" : "No photo"}
              </div>
            )}
            {/* One thumbnail and a count, never the whole gallery — this is a
                table, and the admin only needs to know there is more. */}
            {product.media_count > 1 && (
              <span className="absolute -bottom-1 -right-1 rounded-badge border border-surface-border bg-surface px-1 text-[10px] font-medium text-primary-900/70">
                +{product.media_count - 1}
              </span>
            )}
          </div>

          <div className="min-w-0">
            <div className="flex flex-wrap items-center gap-2">
              <h2 className="font-semibold text-primary-900">{product.name}</h2>
              {product.grade && (
                <span className="rounded-full bg-surface-sunken px-2 py-0.5 text-xs text-primary-900/70">
                  {product.grade}
                </span>
              )}
              <StatusBadge status={product.status} />
            </div>
            <p className="mt-0.5 text-sm text-primary-900/70">
              {product.supplier_name || "Unknown supplier"}
              <span className="text-primary-900/40"> · </span>
              <span className="capitalize">{product.type}</span>
            </p>
            {product.description && (
              <p className="mt-1 line-clamp-2 max-w-prose text-sm text-primary-900/60">
                {product.description}
              </p>
            )}
          </div>
        </div>

        {/* The markup box sits beside the packs it moves, so the effect of a
            change is visible in the same glance as the change itself. */}
        <form onSubmit={submit} className="flex shrink-0 items-end gap-2">
          <label className="flex flex-col gap-1 text-sm">
            <span className="text-primary-900/70">Markup</span>
            <div className="flex items-center gap-1">
              <input
                type="text"
                inputMode="decimal"
                value={markup}
                onChange={(e) => setMarkup(e.target.value)}
                aria-label={`Markup for ${product.name}, as a percentage of the supplier price`}
                aria-invalid={trimmed !== "" && !valid}
                className={`w-20 rounded-card border bg-surface-raised px-3 py-2 text-sm tabular-nums ${
                  trimmed !== "" && !valid ? "border-accent-500" : "border-surface-border"
                }`}
              />
              <span className="text-primary-900/60">%</span>
            </div>
          </label>
          <button
            type="submit"
            disabled={!dirty || save.isPending}
            className="rounded-card bg-primary-600 px-3 py-2 text-sm font-medium text-white disabled:cursor-not-allowed disabled:bg-surface-sunken disabled:text-primary-900/40"
          >
            {save.isPending ? "Saving…" : "Save"}
          </button>
        </form>
      </div>

      {trimmed !== "" && !valid && (
        <p role="alert" className="mt-2 text-sm text-accent-800">
          Enter a percentage between 0 and 100.
        </p>
      )}

      {save.isError && (
        <p role="alert" className="mt-2 text-sm text-accent-800">
          {save.error.message}
        </p>
      )}

      {product.units.length === 0 ? (
        <p className="mt-3 text-sm text-primary-900/50">
          No pack sizes yet — nothing to sell until this grower adds one.
        </p>
      ) : (
        <div className="mt-3 overflow-x-auto">
          <table className="w-full min-w-[30rem] text-left text-sm">
            <thead className="border-b border-surface-border text-xs uppercase tracking-wide text-primary-900/60">
              <tr>
                <th className="py-2 pr-3">Pack</th>
                <th className="py-2 pr-3 text-right">Supplier price</th>
                <th className="py-2 pr-3 text-right">
                  Markup{product.markup_bps > 0 ? ` (${product.markup_display})` : ""}
                </th>
                <th className="py-2 text-right">Customer pays</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-surface-border">
              {product.units.map((unit) => (
                <tr key={unit.id} className={unit.is_active ? "" : "opacity-50"}>
                  <td className="py-2 pr-3 text-primary-900">
                    {/* The grade first: the same label appears once per grade
                        at different prices, and without this the two rows read
                        as a duplicate. */}
                    {product.size_code_count > 1 && (
                      <span className="mr-2 rounded-full bg-surface-sunken px-2 py-0.5 text-xs font-medium text-primary-800">
                        {unit.size_code}
                      </span>
                    )}
                    {unit.label}
                    {!unit.is_active && (
                      <span className="ml-2 text-xs text-primary-900/50">inactive</span>
                    )}
                    {/* The grower's own detail for this pack, as the customer
                        reads it on the product page. */}
                    {unit.meta && (
                      <span className="block text-xs text-primary-900/55">{unit.meta}</span>
                    )}
                  </td>
                  <td className="py-2 pr-3 text-right tabular-nums text-primary-900/70">
                    {unit.supplier_price_display}
                  </td>
                  <td className="py-2 pr-3 text-right tabular-nums text-primary-900/70">
                    {unit.markup_paise > 0 ? `+ ${unit.markup_display}` : "—"}
                  </td>
                  <td className="py-2 text-right font-medium tabular-nums text-primary-900">
                    {unit.customer_price_display}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </Card>
  );
}

function StatusBadge({ status }: { status: string }): ReactElement {
  const tone =
    status === "active"
      ? "bg-primary-50 text-primary-800"
      : status === "draft"
        ? "bg-surface-sunken text-primary-900/70"
        : "bg-accent-50 text-accent-800";
  return (
    <span className={`rounded-full px-2 py-0.5 text-xs font-medium capitalize ${tone}`}>
      {status}
    </span>
  );
}
