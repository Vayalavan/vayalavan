/**
 * Dates and times, always in Asia/Kolkata.
 *
 * CLAUDE.md rule 2: every business day — the 4pm cutoff, the delivery day —
 * is an IST day. A customer's phone is normally on IST too, but "normally" is
 * not a guarantee: a handset with the wrong zone set, or someone ordering from
 * abroad for family at home, would otherwise be shown a delivery day one off
 * from the one the courier works to.
 *
 * The web UIs get this from `Intl.DateTimeFormat` with `timeZone:
 * "Asia/Kolkata"`. That option is NOT reliable here. React Native's Hermes
 * engine ships without a full ICU database on Android, and depending on the
 * build, `timeZone` is either ignored — silently formatting in device local
 * time, which is the exact bug this is supposed to prevent — or throws a
 * RangeError. Neither is acceptable for the date a customer plans their week
 * around.
 *
 * So the conversion is done by arithmetic instead. India has observed a single
 * offset of UTC+05:30 nationwide with no daylight saving since 1945, and no
 * change is proposed; there is no rule to look up and nothing to get wrong.
 * That makes this both more portable AND more correct than Intl here.
 */

/** IST is UTC+05:30, always, everywhere in India. */
const IST_OFFSET_MINUTES = 5 * 60 + 30;

const MONTHS = [
  "Jan", "Feb", "Mar", "Apr", "May", "Jun",
  "Jul", "Aug", "Sep", "Oct", "Nov", "Dec",
] as const;

const DATE_ONLY = /^\d{4}-\d{2}-\d{2}$/;

/** The calendar/clock fields of an instant, as they read in India. */
export interface ISTParts {
  year: number;
  /** 1-12. */
  month: number;
  /** 1-31. */
  day: number;
  /** 0-23. */
  hour: number;
  minute: number;
  second: number;
}

/**
 * Parses any timestamp the API sends into IST calendar fields.
 *
 * Handles both shapes: a full RFC3339 timestamp for `placed_at` and
 * `processing_at`, and a bare "YYYY-MM-DD" for `delivery_day` and
 * `available_on`. A bare date is anchored to IST midnight explicitly —
 * `new Date("2026-08-16")` parses as UTC midnight, which is still the 15th
 * anywhere behind UTC, and that off-by-one is how a delivery date silently
 * moves a day.
 *
 * Returns null when the value is not a date at all, so callers can fall back
 * to showing the server's own string rather than "Invalid Date".
 */
export function toISTParts(value: string): ISTParts | null {
  if (value === "") return null;

  if (DATE_ONLY.test(value)) {
    const year = Number(value.slice(0, 4));
    const month = Number(value.slice(5, 7));
    const day = Number(value.slice(8, 10));
    if (month < 1 || month > 12 || day < 1 || day > 31) return null;
    return { year, month, day, hour: 0, minute: 0, second: 0 };
  }

  const parsed = new Date(value);
  const epochMs = parsed.getTime();
  if (Number.isNaN(epochMs)) return null;

  // Shift the instant by the offset, then read UTC fields. Reading UTC fields
  // of a shifted instant gives the wall clock in that zone — and, unlike the
  // getFullYear/getHours family, involves the device's own timezone nowhere.
  const shifted = new Date(epochMs + IST_OFFSET_MINUTES * 60_000);
  return {
    year: shifted.getUTCFullYear(),
    month: shifted.getUTCMonth() + 1,
    day: shifted.getUTCDate(),
    hour: shifted.getUTCHours(),
    minute: shifted.getUTCMinutes(),
    second: shifted.getUTCSeconds(),
  };
}

/** "15 Aug 2026". */
function formatParts(parts: ISTParts): string {
  return `${parts.day} ${MONTHS[parts.month - 1]} ${parts.year}`;
}

/** "3:28 am" — 12-hour, matching the web UIs and how a clock is read here. */
function formatTime(parts: ISTParts): string {
  const meridiem = parts.hour < 12 ? "am" : "pm";
  const hour12 = parts.hour % 12 === 0 ? 12 : parts.hour % 12;
  return `${hour12}:${String(parts.minute).padStart(2, "0")} ${meridiem}`;
}

/**
 * A date with no invented time of day: "16 Aug 2026".
 *
 * For `delivery_day` and `available_on`, which are dates, not instants.
 * Falls back to the server's own string if it cannot be parsed — wrong-looking
 * is recoverable, "Invalid Date" is alarming.
 */
export function formatISTDate(value: string): string {
  const parts = toISTParts(value);
  return parts ? formatParts(parts) : value;
}

/** A full timestamp: "15 Aug 2026, 3:28 am". */
export function formatISTDateTime(value: string): string {
  const parts = toISTParts(value);
  return parts ? `${formatParts(parts)}, ${formatTime(parts)}` : value;
}

/** Just the clock: "4:00 pm". For the cutoff, where the date is already known. */
export function formatISTTime(value: string): string {
  const parts = toISTParts(value);
  return parts ? formatTime(parts) : value;
}

/**
 * Today's date in IST as "YYYY-MM-DD", from the device clock.
 *
 * Display only — a heading, a hint about which day the catalogue covers. The
 * SERVER decides what "today" is for anything that decides whether produce can
 * be BOUGHT (CLAUDE.md §5.2, and the `date` the catalogue endpoint echoes back).
 * A phone with a wrong clock must not be able to buy against yesterday's stock.
 */
export function istTodayISO(now: Date = new Date()): string {
  const shifted = new Date(now.getTime() + IST_OFFSET_MINUTES * 60_000);
  const year = shifted.getUTCFullYear();
  const month = String(shifted.getUTCMonth() + 1).padStart(2, "0");
  const day = String(shifted.getUTCDate()).padStart(2, "0");
  return `${year}-${month}-${day}`;
}
