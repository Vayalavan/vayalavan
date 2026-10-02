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
  /** Headings and anything the supplier must not misread. */
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
} as const;

/**
 * The minimum height of anything tappable.
 *
 * 44pt is Apple's floor and Android's is 48dp. Suppliers use this app outdoors,
 * one-handed, often with wet or muddy hands — this is not a target to shave.
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
