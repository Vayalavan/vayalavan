import { useEffect, useState, type ReactElement } from "react";
import { Link } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Card, PageHeading, ApiError } from "@vayal/ui-kit";
import { api } from "../lib/api.js";
import { formatGrams } from "../lib/money.js";
import {
  PRODUCT_STATUSES,
  PRODUCT_TYPES,
  type Product,
  type ProductPage,
} from "../lib/types.js";

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

export function ProductsPage(): ReactElement {
  const [search, setSearch] = useState("");
  const [type, setType] = useState("");
  const [status, setStatus] = useState("");
  const [page, setPage] = useState(0);

  const debouncedSearch = useDebounced(search, 300);

  // Any filter change invalidates the current page number.
  useEffect(() => setPage(0), [debouncedSearch, type, status]);

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
    // flashing an empty table.
    placeholderData: (previous) => previous,
  });

  const total = query.data?.total ?? 0;
  const pageCount = Math.max(1, Math.ceil(total / PAGE_SIZE));

  return (
    <>
      <PageHeading
        title="Your produce"
        description="Everything you list on Vayalavan."
        actions={
          <div className="flex flex-wrap gap-2">
            <Link
              to="/imports"
              className="rounded-card border border-surface-border bg-surface-raised px-4 py-2 text-sm font-medium text-primary-800 hover:bg-primary-50"
            >
              Import CSV
            </Link>
            <Link
              to="/products/new"
              className="rounded-card bg-primary-600 px-4 py-2 text-sm font-medium text-white hover:bg-primary-700"
            >
              Add product
            </Link>
          </div>
        }
      />

      <Card className="mb-4">
        {/* Stacks on mobile, one row from sm up. */}
        <div className="flex flex-col gap-3 sm:flex-row">
          <div className="flex-1">
            <label htmlFor="search" className="sr-only">
              Search products
            </label>
            <input
              id="search"
              type="search"
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              placeholder="Search by name…"
              className="w-full rounded-card border border-surface-border bg-surface-raised px-3 py-2"
            />
          </div>

          <div>
            <label htmlFor="type" className="sr-only">
              Filter by type
            </label>
            <select
              id="type"
              value={type}
              onChange={(e) => setType(e.target.value)}
              className="w-full rounded-card border border-surface-border bg-surface-raised px-3 py-2 sm:w-40"
            >
              <option value="">All types</option>
              {PRODUCT_TYPES.map((t) => (
                <option key={t} value={t}>
                  {t[0]!.toUpperCase() + t.slice(1)}
                </option>
              ))}
            </select>
          </div>

          <div>
            <label htmlFor="status" className="sr-only">
              Filter by status
            </label>
            <select
              id="status"
              value={status}
              onChange={(e) => setStatus(e.target.value)}
              className="w-full rounded-card border border-surface-border bg-surface-raised px-3 py-2 sm:w-40"
            >
              <option value="">All statuses</option>
              {PRODUCT_STATUSES.map((s) => (
                <option key={s} value={s}>
                  {s[0]!.toUpperCase() + s.slice(1)}
                </option>
              ))}
            </select>
          </div>
        </div>
      </Card>

      {query.isError && (
        <Card>
          <p role="alert" className="text-sm text-accent-800">
            {query.error.message}
          </p>
        </Card>
      )}

      {query.isPending && <Card>Loading your products…</Card>}

      {query.data && query.data.products.length === 0 && (
        <Card>
          <p className="text-primary-900/70">
            {search || type || status
              ? "No products match those filters."
              : "You have not added any produce yet."}
          </p>
          {!search && !type && !status && (
            <Link
              to="/products/new"
              className="mt-3 inline-block rounded-card bg-primary-600 px-4 py-2 text-sm font-medium text-white"
            >
              Add your first product
            </Link>
          )}
        </Card>
      )}

      {query.data && query.data.products.length > 0 && (
        <>
          <div className="grid gap-3">
            {query.data.products.map((product) => (
              <ProductRow key={product.id} product={product} />
            ))}
          </div>

          <div className="mt-6 flex items-center justify-between text-sm">
            <p className="text-primary-900/70">
              {total} product{total === 1 ? "" : "s"}
            </p>
            {pageCount > 1 && (
              <div className="flex items-center gap-2">
                <button
                  onClick={() => setPage((p) => Math.max(0, p - 1))}
                  disabled={page === 0}
                  className="rounded-card border border-surface-border px-3 py-1.5 disabled:opacity-40"
                >
                  Previous
                </button>
                <span className="text-primary-900/70">
                  {page + 1} / {pageCount}
                </span>
                <button
                  onClick={() => setPage((p) => Math.min(pageCount - 1, p + 1))}
                  disabled={page + 1 >= pageCount}
                  className="rounded-card border border-surface-border px-3 py-1.5 disabled:opacity-40"
                >
                  Next
                </button>
              </div>
            )}
          </div>
        </>
      )}
    </>
  );
}

const STATUS_STYLES: Record<string, string> = {
  active: "bg-primary-100 text-primary-800",
  draft: "bg-surface-sunken text-primary-900/70",
  archived: "bg-secondary-100 text-secondary-800",
};

function ProductRow({ product }: { product: Product }): ReactElement {
  const queryClient = useQueryClient();

  const archive = useMutation<Product, ApiError>({
    mutationFn: () => api.post<Product>(`/products/${product.id}/archive`),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["products"] });
    },
  });

  return (
    <Card>
      {/* Column on mobile, row from sm up. */}
      <div className="flex flex-col gap-4 sm:flex-row sm:items-start">
        {product.image_url ? (
          <img
            src={product.image_url}
            alt=""
            className="h-20 w-20 shrink-0 rounded-card object-cover"
            loading="lazy"
          />
        ) : (
          <div
            aria-hidden="true"
            className="flex h-20 w-20 shrink-0 items-center justify-center rounded-card bg-surface-sunken text-xs text-primary-900/40"
          >
            No image
          </div>
        )}

        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-2">
            <h2 className="font-semibold text-primary-900">{product.name}</h2>
            <span
              className={`rounded-full px-2 py-0.5 text-xs font-medium ${
                STATUS_STYLES[product.status] ?? ""
              }`}
            >
              {product.status}
            </span>
            {product.grade && (
              <span className="rounded-full bg-surface-sunken px-2 py-0.5 text-xs text-primary-900/70">
                Grade {product.grade}
              </span>
            )}
          </div>

          <p className="mt-0.5 text-sm capitalize text-primary-900/60">{product.type}</p>

          {/* Grouped by grade, because a flat list of packs would show "1 kg"
              twice at two prices with nothing to explain the difference. */}
          <div className="mt-2 space-y-1.5">
            {product.size_codes.map((sizeCode) => (
              <div key={sizeCode.id} className="text-sm text-primary-900/80">
                <span className="font-medium text-primary-900">{sizeCode.code}</span>
                {sizeCode.meta && (
                  <span className="text-primary-900/50"> · {sizeCode.meta}</span>
                )}
                <ul className="mt-0.5 flex flex-wrap gap-x-4 gap-y-1 pl-3">
                  {sizeCode.packs.map((pack) => (
                    <li key={pack.id}>
                      <span className="font-medium">{pack.label}</span>{" "}
                      <span className="text-primary-900/50">
                        ({formatGrams(pack.weight_grams)})
                      </span>{" "}
                      — {pack.price_display}
                    </li>
                  ))}
                </ul>
              </div>
            ))}
          </div>
        </div>

        <div className="flex shrink-0 gap-2">
          <Link
            to={`/products/${product.id}`}
            className="rounded-card border border-surface-border px-3 py-1.5 text-sm font-medium text-primary-800 hover:bg-primary-50"
          >
            Edit
          </Link>
          {product.status !== "archived" && (
            <button
              onClick={() => archive.mutate()}
              disabled={archive.isPending}
              className="rounded-card border border-surface-border px-3 py-1.5 text-sm text-primary-900/70 hover:bg-surface-sunken disabled:opacity-50"
            >
              {archive.isPending ? "Archiving…" : "Archive"}
            </button>
          )}
        </div>
      </div>

      {archive.isError && (
        <p role="alert" className="mt-2 text-sm text-accent-800">
          {archive.error.message}
        </p>
      )}
    </Card>
  );
}
