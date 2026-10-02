import { useEffect, useRef, useState, type ReactElement } from "react";
import { useQuery } from "@tanstack/react-query";
import { Link, useNavigate } from "react-router-dom";
import {
  Card, ApiError, ProductGridSkeleton, ProducePlaceholder,
} from "@vayal/ui-kit";
import { HomeHero } from "../components/HomeHero.js";
import { CategoryRail } from "../components/CategoryRail.js";
import { WhyVayal } from "../components/WhyVayal.js";
import { api } from "../lib/api.js";
import { useAddToCart, useCart, useUpdateCartQty, useRemoveCartItem } from "../lib/cart.js";
import { useAuth } from "../lib/auth.js";
import { summariseCatalogCounts } from "../lib/catalogCounts.js";

/**
 * Category tabs. "" is All, and the labels are what a customer would say —
 * "Vegetables", not the API's singular "vegetable".
 */
const CATEGORIES: ReadonlyArray<readonly [string, string]> = [
  ["", "All"],
  ["fruit", "Fruits"],
  ["vegetable", "Vegetables"],
  ["microgreen", "Microgreens"],
  ["other", "Other"],
];

interface CatalogUnit {
  id: string;
  label: string;
  weight_grams: number;
  price_paise: number;
  price_display: string;
  /** False when this pack is larger than the stock left today. */
  purchasable: boolean;
}

/**
 * One GRADE, as the card offers it: its own cover, its own packs, its own
 * stock. The grades do not share stock, so each carries its own sold-out
 * state — selling out M says nothing about the XL beside it.
 */
interface CatalogSizeCode {
  id: string;
  code: string;
  meta: string | null;
  image_url: string | null;
  stock_hint: string;
  any_unit_purchasable: boolean;
  units: CatalogUnit[];
}

interface CatalogProduct {
  id: string;
  name: string;
  type: string;
  grade: string | null;
  description: string | null;
  image_url: string | null;
  /**
   * The GRADE the card OPENS on. The server picks the first with something
   * buyable, so a card never leads with a sold-out M while the XL beside it
   * is on sale; the customer switches from there without leaving the grid.
   */
  size_code_id: string;
  size_code: string;
  size_meta: string | null;
  /** How many grades the product has today. More than one shows a selector. */
  size_code_count: number;
  /** Coarse phrase or "". The API never exposes exact stock. */
  stock_hint: string;
  /**
   * False when nothing on this card can be bought — sold out, closed by the
   * grower, or every pack larger than the stock left. The card is still
   * rendered, greyed out.
   */
  any_unit_purchasable: boolean;
  units: CatalogUnit[];
  /**
   * Every grade declared today, in the grower's order. The card holds them
   * all, so switching grade swaps the picture and the prices with no round
   * trip.
   */
  size_codes: CatalogSizeCode[];
}

interface Catalog {
  /** The business day, decided server-side in IST. */
  date: string;
  products: CatalogProduct[];
  /** Every card listed today, sold-out ones included. */
  total: number;
  /** How many of those can actually be bought right now. */
  sellable_total: number;
  grades: string[];
}

/**
 * Today's produce.
 *
 * The date is never sent: what counts as "today" is a server decision in
 * Asia/Kolkata (CLAUDE.md rule 2), and a browser with a skewed clock must not
 * be able to shop against another day's stock.
 */
export function CatalogPage(): ReactElement {
  const [type, setType] = useState("");
  const [grade, setGrade] = useState("");

  const catalog = useQuery<Catalog, ApiError>({
    queryKey: ["catalog", type, grade],
    queryFn: () =>
      api.get<Catalog>("/catalog", {
        query: { type: type || undefined, grade: grade || undefined },
      }),
    // Stock moves as other customers buy, so this must not go stale for long.
    staleTime: 20_000,
    refetchOnWindowFocus: true,
  });

  const formattedDate = catalog.data
    ? new Date(`${catalog.data.date}T00:00:00+05:30`).toLocaleDateString("en-IN", {
        day: "numeric",
        month: "long",
      })
    : "";

  return (
    <>
      <HomeHero dateLine={catalog.data ? ` Available for ${formattedDate}.` : ""} />

      {/* The category filter, as photographs. Same values and same single
          piece of state the pill buttons carried. */}
      <CategoryRail categories={CATEGORIES} value={type} onChange={setType} />

      {/* "Listed today", not "Available today": the grid now includes produce
          that has since sold out, shown greyed out rather than removed. The
          grade filter sits on the same line — it is a refinement of this list,
          not a section of its own. */}
      <div className="mb-4 mt-8 flex flex-wrap items-end justify-between gap-3">
        <div>
          <h2 className="text-2xl font-semibold tracking-tight text-primary-900 sm:text-3xl">
            Listed today
          </h2>
          {catalog.data && (
            <p className="mt-0.5 text-sm text-primary-900/60">
              {summariseCatalogCounts(
                catalog.data.sellable_total,
                catalog.data.total,
              )}
            </p>
          )}
        </div>

        {catalog.data && catalog.data.grades.length > 0 && (
          <div>
            <label htmlFor="grade" className="sr-only">
              Filter by grade
            </label>
            <select
              id="grade"
              value={grade}
              onChange={(e) => setGrade(e.target.value)}
              className="rounded-full border border-cream-300 bg-surface-raised px-4 py-2 text-sm font-medium text-primary-800 shadow-card"
            >
              <option value="">All grades</option>
              {catalog.data.grades.map((g) => (
                <option key={g} value={g}>
                  Grade {g}
                </option>
              ))}
            </select>
          </div>
        )}
      </div>

      {catalog.isPending && <ProductGridSkeleton count={8} />}

      {catalog.isError && (
        <Card>
          <p role="alert" className="text-sm text-accent-800">
            {catalog.error.message}
          </p>
        </Card>
      )}

      {catalog.data && catalog.data.products.length === 0 && (
        <Card>
          <h2 className="text-[0.975rem] font-semibold leading-snug tracking-tight text-primary-900">Nothing available right now</h2>
          <p className="mt-1 text-sm text-primary-900/70">
            {type || grade
              ? "No produce matches those filters today."
              : "Our suppliers have not listed produce for today yet. Please check back later this morning."}
          </p>
        </Card>
      )}

      {catalog.data && catalog.data.products.length > 0 && (
        <>
          {/* One column on a phone, up to four on a desktop. */}
          <div className="grid gap-5 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4">
            {catalog.data.products.map((product) => (
              <ProductCard key={product.id} product={product} />
            ))}
          </div>
        </>
      )}

      {/* Below the produce: someone who came to shop reaches the shopping
          first, and someone still deciding whether to trust us reads on. */}
      <WhyVayal />
    </>
  );
}

/** The pack a card should open on: the first one that can actually be bought. */
function firstBuyableId(units: CatalogUnit[]): string {
  return (units.find((unit) => unit.purchasable) ?? units[0])?.id ?? "";
}

function ProductCard({ product }: { product: CatalogProduct }): ReactElement {
  // Every grade came down with the listing, so switching between them is a
  // local move: the photograph, the packs and the prices all swap with no
  // round trip. The card opens on the grade the server chose — the first with
  // something buyable.
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
  const imageUrl = activeSize?.image_url ?? product.image_url;

  const [selectedId, setSelectedId] = useState(() => firstBuyableId(units));

  const selected = units.find((unit) => unit.id === selectedId);

  const { user } = useAuth();
  const { cart } = useCart();
  const navigate = useNavigate();
  const addToCart = useAddToCart();
  const updateQty = useUpdateCartQty();
  const removeItem = useRemoveCartItem();

  // The line for the pack currently selected, if it is already in the cart.
  // Keyed on the UNIT, not the product: a customer may hold both a 1 kg and a
  // 5 kg pack of the same tomatoes, and the stepper must edit the one shown.
  const line = cart.data?.items.find((item) => item.product_unit_id === selectedId);

  /**
   * Quantity the customer has tapped but the server has not confirmed.
   *
   * Same debounce as the cart drawer, for the same reason: a tap per request
   * plus a refetch each time is what tripped the gateway's rate limit.
   */
  const [draftQty, setDraftQty] = useState<number | null>(null);
  const qtyTimer = useRef<ReturnType<typeof setTimeout> | null>(null);

  useEffect(
    () => () => {
      if (qtyTimer.current) clearTimeout(qtyTimer.current);
    },
    [],
  );

  // Once the server agrees with the draft, stop overriding it.
  useEffect(() => {
    if (line && draftQty !== null && line.qty === draftQty) setDraftQty(null);
  }, [line, draftQty]);

  // Switching grade changes which packs exist — 1 kg of M and 1 kg of XL are
  // different goods with different ids — so the selection has to move to a
  // pack of the grade now showing, and any un-sent quantity is dropped with
  // it. Also covers stock running out under the selected pack on a refetch.
  useEffect(() => {
    if (units.some((unit) => unit.id === selectedId)) return;
    setSelectedId(firstBuyableId(units));
    setDraftQty(null);
  }, [units, selectedId]);

  const shownQty = draftQty ?? line?.qty ?? 0;

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

  function handleAdd(): void {
    if (!selected) return;
    // The cart is server-side and keyed by customer, so there is nothing to
    // add to until they are signed in. Sending them to sign-in beats a 401
    // rendered as a generic error under the button.
    if (!user) {
      navigate("/account");
      return;
    }
    // No drawer. Adding an item should not interrupt browsing — the card
    // itself turns into a stepper, and the header count confirms it landed.
    addToCart.mutate({ product_unit_id: selected.id, qty: 1 });
  }

  return (
    <article
      // h-full: the grid stretches cells to the tallest in the row, and
      // without this the card floats inside its cell at its natural height —
      // which is what made a row of cards look ragged.
      //
      // A ring rather than a border, and a lift on hover: the card should read
      // as an object sitting on the page, which is the difference between a
      // shop and a table of rows. The lift is a shadow and a 1px rise, not a
      // scale — produce photography must not resample on hover.
      className={`group flex h-full flex-col overflow-hidden rounded-3xl bg-surface-raised shadow-card ring-1 ring-cream-300/70 transition duration-300 hover:-translate-y-1 hover:shadow-lift motion-reduce:transform-none motion-reduce:transition-none ${
        soldOut ? "opacity-75" : ""
      }`}
    >
      {/* A FIXED height, not an aspect ratio.
          An aspect ratio ties the image's height to the column width, so the
          same card is a different height at every breakpoint and any column
          that measures differently throws the row out of line. A fixed height
          makes every thumbnail identical regardless of the photo's own
          proportions, which is what makes a grid read as a grid. */}
      <div className="relative m-2 mb-0 h-52 shrink-0 overflow-hidden rounded-[1.25rem] bg-gradient-to-b from-cream-50 to-cream-200/70 sm:h-56">
        {imageUrl ? (
          <img
            // Keyed on the grade so switching size replaces the photograph
            // rather than leaving the previous grade's on screen while the new
            // one loads — the pictures are OF the grade, and a stale one
            // misdescribes what is being priced.
            key={activeSize?.id ?? product.id}
            src={imageUrl}
            alt={
              activeSize && sizeCodes.length > 1
                ? `${product.name}, size ${activeSize.code}`
                : product.name
            }
            loading="lazy"
            // object-CONTAIN, not cover. Cover filled the box neatly but
            // cropped, and on produce photography the crop takes the part
            // that identifies the item — an apple lost its stem and leaf off
            // the top edge. Showing the whole product matters more than a
            // flush edge in a shop.
            //
            // The letterboxing that contain implies is invisible here because
            // the box is the card's own white, and product shots arrive on
            // white or transparent backgrounds.
            className={`h-full w-full object-contain p-3 mix-blend-multiply transition-transform duration-300 group-hover:scale-[1.04] motion-reduce:transform-none ${
              soldOut ? "grayscale" : ""
            }`}
          />
        ) : (
          <ProducePlaceholder label={soldOut ? "" : "No photo yet"} />
        )}

        {soldOut && (
          <div className="absolute inset-0 flex items-center justify-center bg-surface/70">
            <span className="rounded-full bg-primary-900/90 px-3.5 py-1.5 text-sm font-semibold tracking-tight text-white shadow-card">
              Sold out for today
            </span>
          </div>
        )}

        {/* A nudge, never a number. */}
        {!soldOut && stockHint && (
          <span className="absolute left-3 top-3 rounded-full bg-accent-500 px-2.5 py-1 text-xs font-semibold tracking-tight text-white shadow-card">
            {stockHint}
          </span>
        )}
      </div>

      <div className="flex flex-1 flex-col p-4">
        <div className="flex items-start justify-between gap-2">
          <h2 className="text-[0.975rem] font-semibold leading-snug tracking-tight text-primary-900">
            {/* The card links to the product's own page. The stepper below
                stays on the card, so browsing and buying do not compete. */}
            <Link to={`/product/${product.id}`} className="hover:underline">
              {product.name}
            </Link>
          </h2>
          {product.grade && (
            <span className="shrink-0 rounded-full bg-surface-sunken px-2 py-0.5 text-xs text-primary-900/70">
              {product.grade}
            </span>
          )}
        </div>

        {/* The grade is chosen below rather than named here — the chips say
            which one is showing, and the product page is no longer the only
            place to switch. */}
        <p className="mt-0.5 text-xs capitalize text-primary-900/50">
          {product.type}
        </p>

        {/* Always rendered, even when empty, and always exactly two lines.
            Letting it collapse pushed the pack sizes and price up on products
            with no description, so neighbouring cards disagreed about where
            their price sat. */}
        <p className="mt-1 line-clamp-2 min-h-[2.5rem] text-sm text-primary-900/70">
          {product.description ?? ""}
        </p>

        {/* The size selector. Hidden when there is only one grade — a grower
            who does not grade should not be shown a choice of one. Sold-out
            grades stay selectable, unlike packs: seeing the XL photograph and
            what it would have cost is worth more than an unclickable chip. */}
        {sizeCodes.length > 1 && (
          <div className="mt-3">
            <fieldset>
              <legend className="mb-1 text-xs font-medium text-primary-900/60">
                Size
              </legend>
              <div className="flex flex-wrap gap-1.5">
                {sizeCodes.map((sizeCode) => {
                  const isSelected = sizeCode.id === selectedSizeId;
                  const gradeSoldOut = !sizeCode.any_unit_purchasable;
                  return (
                    <label
                      key={sizeCode.id}
                      className={`cursor-pointer rounded-card border px-2.5 py-1.5 text-sm transition-colors ${
                        isSelected
                          ? "border-primary-600 bg-primary-50 font-medium text-primary-800"
                          : gradeSoldOut
                            ? "border-surface-border bg-surface-sunken text-primary-900/40"
                            : "border-surface-border text-primary-900/80 hover:border-primary-400"
                      }`}
                    >
                      <input
                        type="radio"
                        name={`size-${product.id}`}
                        value={sizeCode.id}
                        checked={isSelected}
                        onChange={() => setSelectedSizeId(sizeCode.id)}
                        className="sr-only"
                      />
                      {sizeCode.code}
                      {gradeSoldOut && (
                        <span className="sr-only"> — sold out for today</span>
                      )}
                    </label>
                  );
                })}
              </div>
            </fieldset>
            {/* The chosen grade's own words for what it is — "150 g - 200 g".
                Under the chips rather than inside them, so a row of three
                stays a row of three. */}
            {activeSize?.meta && (
              <p className="mt-1 text-xs text-primary-900/50">{activeSize.meta}</p>
            )}
          </div>
        )}

        <div className="mt-3">
          <fieldset>
            <legend className="sr-only">Choose a pack size for {product.name}</legend>
            <div className="flex flex-wrap gap-1.5">
              {units.map((unit) => {
                const isSelected = unit.id === selectedId;
                return (
                  <label
                    key={unit.id}
                    className={`cursor-pointer rounded-card border px-2.5 py-1.5 text-sm transition-colors ${
                      !unit.purchasable
                        ? // Kept visible but unselectable: the customer should
                          // see the 5 kg pack exists and is simply out today.
                          "cursor-not-allowed border-surface-border bg-surface-sunken text-primary-900/35 line-through"
                        : isSelected
                          ? "border-primary-600 bg-primary-50 font-medium text-primary-800"
                          : "border-surface-border text-primary-900/80 hover:border-primary-400"
                    }`}
                  >
                    <input
                      type="radio"
                      name={`unit-${product.id}`}
                      value={unit.id}
                      checked={isSelected}
                      disabled={!unit.purchasable}
                      onChange={() => setSelectedId(unit.id)}
                      className="sr-only"
                    />
                    {unit.label}
                  </label>
                );
              })}
            </div>
          </fieldset>
        </div>

        {/* mt-auto pins this to the bottom of the card, so price and button
            line up across the row however tall the name or chips above are. */}
        <div className="mt-auto flex items-end justify-between gap-2 pt-3">
          <div>
            <p className="text-xl font-semibold tracking-tight text-primary-900">
              {selected?.price_display ?? "—"}
            </p>
            {selected && (
              <p className="text-xs text-primary-900/50">per {selected.label}</p>
            )}
          </div>

          {/* Once it is in the cart the button becomes a stepper in place, so
              a customer buying three of something taps the same spot three
              times instead of opening a panel and hunting for the row. */}
          {shownQty > 0 ? (
            <div className="flex items-center gap-1 rounded-full border border-primary-600 bg-primary-50/60 p-0.5">
              <button
                type="button"
                aria-label={
                  shownQty === 1
                    ? `Remove ${product.name} from cart`
                    : `Decrease ${product.name} quantity`
                }
                onClick={() => changeQty(shownQty - 1)}
                className="h-8 w-8 rounded-full text-lg leading-none text-primary-800 transition hover:bg-primary-100"
              >
                {shownQty === 1 ? "×" : "−"}
              </button>
              <span
                aria-live="polite"
                aria-label={`${shownQty} in cart`}
                className="w-7 text-center text-sm font-semibold tabular-nums text-primary-900"
              >
                {shownQty}
              </span>
              <button
                type="button"
                aria-label={`Increase ${product.name} quantity`}
                disabled={!selected?.purchasable}
                onClick={() => changeQty(shownQty + 1)}
                className="h-8 w-8 rounded-full text-lg leading-none text-primary-800 transition hover:bg-primary-100 disabled:opacity-40"
              >
                +
              </button>
            </div>
          ) : (
            <button
              type="button"
              onClick={handleAdd}
              disabled={soldOut || !selected?.purchasable || addToCart.isPending}
              // The size is named when there is a choice of them: two cards'
              // worth of "Add tomatoes, 1 kg box" would otherwise be
              // indistinguishable to a screen reader.
              aria-label={
                selected
                  ? `Add ${product.name}, ${
                      activeSize && sizeCodes.length > 1 ? `size ${activeSize.code}, ` : ""
                    }${selected.label}, to cart`
                  : "Add to cart"
              }
              className="rounded-full bg-primary-600 px-4 py-2.5 text-sm font-semibold text-white shadow-card transition hover:bg-primary-700 active:scale-[0.98] disabled:cursor-not-allowed disabled:bg-surface-sunken disabled:text-primary-900/40 disabled:shadow-none motion-reduce:transform-none"
            >
              {soldOut ? "Sold out" : addToCart.isPending ? "Adding…" : "Add to cart"}
            </button>
          )}
        </div>

        {/* Failures belong on the card that caused them — stock can run out
            between the page loading and the tap, and the customer needs to
            know it was this product. */}
        {addToCart.isError && (
          <p role="alert" className="mt-2 text-xs text-accent-800">
            {addToCart.error.message}
          </p>
        )}
      </div>
    </article>
  );
}
