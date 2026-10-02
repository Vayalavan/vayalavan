/**
 * The Vayalavan logo.
 *
 * The master asset is `vayal-logo.png` in this directory — the Tamil வ glyph
 * with a sprouting leaf, above the VAYALAVAN wordmark. It lives here, in the UI
 * kit, so all three apps read the brand from one file and a future logo change
 * is a single replacement.
 *
 * The artwork is raster, so unlike an SVG it cannot inherit `currentColor`.
 * See VayalMarkMono for how the single-colour case is handled and what would
 * be needed to do it properly.
 */
import type { ReactElement } from "react";

/**
 * Served from each app's public/ directory rather than imported.
 *
 * Vite's library mode inlines every imported asset as base64, which put a
 * ~1 MB data URI inside the shared bundle — paid for on first load by every
 * customer, on a phone, before anything renders. As a plain URL it is a
 * separate cacheable request instead.
 *
 * The master lives in this directory; `make -C infra sync-brand` copies it
 * into each app's public/.
 */
const logoUrl = "/vayal-logo.png";

export type LogoSize = "sm" | "md" | "lg" | "xl";

/**
 * The full lockup keeps the artwork's own proportions (roughly 1:1, with the
 * wordmark occupying the bottom quarter).
 */
const LOCKUP_SIZE: Record<LogoSize, string> = {
  sm: "h-10",
  md: "h-14",
  lg: "h-20",
  xl: "h-32",
};

/**
 * The mark alone is square, because the wordmark is cropped away.
 */
const MARK_SIZE: Record<LogoSize, string> = {
  sm: "h-6 w-6",
  md: "h-8 w-8",
  lg: "h-12 w-12",
  xl: "h-20 w-20",
};

export interface VayalLockupProps {
  size?: LogoSize;
  /** Rendered as a single dark colour, for print and watermarks. */
  mono?: boolean;
  className?: string;
  /**
   * Set when the logo sits next to a visible "Vayalavan" text label,
   * so a screen reader does not read the name twice.
   */
  decorative?: boolean;
}

/**
 * The full lockup: glyph, leaf and VAYALAVAN wordmark.
 *
 * Use this wherever the brand needs to be named — headers, sign-in screens,
 * emails.
 */
export function VayalLockup({
  size = "md",
  mono = false,
  className = "",
  decorative = false,
}: VayalLockupProps): ReactElement {
  return (
    <img
      src={logoUrl}
      // Empty alt + aria-hidden is the correct pairing for decorative use;
      // otherwise the logo IS the brand name and must be announced.
      alt={decorative ? "" : "Vayalavan"}
      aria-hidden={decorative || undefined}
      className={`w-auto object-contain ${LOCKUP_SIZE[size]} ${
        mono ? "[filter:grayscale(1)_brightness(0)]" : ""
      } ${className}`.trim()}
      // Intrinsic size prevents layout shift before the image decodes.
      width={1024}
      height={1024}
    />
  );
}

export interface VayalMarkProps {
  size?: LogoSize;
  className?: string;
  decorative?: boolean;
}

/**
 * The mark alone — glyph and leaf, no wordmark.
 *
 * For tight spaces: a mobile header, an avatar slot, a favicon-sized badge.
 * The wordmark is cropped by showing only the top ~76% of the square artwork,
 * which is where the glyph sits.
 */
export function VayalMark({
  size = "md",
  className = "",
  decorative = false,
}: VayalMarkProps): ReactElement {
  return (
    <span
      className={`relative inline-block overflow-hidden ${MARK_SIZE[size]} ${className}`.trim()}
      role={decorative ? undefined : "img"}
      aria-label={decorative ? undefined : "Vayalavan"}
      aria-hidden={decorative || undefined}
    >
      {/* Scaled up and pulled from the top so the wordmark falls outside the
          clipping box. */}
      <img
        src={logoUrl}
        alt=""
        aria-hidden="true"
        className="absolute left-1/2 top-0 h-[132%] w-auto max-w-none -translate-x-1/2 object-contain"
      />
    </span>
  );
}

/**
 * The single-colour mark, flattened to solid black.
 *
 * For contexts two-tone cannot survive: a faxed invoice, a monochrome print
 * run, a watermark. Implemented with a CSS filter because the master asset is
 * raster — a true single-colour treatment needs an SVG from the designer, at
 * which point this should switch to `fill-current` and inherit text colour
 * like the rest of the system. Flagged rather than left looking intentional.
 */
export function VayalMarkMono({
  size = "md",
  className = "",
  decorative = false,
}: VayalMarkProps): ReactElement {
  return (
    <VayalMark
      size={size}
      decorative={decorative}
      className={`[filter:grayscale(1)_brightness(0)] ${className}`.trim()}
    />
  );
}
