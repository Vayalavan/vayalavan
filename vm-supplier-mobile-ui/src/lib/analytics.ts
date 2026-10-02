/**
 * The insights screen's arithmetic, kept out of the component so it can be
 * tested under plain Node — importing anything that pulls in react-native
 * fails the test runner, which is why every test in this app targets a module
 * like this one.
 *
 * Nothing here formats money. The API sends every amount already formatted
 * (`amount_display`), and paise arithmetic stays on the server where the
 * rounding rules live (CLAUDE.md rule 1).
 */

/** A short axis label from a server ISO date: "2026-08-18" -> "18 Aug". */
export function shortDate(iso: string): string {
  const months = ["Jan", "Feb", "Mar", "Apr", "May", "Jun",
                  "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];
  const parts = iso.split("-");
  const month = Number(parts[1]);
  const day = Number(parts[2]);
  if (!Number.isFinite(month) || !Number.isFinite(day)) return iso;
  return `${day} ${months[month - 1] ?? ""}`.trim();
}

/**
 * A share as a percentage label.
 *
 * Below 1% reads as "0%" and looks like a bug beside a non-zero amount, so it
 * is written as "<1%" instead.
 */
export function sharePct(part: number, whole: number): string {
  if (whole <= 0) return "0%";
  const pct = (part / whole) * 100;
  return pct > 0 && pct < 1 ? "<1%" : `${Math.round(pct)}%`;
}

/** The largest value in a series, or 1 so a division can never be by zero. */
export function peakOf(values: number[]): number {
  return values.reduce((max, value) => (value > max ? value : max), 1);
}

/** Grams as a grower thinks of them — kilos above 1 kg. */
export function formatKg(grams: number): string {
  if (grams < 1000) return `${grams} g`;
  const kg = grams / 1000;
  return `${Number.isInteger(kg) ? kg : kg.toFixed(1)} kg`;
}
