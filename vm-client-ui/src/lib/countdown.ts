/**
 * Cutoff countdown formatting.
 *
 * Pure functions in their own module, with no React and no config import, so
 * they are testable without a Vite environment — pulling them out of the
 * component was the difference between "untestable" and eleven test cases.
 */
/**
 * Renders seconds as "11h 32m 18s", "8m 03s" or "47s".
 *
 * Seconds are always shown. The clock already ticked once a second, but with
 * hours left it only ever redrew the minutes — so for fifty-nine seconds out
 * of every sixty it looked like a static label rather than a countdown, which
 * is exactly how it was read. A moving digit is what makes it legible as time
 * running out.
 *
 * Larger units are dropped once they hit zero rather than padded to "0h", so
 * the last minute reads "47s" instead of the much less urgent "0h 0m 47s".
 */
export function formatRemaining(totalSeconds: number): string {
  const hours = Math.floor(totalSeconds / 3600);
  const minutes = Math.floor((totalSeconds % 3600) / 60);
  const seconds = totalSeconds % 60;

  const parts: string[] = [];
  if (hours > 0) parts.push(`${hours}h`);
  // Minutes are kept once there are hours, so "2h 0m 05s" does not skip to
  // "2h 05s" and read as two hours and five seconds.
  if (hours > 0 || minutes > 0) {
    parts.push(hours > 0 ? `${String(minutes).padStart(2, "0")}m` : `${minutes}m`);
  }
  parts.push(
    parts.length > 0 ? `${String(seconds).padStart(2, "0")}s` : `${seconds}s`,
  );
  return parts.join(" ");
}

/**
 * The countdown as a screen reader should hear it.
 *
 * Coarse on purpose: whole minutes above a minute, and only then seconds. The
 * visible clock changes every second, which is right for a glance and wrong
 * for speech.
 */
export function announceRemaining(totalSeconds: number): string {
  const minutes = Math.round(totalSeconds / 60);
  if (minutes >= 120) return `About ${Math.round(minutes / 60)} hours left`;
  if (minutes >= 60) {
    const hours = Math.floor(minutes / 60);
    const rest = minutes % 60;
    return rest === 0
      ? `About ${hours} hour${hours === 1 ? "" : "s"} left`
      : `About ${hours} hour${hours === 1 ? "" : "s"} and ${rest} minutes left`;
  }
  if (minutes >= 1) return `About ${minutes} minute${minutes === 1 ? "" : "s"} left`;
  return `Less than a minute left`;
}
