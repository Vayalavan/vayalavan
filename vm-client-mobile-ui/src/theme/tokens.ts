/**
 * The Vayal design tokens, in the shape React Native can consume.
 *
 * CLAUDE.md §9 puts the brand in one place: `packages/vm-ui-kit/tailwind-preset.js`.
 * That file is a Tailwind preset — it cannot be imported here, because this app
 * is not an npm workspace member and a native build only uploads its own
 * directory, so an import climbing out of this folder would work on a laptop
 * and fail on EAS.
 *
 * So the ramps are transcribed rather than imported, and `tokens.test.ts`
 * imports the preset and asserts every value matches. Change the brand in the
 * preset and this app's test fails until it is updated — which is the property
 * that matters. Components still never write a hex value; they reach for
 * `colors.primary[600]` exactly as the web components reach for
 * `text-primary-600`.
 */

/** Deep field green. The primary ramp. */
export const field = {
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
} as const;

/** Warm earth/soil. The secondary ramp. */
export const earth = {
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
} as const;

/** Amber. The accent ramp — "available today", and warnings. */
export const harvest = {
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
} as const;

/**
 * Rarity ramps: how common a GRADE is, drawn as a metal.
 *
 * Gold for the grade there is barely any of, bronze for the middle, silver for
 * the one that is most of the harvest. Desaturated against the brand greens on
 * purpose — this is produce, not a loot box, and a saturated gold chip beside
 * a field-green buy button fights it and wins.
 *
 * Only the steps in use are transcribed, matching the preset.
 */
export const gold = {
  50: "#fdf8e9",
  100: "#f7ecc4",
  200: "#ecd894",
  500: "#b8912a",
  700: "#7d611a",
} as const;

export const bronze = {
  50: "#f9f0e8",
  100: "#efdcc9",
  200: "#dcbe9d",
  500: "#a9743f",
  700: "#734c29",
} as const;

export const silver = {
  50: "#f4f5f5",
  100: "#e6e8e9",
  200: "#d2d6d8",
  500: "#8d9599",
  700: "#5d6467",
} as const;

/**
 * Cream: the storefront's canvas, transcribed from the preset like every other
 * ramp. White cards on a warm ivory page have an edge without a border doing
 * the work; the neutral `surface` stays for the pieces that must read as plain.
 */
export const cream = {
  50: "#fffcf7",
  100: "#f8f3e9",
  200: "#f1e8d6",
  300: "#e6d8bc",
  400: "#d4bd93",
} as const;

export const surface = {
  /** Off-white page background — warmer than pure white next to produce. */
  DEFAULT: "#faf9f6",
  raised: "#ffffff",
  sunken: "#f2f1ec",
  border: "#e4e2da",
} as const;

export const colors = {
  field,
  earth,
  harvest,

  // How common a grade is, for the product screen's size selector.
  gold,
  bronze,
  silver,

  // The page behind everything, and the header and tab bar over it.
  cream,

  // Semantic aliases. Prefer these: they say what the colour means, so a
  // palette change never requires renaming anything at the call site.
  primary: field,
  secondary: earth,
  accent: harvest,

  surface,
} as const;

/**
 * Text colours, pre-mixed.
 *
 * The web uses `text-primary-900/70` — Tailwind's slash opacity. React Native
 * has no equivalent for a colour used as `color`, and stacking `opacity` on a
 * <Text> also fades anything nested inside it. These are the same three tones
 * flattened against the surface colour once, here, instead of approximated
 * differently on every screen.
 */
export const text = {
  /** Headings, prices, and anything the customer must not misread. */
  strong: field[900],
  /** Body copy. */
  body: "#4a5b52",
  /** Captions, hints, secondary metadata. */
  muted: "#6b7a72",
  /** Placeholder and disabled text. */
  faint: "#93a099",
  /** On a primary-filled surface. */
  onPrimary: "#ffffff",
  /** Errors and destructive confirmations. */
  danger: harvest[800],
} as const;

/**
 * A 4pt spacing scale.
 *
 * Named by size rather than by purpose so it reads the same as Tailwind's
 * numeric scale the web UI uses: `spacing.md` is 12, matching `p-3`.
 */
export const spacing = {
  xs: 4,
  sm: 8,
  md: 12,
  lg: 16,
  xl: 24,
  xxl: 32,
} as const;

/** `rounded-card` in the preset is 0.75rem = 12px at the default root size. */
export const radius = {
  card: 12,
  // Panels rather than cards: the hero, a category tile, the "why choose"
  // block. The web's rounded-3xl is 24px; this is the same figure, and the
  // tokens test keeps them from drifting apart.
  panel: 24,
  pill: 999,
  input: 10,
} as const;

/**
 * Type scale.
 *
 * `tabular` is set wherever a number is shown: money and weights sit in
 * columns that must line up, and proportional digits make a rupee total jump
 * sideways as it updates.
 */
export const typography = {
  display: { fontSize: 24, fontWeight: "700" },
  title: { fontSize: 20, fontWeight: "700" },
  heading: { fontSize: 17, fontWeight: "600" },
  body: { fontSize: 15, fontWeight: "400" },
  bodyStrong: { fontSize: 15, fontWeight: "600" },
  label: { fontSize: 13, fontWeight: "600" },
  caption: { fontSize: 12, fontWeight: "400" },
} as const;

/**
 * The card shadow from the preset, expressed for both platforms.
 *
 * iOS reads shadowColor/Offset/Opacity/Radius; Android reads elevation only.
 * Setting just one gives a card that is raised on one platform and flat on the
 * other, which is the most common way a ported design looks subtly broken.
 */
export const shadow = {
  card: {
    shadowColor: field[900],
    shadowOffset: { width: 0, height: 2 },
    shadowOpacity: 0.06,
    shadowRadius: 8,
    elevation: 2,
  },
  /** The web's shadow-lift: a card that should float off the cream page. */
  lift: {
    shadowColor: field[900],
    shadowOffset: { width: 0, height: 10 },
    shadowOpacity: 0.12,
    shadowRadius: 20,
    elevation: 5,
  },
} as const;

/**
 * The minimum height of anything tappable.
 *
 * 44pt is Apple's floor and Android's is 48dp. This app is shopped one-handed,
 * on a phone, often while doing something else — and the controls that most
 * need the room are the quantity steppers, where a mis-tap costs money rather
 * than a moment. Not a target to shave.
 */
export const HIT_SIZE = 48;

export const theme = {
  colors,
  text,
  spacing,
  radius,
  typography,
  shadow,
  HIT_SIZE,
} as const;
