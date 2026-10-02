/**
 * One product, on its own page.
 *
 * What a card cannot do: the full description rather than two clamped lines, a
 * large image, who grew it, and every pack size with the grower's own detail
 * for it. It also gives produce a shareable URL — until now there was no way
 * to send someone a link to a single item.
 */
import { useEffect, useRef, useState, type ReactElement } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import {
  Card,
  Skeleton,
  harvestTier,
  harvestTierLabel,
  harvestShareSentence,
  type HarvestTier,
  type ApiError,
} from "@vayal/ui-kit";
import { ProductGallery, type GalleryMedia } from "../components/ProductGallery.js";
import { api } from "../lib/api.js";
import { useAddToCart, useCart, useRemoveCartItem, useUpdateCartQty } from "../lib/cart.js";
import { useAuth } from "../lib/auth.js";

interface Unit {
  id: string;
  label: string;
  /**
   * The grower's own detail for this pack — "6-8 fruit", "ventilated carton".
   * The label is a chip; this is the sentence that would not fit in it.
   */
  meta: string | null;
  weight_grams: number;
  price_display: string;
  purchasable: boolean;
}

/**
 * One SIZE CODE — a grade of the produce, with its OWN gallery, packs and
 * stock. Choosing a different grade swaps the pictures and the prices.
 */
interface SizeCode {
  id: string;
  code: string;
  meta: string | null;
  /**
   * Roughly what percent of this product's harvest comes off as this grade.
   * Null when the grower has not estimated their split — which draws no
   * rarity treatment at all, rather than guessing one.
   */
  harvest_share_pct: number | null;
  media: GalleryMedia[];
  image_url: string | null;
  stock_hint: string;
  any_unit_purchasable: boolean;
  units: Unit[];
}

/**
 * How each rarity tier is drawn.
 *
 * Classes, not values: the metals are theme tokens in vm-ui-kit's preset
 * (CLAUDE.md §9), so a palette change is a change to that file and not to this
 * one. Only the UNSELECTED state is here — a selected chip keeps the brand
 * green, because which grade you are buying matters more than how rare it is.
 */
const METAL: Record<HarvestTier, { border: string; body: string }> = {
  // Two parts per tier: the cell's edge and its tinted body. There used to be
  // a third — a filled band across the foot carrying the share — and removing
  // it leaves the EDGE doing the work the band did. That is why the border is
  // 2px at the 500 step rather than a hairline: against an off-white page a
  // 50-step tint alone is close to invisible, and the tier has to survive
  // being read at arm's length.
  rare: {
    border: "border-gold-500",
    body: "bg-gold-50",
  },
  uncommon: {
    border: "border-bronze-500",
    body: "bg-bronze-50",
  },
  common: {
    border: "border-silver-500",
    body: "bg-silver-50",
  },
};

interface ProductDetail {
  product: {
    id: string;
    name: string;
    type: string;
    grade: string | null;
    description: string | null;
    image_url: string | null;
    size_code_id: string;
    size_code: string;
    size_meta: string | null;
    size_code_count: number;
    stock_hint: string;
    any_unit_purchasable: boolean;
    supplier_name: string;
    available_today: boolean;
    date: string;
    size_codes: SizeCode[];
  };
  units: Unit[];
}

export function ProductPage(): ReactElement {
  const { id = "" } = useParams();
  const navigate = useNavigate();
  const { user } = useAuth();
  const { cart } = useCart();

  const addToCart = useAddToCart();
  const updateQty = useUpdateCartQty();
  const removeItem = useRemoveCartItem();

  const detail = useQuery<ProductDetail, ApiError>({
    queryKey: ["product", id],
    queryFn: () => api.get<ProductDetail>(`/catalog/${id}`),
    // Stock moves as other customers buy.
    staleTime: 20_000,
  });

  const [selectedSizeId, setSelectedSizeId] = useState("");
  const [selectedId, setSelectedId] = useState("");
  const [draftQty, setDraftQty] = useState<number | null>(null);
  const qtyTimer = useRef<ReturnType<typeof setTimeout> | null>(null);

  // Open on the grade the server chose — the first with something buyable.
  useEffect(() => {
    if (!detail.data || selectedSizeId) return;
    setSelectedSizeId(
      detail.data.product.size_code_id || (detail.data.product.size_codes[0]?.id ?? ""),
    );
  }, [detail.data, selectedSizeId]);

  const sizeCodes = detail.data?.product.size_codes ?? [];
  const activeSize =
    sizeCodes.find((sc) => sc.id === selectedSizeId) ?? sizeCodes[0];

  // The chosen grade's share, once — both the sentence and the guard below
  // need the tier, and deriving it twice invites the two disagreeing.
  const activeTier = harvestTier(activeSize?.harvest_share_pct);
  const selectedShare =
    activeTier !== null && activeSize?.harvest_share_pct != null
      ? { pct: activeSize.harvest_share_pct, tier: activeTier }
      : null;

  // Preselect the first pack of the ACTIVE grade that can actually be bought.
  // Re-runs when the grade changes, because the packs change with it.
  useEffect(() => {
    if (!activeSize) return;
    const stillValid = activeSize.units.some((u) => u.id === selectedId);
    if (stillValid) return;
    const first = activeSize.units.find((u) => u.purchasable) ?? activeSize.units[0];
    setSelectedId(first?.id ?? "");
    setDraftQty(null);
  }, [activeSize, selectedId]);

  useEffect(
    () => () => {
      if (qtyTimer.current) clearTimeout(qtyTimer.current);
    },
    [],
  );

  const line = cart.data?.items.find((item) => item.product_unit_id === selectedId);

  useEffect(() => {
    if (line && draftQty !== null && line.qty === draftQty) setDraftQty(null);
  }, [line, draftQty]);

  const shownQty = draftQty ?? line?.qty ?? 0;

  // Same debounce as the catalogue and the drawer: one request per burst of
  // taps, not one per tap.
  function changeQty(next: number): void {
    if (!line) return;
    if (next < 1) {
      setDraftQty(null);
      removeItem.mutate(line.id);
      return;
    }
    setDraftQty(next);
    if (qtyTimer.current) clearTimeout(qtyTimer.current);
    qtyTimer.current = setTimeout(() => {
      updateQty.mutate({ id: line.id, qty: next });
    }, 450);
  }

  if (detail.isPending) {
    return (
      <div className="grid gap-8 lg:grid-cols-2 lg:gap-12">
        <Skeleton className="aspect-square w-full rounded-3xl" />
        <div className="space-y-4">
          <Skeleton className="h-5 w-1/4" />
          <Skeleton className="h-10 w-2/3" />
          <Skeleton className="h-4 w-1/3" />
          <Skeleton className="h-12 w-1/3" />
          <Skeleton className="h-40 w-full" />
        </div>
      </div>
    );
  }

  if (detail.isError) {
    return (
      <Card>
        <h1 className="font-semibold text-primary-900">
          {detail.error.status === 404 ? "Produce not found" : "Something went wrong"}
        </h1>
        <p className="mt-1 text-sm text-primary-900/70">
          {detail.error.status === 404
            ? "This produce may have been removed, or the link is wrong."
            : detail.error.message}
        </p>
        <Link
          to="/"
          className="mt-4 inline-block rounded-card bg-primary-600 px-4 py-2 text-sm font-medium text-white"
        >
          Browse today&rsquo;s produce
        </Link>
      </Card>
    );
  }

  const { product } = detail.data;
  // Everything below is scoped to the grade in hand: its packs, its gallery,
  // its stock hint. Switching grade is a local move — the whole tree came down
  // in one response, so the pictures swap with no round trip.
  const units = activeSize?.units ?? [];
  const selected = units.find((u) => u.id === selectedId);
  const soldOut = !product.any_unit_purchasable;

  function handleAdd(): void {
    if (!selected) return;
    if (!user) {
      navigate("/account");
      return;
    }
    addToCart.mutate({ product_unit_id: selected.id, qty: 1 });
  }

  // The buy control, built once and placed twice: inline under the packs on a
  // wide screen, and in a bar pinned to the bottom on a phone, where the packs
  // push it below the fold and a customer should not have to scroll back to
  // buy what they just chose.
  const buyControl =
    shownQty > 0 ? (
      <div className="flex flex-1 items-center gap-3">
        <div className="flex items-center gap-1 rounded-full border-[1.5px] border-primary-600 bg-surface-raised p-0.5">
          <button
            type="button"
            aria-label={shownQty === 1 ? "Remove from cart" : "Decrease quantity"}
            onClick={() => changeQty(shownQty - 1)}
            className="h-10 w-10 rounded-full text-lg text-primary-800 transition hover:bg-primary-50"
          >
            {shownQty === 1 ? "×" : "−"}
          </button>
          <span
            aria-live="polite"
            aria-label={`${shownQty} in cart`}
            className="w-8 text-center text-base font-semibold tabular-nums text-primary-900"
          >
            {shownQty}
          </span>
          <button
            type="button"
            aria-label="Increase quantity"
            disabled={!selected?.purchasable}
            onClick={() => changeQty(shownQty + 1)}
            className="h-10 w-10 rounded-full text-lg text-primary-800 transition hover:bg-primary-50 disabled:opacity-40"
          >
            +
          </button>
        </div>
        <p className="flex items-center gap-1.5 text-sm font-medium text-primary-700">
          <CheckIcon className="h-4 w-4" />
          In your cart
        </p>
      </div>
    ) : (
      <button
        type="button"
        onClick={handleAdd}
        disabled={!selected?.purchasable || addToCart.isPending}
        className="flex h-12 flex-1 items-center justify-center gap-2 rounded-full bg-primary-600 px-6 text-[0.9375rem] font-semibold text-white shadow-[0_8px_24px_-8px] shadow-primary-600/60 transition hover:bg-primary-700 active:scale-[0.99] disabled:cursor-not-allowed disabled:bg-surface-sunken disabled:text-primary-900/40 disabled:shadow-none"
      >
        {!soldOut && <BagIcon />}
        {soldOut ? "Sold out today" : addToCart.isPending ? "Adding…" : "Add to cart"}
      </button>
    );

  return (
    // Room at the foot for the pinned bar on a phone, so it never sits on top
    // of the last thing on the page.
    <div className="pb-28 lg:pb-0">
      <nav aria-label="Breadcrumb" className="mb-4 flex items-center gap-1.5 text-sm">
        <Link
          to="/"
          className="text-primary-900/60 transition hover:text-primary-700"
        >
          Today&rsquo;s produce
        </Link>
        <ChevronIcon />
        <span className="truncate font-medium text-primary-900">{product.name}</span>
      </nav>

      <div className="grid items-start gap-8 lg:grid-cols-[minmax(0,26rem)_minmax(0,1fr)] lg:gap-10 xl:grid-cols-[minmax(0,30rem)_minmax(0,1fr)] xl:gap-12">
        {/* Sticky on a wide screen: the right column is long, and the
            photograph is what the customer is choosing against. */}
        <div className="lg:sticky lg:top-24">
          {/* Keyed on the grade so switching size resets the gallery to that
              grade's first picture rather than leaving it on an index into the
              previous one. */}
          <ProductGallery
            key={activeSize?.id ?? product.id}
            media={activeSize?.media ?? []}
            coverUrl={activeSize?.image_url ?? product.image_url}
            name={
              activeSize && sizeCodes.length > 1
                ? `${product.name}, size ${activeSize.code}`
                : product.name
            }
            dimmed={!(activeSize?.any_unit_purchasable ?? product.any_unit_purchasable)}
            badge={
              activeSize?.stock_hint && activeSize.any_unit_purchasable
                ? activeSize.stock_hint
                : ""
            }
          />
        </div>

        <div>
          <div className="flex flex-wrap items-center gap-2">
            {product.available_today && !soldOut ? (
              <span className="inline-flex items-center gap-1.5 rounded-full bg-primary-50 px-2.5 py-0.5 text-[11px] font-semibold text-primary-700 ring-1 ring-primary-200">
                <span className="relative flex h-2 w-2">
                  <span className="absolute inline-flex h-full w-full animate-ping rounded-full bg-primary-400 opacity-60 motion-reduce:animate-none" />
                  <span className="relative inline-flex h-2 w-2 rounded-full bg-primary-500" />
                </span>
                Fresh today
              </span>
            ) : (
              <span className="inline-flex items-center rounded-full bg-surface-sunken px-2.5 py-0.5 text-[11px] font-semibold text-primary-900/60">
                Not on sale today
              </span>
            )}
            <span className="rounded-full bg-secondary-50 px-2.5 py-0.5 text-[11px] font-semibold capitalize text-secondary-700 ring-1 ring-secondary-200">
              {product.type}
            </span>
            {product.grade && (
              <span className="rounded-full bg-gold-50 px-2.5 py-0.5 text-[11px] font-semibold text-gold-700 ring-1 ring-gold-200">
                Grade {product.grade}
              </span>
            )}
          </div>

          <h1 className="mt-2.5 text-[1.75rem] font-semibold leading-tight tracking-tight text-primary-900 sm:text-3xl">
            {product.name}
          </h1>

          {/* Who grew it. Named because a marketplace that hides its growers
              is just a shop, and the trading name is the only supplier detail
              that belongs on a public page. */}
          {product.supplier_name && (
            <div className="mt-3 flex items-center gap-2.5">
              <span
                aria-hidden="true"
                className="flex h-7 w-7 items-center justify-center rounded-full bg-gradient-to-br from-primary-500 to-primary-700 text-xs font-semibold text-white"
              >
                {product.supplier_name.trim().charAt(0).toUpperCase()}
              </span>
              <p className="text-sm text-primary-900/60">
                Grown by{" "}
                <span className="font-semibold text-primary-900">{product.supplier_name}</span>
              </p>
            </div>
          )}

          {/* The price of what is chosen, up top where the eye lands — it used
              to exist only inside the pack rows, so a customer had to find
              their own selection to learn what it cost. */}
          {selected && (
            <div className="mt-4 flex flex-wrap items-baseline gap-x-2.5 gap-y-1">
              <p className="text-2xl font-bold tabular-nums tracking-tight text-primary-900 sm:text-[1.75rem]">
                {selected.price_display}
              </p>
              <p className="text-sm text-primary-900/55">
                for {selected.label}
                {activeSize && sizeCodes.length > 1 ? ` · size ${activeSize.code}` : ""}
              </p>
            </div>
          )}

          {!product.available_today && (
            <div className="mt-5 rounded-2xl border border-secondary-200 bg-secondary-50 p-4">
              <p className="text-sm font-semibold text-secondary-900">
                Not on sale today
              </p>
              <p className="mt-1 text-sm text-secondary-900/80">
                This grower hasn&rsquo;t listed it for today, or has sold out.
                Produce is listed fresh each morning — check back tomorrow.
              </p>
            </div>
          )}

          <div className="mt-5 space-y-5 rounded-3xl border border-cream-300/70 bg-surface-raised p-4 shadow-card sm:p-5">
            {/* The size selector. Hidden when there is only one grade — a
                grower who does not grade should not be shown a choice of one. */}
            {sizeCodes.length > 1 && (
              <fieldset>
                <legend className="mb-3 flex w-full items-center justify-between text-sm font-semibold text-primary-900">
                  Choose a size
                  {activeSize && (
                    <span className="font-normal text-primary-900/50">
                      Selected: <span className="font-semibold text-primary-900">{activeSize.code}</span>
                    </span>
                  )}
                </legend>
                {/* A GRID, not a row of chips. Grades are a matrix — four, six,
                    eleven of them — and a wrapping row leaves ragged rows whose
                    cells are all different widths, which is exactly the layout
                    that makes a colour difference hard to compare. Equal cells
                    put the metals side by side in columns.

                    TWO columns at every width, so the same product is the same
                    shape on a laptop and a phone and the comparison this grid
                    exists for is between cells in a COLUMN.

                    The cells are RECTANGLES: the code and its meta on the left,
                    the share on the right, one line of each. Squares stacked
                    four lines of text into a 93px box; a wide cell reads left
                    to right like a label, and at the full column width it has
                    room for a meta line without wrapping. */}
                <div className="grid grid-cols-2 gap-2.5">
                  {sizeCodes.map((sizeCode) => {
                    const isSelected = sizeCode.id === selectedSizeId;
                    const sizeSoldOut = !sizeCode.any_unit_purchasable;
                    const tier = harvestTier(sizeCode.harvest_share_pct);
                    const metal = tier === null || sizeSoldOut ? null : METAL[tier];
                    return (
                      <label
                        key={sizeCode.id}
                        // The BORDER carries the state: metal when idle, green
                        // with a ring when chosen. The tier does not vanish
                        // with selection — the body keeps its metal tint and
                        // the share is printed in the cell.
                        className={`relative flex min-h-[3.5rem] cursor-pointer rounded-xl border-[1.5px] text-sm transition-all ${
                          sizeSoldOut
                            ? "border-surface-border bg-surface-sunken text-primary-900/40"
                            : isSelected
                              ? "border-primary-600 shadow-card ring-2 ring-primary-600/20"
                              : `${metal?.border ?? "border-surface-border"} hover:-translate-y-0.5 hover:shadow-card`
                        }`}
                      >
                        <input
                          type="radio"
                          name="size_code"
                          value={sizeCode.id}
                          checked={isSelected}
                          onChange={() => setSelectedSizeId(sizeCode.id)}
                          className="peer sr-only"
                        />

                        <span
                          className={`relative flex w-full flex-col justify-center overflow-hidden rounded-[0.65rem] px-3 py-2 peer-focus-visible:outline peer-focus-visible:outline-2 peer-focus-visible:outline-offset-2 peer-focus-visible:outline-primary-600 ${
                            sizeSoldOut ? "" : (metal?.body ?? "bg-surface-raised")
                          }`}
                        >
                          {/* The sweep, on the rarest grade only. A shimmer on
                              every cell is a shimmer on none. motion-safe, so
                              a reduced-motion setting leaves it still — and it
                              is decoration, so nothing here is announced. */}
                          {tier === "rare" && !sizeSoldOut && (
                            <span
                              aria-hidden="true"
                              className="pointer-events-none absolute inset-y-0 -left-1/2 w-1/2 -skew-x-12 bg-gradient-to-r from-transparent via-white/80 to-transparent motion-safe:animate-[shimmer_2.8s_ease-in-out_infinite]"
                            />
                          )}

                          {/* Two rows, each split left and right: the code
                              against the share, the grower's size range against
                              what the share is OF. The meta gets the whole
                              width of its row rather than whatever a stacked
                              share column left over — at 360px a column took
                              half the cell and cut "300 g - 350 g" in two. */}
                          <span className="relative flex items-center justify-between gap-2">
                            <span className="flex min-w-0 items-center gap-1.5">
                              <span className="truncate text-[0.9375rem] font-bold leading-none text-primary-900">
                                {sizeCode.code}
                              </span>
                              {tier === "rare" && !sizeSoldOut && (
                                <span className="shrink-0 rounded-full bg-gold-500 px-1.5 py-0.5 text-[9px] font-bold uppercase leading-none tracking-wider text-white">
                                  {harvestTierLabel(tier)}
                                </span>
                              )}
                            </span>
                            {tier !== null && !sizeSoldOut && (
                              <span className="shrink-0 text-sm font-bold leading-none tabular-nums text-primary-900">
                                {sizeCode.harvest_share_pct}%
                                {/* The tier is carried by the cell's colour,
                                    which a customer who cannot see it never
                                    gets. */}
                                <span className="sr-only"> of the harvest — {harvestTierLabel(tier)}</span>
                              </span>
                            )}
                            {sizeSoldOut && (
                              <span className="shrink-0 text-[11px] font-medium leading-none text-primary-900/50">
                                Sold out
                              </span>
                            )}
                          </span>
                          {(sizeCode.meta || (tier !== null && !sizeSoldOut)) && (
                            <span className="relative mt-1.5 flex items-baseline justify-between gap-2">
                              <span className="min-w-0 truncate text-[11px] leading-none text-primary-900/60">
                                {sizeCode.meta}
                              </span>
                              {tier !== null && !sizeSoldOut && (
                                <span
                                  aria-hidden="true"
                                  className="shrink-0 whitespace-nowrap text-[10px] leading-none text-primary-900/45"
                                >
                                  of harvest
                                </span>
                              )}
                            </span>
                          )}
                        </span>

                        {/* The tick, outside the corner: a mark does not
                            depend on telling two greens apart next to a gold
                            edge. aria-hidden because the radio already
                            carries the state to a screen reader. */}
                        {isSelected && (
                          <span
                            aria-hidden="true"
                            className="absolute -right-1.5 -top-1.5 z-10 flex h-5 w-5 items-center justify-center rounded-full bg-primary-600 text-white shadow ring-2 ring-surface-raised"
                          >
                            <CheckIcon className="h-3 w-3" />
                          </span>
                        )}
                      </label>
                    );
                  })}

                  {/* An odd number of grades leaves a hole in the last row, and
                      a half-empty row reads as a cell that failed to load. This
                      is that hole, drawn: inert, unlabelled, and hidden from
                      screen readers because there is nothing there to choose. */}
                  {sizeCodes.length % 2 === 1 && (
                    <span
                      aria-hidden="true"
                      className="rounded-xl border-[1.5px] border-dashed border-surface-border bg-surface-sunken/40"
                    />
                  )}
                </div>

                {/* What the share MEANS, for the grade actually chosen. Named,
                    because the line moves when the selection does and an
                    unattributed "About 2%" leaves the reader checking which
                    cell it belongs to. */}
                {selectedShare !== null && (
                  <p className="mt-3 rounded-xl bg-surface-sunken/70 px-3 py-2 text-xs text-primary-900/65">
                    <span className="font-semibold text-primary-900">
                      {activeSize?.code}
                    </span>{" "}
                    &mdash; {harvestShareSentence(selectedShare.pct, selectedShare.tier)}
                  </p>
                )}
              </fieldset>
            )}

            <fieldset>
              <legend className="mb-3 text-sm font-semibold text-primary-900">
                Choose a pack
              </legend>
              <div className="grid gap-2.5 sm:grid-cols-2">
                {units.map((unit) => {
                  const isSelected = unit.id === selectedId;
                  return (
                    <label
                      key={unit.id}
                      className={`flex items-center gap-3 rounded-xl border-[1.5px] px-3 py-2.5 text-sm transition-all ${
                        !unit.purchasable
                          ? "cursor-not-allowed border-surface-border bg-surface-sunken text-primary-900/40"
                          : isSelected
                            ? "cursor-pointer border-primary-600 bg-primary-50/70 ring-2 ring-primary-600/20"
                            : "cursor-pointer border-surface-border bg-surface-raised hover:border-primary-300"
                      }`}
                    >
                      <input
                        type="radio"
                        name="unit"
                        value={unit.id}
                        checked={isSelected}
                        disabled={!unit.purchasable}
                        onChange={() => {
                          setSelectedId(unit.id);
                          setDraftQty(null);
                        }}
                        className="peer sr-only"
                      />
                      {/* A drawn radio, so the choice reads as a choice and
                          not as a row of prices. */}
                      <span
                        aria-hidden="true"
                        className={`flex h-5 w-5 shrink-0 items-center justify-center rounded-full border-2 transition peer-focus-visible:ring-2 peer-focus-visible:ring-primary-600 peer-focus-visible:ring-offset-2 ${
                          isSelected && unit.purchasable
                            ? "border-primary-600 bg-primary-600"
                            : "border-surface-border bg-surface-raised"
                        }`}
                      >
                        {isSelected && unit.purchasable && (
                          <span className="h-2 w-2 rounded-full bg-white" />
                        )}
                      </span>
                      <span className="min-w-0 flex-1">
                        <span
                          className={`block font-semibold ${
                            unit.purchasable ? "text-primary-900" : "line-through"
                          }`}
                        >
                          {unit.label}
                        </span>
                        {/* What the grower wanted to say about this pack and
                            could not fit in its name. The only line under the
                            label: a per-kilo figure was here too and read as
                            clutter beside a pack that already names its own
                            weight. */}
                        {unit.meta && (
                          <span className="block truncate text-xs text-primary-900/60">
                            {unit.meta}
                          </span>
                        )}
                        {!unit.purchasable && (
                          <span className="block text-xs">Not available today</span>
                        )}
                      </span>
                      <span
                        className={`shrink-0 font-bold tabular-nums ${
                          unit.purchasable ? "text-primary-900" : ""
                        }`}
                      >
                        {unit.price_display}
                      </span>
                    </label>
                  );
                })}
              </div>
            </fieldset>

            <div className="hidden lg:flex">{buyControl}</div>

            {addToCart.isError && (
              <p role="alert" className="text-sm text-accent-800">
                {addToCart.error.message}
              </p>
            )}
          </div>

          {/* Three facts, not slogans — the same three the shop front makes,
              each one something the platform actually does. */}
          <ul className="mt-5 grid grid-cols-3 gap-2.5">
            {[
              { icon: <LeafIcon />, title: "Listed today", body: "by the grower" },
              { icon: <ClockIcon />, title: "Order by 4 pm", body: "daily cutoff" },
              { icon: <FarmIcon />, title: "Direct", body: "no cold store" },
            ].map((fact) => (
              <li
                key={fact.title}
                className="flex flex-col items-center gap-1 rounded-2xl bg-surface-raised/70 px-2 py-2.5 text-center ring-1 ring-cream-300/70"
              >
                <span className="flex h-8 w-8 items-center justify-center rounded-full bg-surface-raised text-primary-600 shadow-card">
                  {fact.icon}
                </span>
                <span className="text-xs font-semibold leading-tight text-primary-900">
                  {fact.title}
                </span>
                <span className="text-[11px] leading-tight text-primary-900/55">{fact.body}</span>
              </li>
            ))}
          </ul>

          {product.description && (
            <section className="mt-7">
              <h2 className="flex items-center gap-3 font-sans text-xs font-semibold uppercase tracking-[0.18em] text-primary-700">
                About this produce
                <span aria-hidden="true" className="h-px flex-1 bg-surface-border" />
              </h2>
              <p className="mt-3 whitespace-pre-line text-sm leading-relaxed text-primary-900/80">
                {product.description}
              </p>
            </section>
          )}
        </div>
      </div>

      {/* The pinned bar, phone widths only. */}
      <div className="fixed inset-x-0 bottom-0 z-30 border-t border-surface-border bg-surface-raised/95 px-4 pb-[max(0.75rem,env(safe-area-inset-bottom))] pt-3 shadow-[0_-8px_24px_-12px_rgba(26,54,40,0.18)] backdrop-blur lg:hidden">
        <div className="mx-auto flex max-w-xl items-center gap-4">
          {selected && shownQty === 0 && (
            <div className="shrink-0">
              <p className="text-lg font-bold leading-none tabular-nums text-primary-900">
                {selected.price_display}
              </p>
              <p className="mt-1 max-w-[7rem] truncate text-xs text-primary-900/55">
                {selected.label}
              </p>
            </div>
          )}
          {buyControl}
        </div>
      </div>
    </div>
  );
}

function ChevronIcon(): ReactElement {
  return (
    <svg aria-hidden="true" viewBox="0 0 20 20" fill="none" stroke="currentColor" strokeWidth={2} strokeLinecap="round" strokeLinejoin="round" className="h-3.5 w-3.5 shrink-0 text-primary-900/35">
      <path d="M7.5 4.5 13 10l-5.5 5.5" />
    </svg>
  );
}

function CheckIcon({ className }: { className: string }): ReactElement {
  return (
    <svg aria-hidden="true" viewBox="0 0 20 20" fill="none" stroke="currentColor" strokeWidth={3} strokeLinecap="round" strokeLinejoin="round" className={className}>
      <path d="M4.5 10.5 8 14l7.5-8" />
    </svg>
  );
}

function BagIcon(): ReactElement {
  return (
    <svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2} strokeLinecap="round" strokeLinejoin="round" className="h-5 w-5">
      <path d="M6 7h12l-1 13H7L6 7Z" />
      <path d="M9 7a3 3 0 0 1 6 0" />
    </svg>
  );
}

function LeafIcon(): ReactElement {
  return (
    <svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2} strokeLinecap="round" strokeLinejoin="round" className="h-4 w-4">
      <path d="M5 19c0-8 5-13 14-14 0 9-5 14-13 14" />
      <path d="M5 19 13 11" />
    </svg>
  );
}

function ClockIcon(): ReactElement {
  return (
    <svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2} strokeLinecap="round" strokeLinejoin="round" className="h-4 w-4">
      <circle cx="12" cy="12" r="8.5" />
      <path d="M12 7.5V12l3 2" />
    </svg>
  );
}

function FarmIcon(): ReactElement {
  return (
    <svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2} strokeLinecap="round" strokeLinejoin="round" className="h-4 w-4">
      <path d="M3 20h18" />
      <path d="M5 20V10l7-5 7 5v10" />
      <path d="M10 20v-5h4v5" />
    </svg>
  );
}
