import type { ReactElement } from "react";

/**
 * Stand-in artwork for produce with no photograph.
 *
 * Served from each app's public/ rather than imported, for the same reason as
 * the logo: Vite's library mode inlines imported assets as base64, which would
 * put the whole PNG inside the shared bundle and make every customer download
 * it before anything renders — even the ones whose catalogue is fully
 * photographed. As a URL it is one cached request, shared by every card that
 * needs it, and skipped entirely when none do.
 *
 * Run `make sync-brand` after changing the master in vm-ui-kit/src/brand/.
 */
const placeholderUrl = "/produce-placeholder.png";

export function ProducePlaceholder({
  className = "",
  label = "No photo yet",
}: {
  className?: string;
  /** Visible caption. Pass "" to draw the artwork alone. */
  label?: string;
}): ReactElement {
  return (
    <div
      className={`relative flex h-full w-full items-center justify-center overflow-hidden bg-primary-50 ${className}`.trim()}
    >
      <img
        src={placeholderUrl}
        // Decorative: on a product card the product's own name is already
        // announced, and "line drawing of assorted vegetables" would only add
        // noise between the customer and the price.
        alt=""
        aria-hidden="true"
        loading="lazy"
        // cover, not contain. The artwork is a dense square of thirty small
        // drawings; shrunk to fit a short landscape thumbnail it becomes an
        // illegible smudge. Cropped and faded it reads as a deliberate
        // pattern instead, and the caption carries the actual meaning.
        className="h-full w-full scale-105 object-cover opacity-30"
      />

      {label && (
        <span className="absolute inset-0 flex items-center justify-center px-3 text-center text-sm font-medium text-primary-900/45">
          {label}
        </span>
      )}
    </div>
  );
}
