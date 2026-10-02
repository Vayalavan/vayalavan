/**
 * The Vayalavan Tailwind preset.
 *
 * CLAUDE.md §9: deep field green primary, warm earth/soil secondary,
 * off-white surface, amber accent for "available today" badges — defined as
 * theme tokens here and NEVER as hex values in a component. One preset, three
 * UIs: a brand change is a change to this file alone.
 *
 * Consume from a UI's tailwind.config.js:
 *
 *   import vayalPreset from "@vayal/ui-kit/tailwind-preset";
 *   export default { presets: [vayalPreset], content: [...] };
 */

/**
 * Colour ramps. Each is a full 50-950 scale so components can reach for a
 * lighter or darker step without inventing an off-palette value.
 */
const field = {
  50: "#f0f7f2",
  100: "#dbebe0",
  200: "#b8d7c3",
  300: "#8bbb9e",
  400: "#5b9a76",
  500: "#3a7d59",
  600: "#2b6446",
  700: "#245039",
  800: "#1f412f",
  900: "#1a3628",
  950: "#0d1e16",
};

const earth = {
  50: "#faf6f1",
  100: "#f2e9dd",
  200: "#e3d1b9",
  300: "#d0b28e",
  400: "#bd9166",
  500: "#ab7749",
  600: "#8f5f3c",
  700: "#734b33",
  800: "#5f3f2e",
  900: "#503628",
  950: "#2c1c14",
};

const harvest = {
  50: "#fff9ed",
  100: "#fff1d4",
  200: "#ffdfa8",
  300: "#ffc871",
  400: "#ffa938",
  500: "#f98e11",
  600: "#e07207",
  700: "#b95508",
  800: "#93430f",
  900: "#78390f",
  950: "#411a05",
};

/**
 * Rarity ramps: how common a GRADE is, drawn as a metal.
 *
 * A grower's crates are not equal piles — a season might run 55% M and 3% XL2
 * — and CLAUDE.md §5.2's size selector drew every grade identically, so the
 * pick of the field looked like the bulk of it. These three ramps carry that
 * difference: gold for the grade there is barely any of, bronze for the middle,
 * silver for the one that is most of the harvest.
 *
 * Deliberately desaturated against the brand greens. This is produce, not a
 * loot box: the metals have to read as a quiet distinction beside a photograph
 * of fruit, and a saturated gold chip next to a field-green buy button would
 * fight it and win.
 *
 * Only the steps that get used are defined. A half-used 50-950 scale invented
 * to look complete is nine values nobody checked against the greens.
 */
const gold = {
  50: "#fdf8e9",
  100: "#f7ecc4",
  200: "#ecd894",
  500: "#b8912a",
  700: "#7d611a",
};

const bronze = {
  50: "#f9f0e8",
  100: "#efdcc9",
  200: "#dcbe9d",
  500: "#a9743f",
  700: "#734c29",
};

const silver = {
  50: "#f4f5f5",
  100: "#e6e8e9",
  200: "#d2d6d8",
  500: "#8d9599",
  700: "#5d6467",
};

/**
 * Cream: the storefront's canvas.
 *
 * The customer apps sat on the same near-white as the admin console, and a
 * shop that looks like a spreadsheet does not sell fruit. A warm ivory under
 * white cards gives every card an edge without a border doing the work, and it
 * sits beside the field green the way paper sits beside ink.
 *
 * Customer-facing only. The admin console and the supplier UI stay on
 * `surface`: tables want a neutral page, not a warm one. Only the steps in use.
 */
const cream = {
  50: "#fffcf7",
  100: "#f8f3e9",
  200: "#f1e8d6",
  300: "#e6d8bc",
  400: "#d4bd93",
};

/** @type {import('tailwindcss').Config} */
export default {
  theme: {
    extend: {
      colors: {
        // Raw ramps, for when a component needs a specific step.
        field,
        earth,
        harvest,

        // How common a grade is, for the storefront's size selector.
        gold,
        bronze,
        silver,

        cream,

        // Semantic aliases. Prefer these in components: they say what the
        // colour means, so a palette change does not require renaming
        // classes across three apps.
        primary: field,
        secondary: earth,
        accent: harvest,

        surface: {
          // Off-white page background — warmer than pure white, which looks
          // clinical next to produce photography.
          DEFAULT: "#faf9f6",
          raised: "#ffffff",
          sunken: "#f2f1ec",
          border: "#e4e2da",
        },
      },

      fontFamily: {
        // System stacks: no webfont request on first paint, which matters on
        // the mid-range Android handsets most of our customers shop from.
        sans: [
          "Inter",
          "ui-sans-serif",
          "system-ui",
          "-apple-system",
          "Segoe UI",
          "Roboto",
          "sans-serif",
        ],
        // Headings on the storefront. A serif is the cheapest single signal
        // of care a shop can send; the body stays in the sans for legibility
        // at small sizes and in prices.
        display: ["Fraunces", "Georgia", "ui-serif", "serif"],
      },

      borderRadius: {
        card: "0.75rem",
        // Panels rather than cards: a hero, a category tile, a section block.
        // A larger radius reads as one soft object instead of a boxed-in
        // rectangle, which is most of what separates a shop front from a form.
        //
        // The same 1.5rem as Tailwind's own `rounded-3xl`, which is what the
        // web components reach for — this entry exists so the figure has a
        // name the two mobile apps can bind their `radius.panel` to, and so a
        // change to it is a change in one file.
        panel: "1.5rem",
        // Fully rounded ends for a small status chip — "Cover", "Shown first",
        // "Available today". `rounded-badge` was already spelled at three call
        // sites before this entry existed, which meant square corners
        // everywhere a badge was drawn. The two mobile apps bind their
        // `radius.pill` to the same idea.
        badge: "9999px",
      },

      boxShadow: {
        card: "0 1px 2px rgba(26, 54, 40, 0.06), 0 4px 12px rgba(26, 54, 40, 0.05)",
        // A card being pointed at, or one that should float off a cream page.
        lift: "0 1px 2px rgba(26, 54, 40, 0.05), 0 18px 40px -16px rgba(26, 54, 40, 0.22)",
      },

      keyframes: {
        // A highlight travelling across a chip, left to right. Used on the
        // RAREST grade only — a shimmer on every chip is a shimmer on none,
        // and the point is that one of them is different.
        //
        // Translate rather than a moving background-position: a transform is
        // composited, so this does not repaint a chip full of text four
        // seconds at a time on a mid-range Android.
        shimmer: {
          "0%": { transform: "translateX(-120%)" },
          // The sweep takes the first third; the rest of the cycle is the
          // pause between sweeps. A continuous shimmer reads as a loading
          // skeleton, which is the one thing this must not look like.
          "35%, 100%": { transform: "translateX(220%)" },
        },
      },

      animation: {
        // Slow on purpose. Apply through Tailwind's `motion-safe:` variant so
        // a reduced-motion setting leaves the chip still.
        shimmer: "shimmer 3.2s ease-in-out infinite",
      },
    },
  },
  plugins: [],
};
