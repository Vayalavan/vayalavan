/**
 * Pure helpers for the wallet and schedule screens — CLAUDE.md §6.7. Kept
 * free of React and the API client so they test under plain Node.
 */

/** The cutoff-relative charge time, in words: "3:30 pm, two days before…". */
export function chargeRuleText(options: {
  charge_lead_minutes: number;
  cutoff_hour_ist: number;
}): string {
  const minutes = options.cutoff_hour_ist * 60 - options.charge_lead_minutes;
  const hour24 = Math.floor(minutes / 60);
  const minute = minutes % 60;
  const hour12 = ((hour24 + 11) % 12) + 1;
  const suffix = hour24 >= 12 ? "pm" : "am";
  return `${hour12}:${String(minute).padStart(2, "0")} ${suffix}, two days before each delivery`;
}

/**
 * Rupees typed by a customer, to paise. Integer arithmetic on the digits —
 * never a float multiply, which turns 10.10 into 1009.9999 (rule 1). Null for
 * anything that is not a plain amount with at most two decimals.
 */
export function rupeesToPaise(input: string): number | null {
  const trimmed = input.trim().replace(/,/g, "").replace(/^₹/, "");
  if (!/^\d+(\.\d{1,2})?$/.test(trimmed)) return null;
  const [whole = "0", fraction = ""] = trimmed.split(".");
  return Number(whole) * 100 + Number(fraction.padEnd(2, "0"));
}

/** Paise to "₹1,050" — whole rupees for chips and prompts; "₹10.50" otherwise. */
export function formatRupees(paise: number): string {
  const rupees = Math.floor(paise / 100);
  const rest = paise % 100;
  const whole = new Intl.NumberFormat("en-IN").format(rupees);
  return rest === 0 ? `₹${whole}` : `₹${whole}.${String(rest).padStart(2, "0")}`;
}

/** The day of the month a date string names, for a monthly default. */
export function dayOfMonth(isoDate: string): number {
  return Number(isoDate.slice(8, 10));
}

/** The weekday (0 = Sunday) an ISO date names, computed without a timezone. */
export function weekdayOf(isoDate: string): number {
  const [y, m, d] = isoDate.split("-").map(Number);
  return new Date(Date.UTC(y ?? 1970, (m ?? 1) - 1, d ?? 1)).getUTCDay();
}

/** "2026-09-29" as "29 Sep 2026", read as a calendar date in no timezone. */
export function displayDate(isoDate: string): string {
  const [y, m, d] = isoDate.split("-").map(Number);
  return new Intl.DateTimeFormat("en-IN", {
    timeZone: "UTC", day: "2-digit", month: "short", year: "numeric",
  }).format(new Date(Date.UTC(y ?? 1970, (m ?? 1) - 1, d ?? 1)));
}
