import type { ReactElement } from "react";

/**
 * The category filter, as pictures.
 *
 * Same values, same tablist semantics and the same single piece of state the
 * pill buttons had — what changed is that a shopper now recognises
 * "Microgreens" by the tray of shoots rather than by reading five words in a
 * row. Produce is bought by eye.
 *
 * Each tile carries its own photograph as a background image, so a file that
 * fails to load leaves the tile's green wash and its label rather than a
 * broken frame.
 */

/** The photograph behind each category, served from public/photos. */
const TILE_IMAGE: Record<string, string> = {
  "": "/photos/cat-all.jpg",
  fruit: "/photos/cat-fruit.jpg",
  vegetable: "/photos/cat-vegetable.jpg",
  microgreen: "/photos/cat-microgreen.jpg",
  other: "/photos/cat-other.jpg",
};

export interface CategoryRailProps {
  categories: ReadonlyArray<readonly [string, string]>;
  value: string;
  onChange: (next: string) => void;
}

export function CategoryRail({
  categories,
  value,
  onChange,
}: CategoryRailProps): ReactElement {
  return (
    <div
      role="tablist"
      aria-label="Filter by category"
      // Scrolls sideways on a phone rather than wrapping to three ragged
      // rows; a grid from `sm` up, where all five fit.
      className="-mx-4 flex snap-x gap-3 overflow-x-auto px-4 pb-2 sm:mx-0 sm:grid sm:grid-cols-5 sm:overflow-visible sm:px-0"
    >
      {categories.map(([key, label]) => {
        const active = value === key;
        return (
          <button
            key={key || "all"}
            role="tab"
            aria-selected={active}
            onClick={() => onChange(key)}
            className={`group relative h-24 w-32 shrink-0 snap-start overflow-hidden rounded-2xl bg-primary-800 text-left transition-shadow sm:h-28 sm:w-auto ${
              active ? "shadow-card ring-2 ring-primary-600 ring-offset-2 ring-offset-surface" : "shadow-card"
            }`}
          >
            <span
              aria-hidden="true"
              className="absolute inset-0 bg-cover bg-center transition-transform duration-300 group-hover:scale-[1.06]"
              style={{ backgroundImage: `url('${TILE_IMAGE[key] ?? ""}')` }}
            />
            {/* A bottom-weighted fade rather than the hero's scrim: a tile is
                a quarter the height, so the same gradient compresses into
                something too pale to put a white label on. Darker again when
                selected, so the choice reads without relying on the ring
                alone — which a colourblind customer may not see against a
                photograph. */}
            <span
              aria-hidden="true"
              className={`absolute inset-0 bg-gradient-to-t from-primary-950 via-primary-950/45 to-primary-950/10 transition-opacity ${
                active ? "opacity-95" : "opacity-80 group-hover:opacity-90"
              }`}
            />
            <span className="relative flex h-full items-end p-3">
              <span className="text-sm font-semibold tracking-tight text-white">
                {label}
              </span>
            </span>
          </button>
        );
      })}
    </div>
  );
}
