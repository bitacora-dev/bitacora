// Several inventory kinds carry an instant whose age is the whole point: a
// WireGuard handshake, the moment a share's size was last computed. Printing
// the absolute timestamp alone makes the reader do the subtraction, and a
// 24-hour-old figure presented without its age reads as current.
//
// Intl.RelativeTimeFormat is locale-aware on its own, so this stays out of the
// dictionaries; the caller passes the locale tag from useTranslation().

const MINUTE_SECONDS = 60;
const HOUR_SECONDS = 60 * MINUTE_SECONDS;
const DAY_SECONDS = 24 * HOUR_SECONDS;

const ZERO_INSTANT_PREFIX = "0001-01-01";

// Returns the instant in milliseconds, or null when the producer never set it.
// Go serializes an unset time.Time as the zero instant rather than omitting
// it, so "never happened" arrives looking like a date in year 1.
export function instantMillis(value: string | undefined | null): number | null {
  if (typeof value !== "string" || value === "" || value.startsWith(ZERO_INSTANT_PREFIX)) return null;
  const millis = new Date(value).getTime();
  return Number.isFinite(millis) ? millis : null;
}

export function ageSeconds(value: string | undefined | null, now = Date.now()): number | null {
  const millis = instantMillis(value);
  return millis === null ? null : Math.max(0, Math.round((now - millis) / 1000));
}

// Formats an elapsed duration as locale-aware relative text ("9 hours ago").
// Returns null rather than a placeholder so callers decide what absence looks
// like in their own panel.
export function formatAge(value: string | undefined | null, locale: string, now = Date.now()): string | null {
  const seconds = ageSeconds(value, now);
  if (seconds === null) return null;

  const format = new Intl.RelativeTimeFormat(locale, { numeric: "auto" });
  if (seconds < MINUTE_SECONDS) return format.format(-seconds, "second");
  if (seconds < HOUR_SECONDS) return format.format(-Math.round(seconds / MINUTE_SECONDS), "minute");
  if (seconds < DAY_SECONDS) return format.format(-Math.round(seconds / HOUR_SECONDS), "hour");
  return format.format(-Math.round(seconds / DAY_SECONDS), "day");
}

// Formats a remaining duration for the UPS runtime estimate. Seconds are
// meaningless for a battery that holds tens of minutes, and an estimate given
// to the second claims a precision the UPS does not have.
export function formatRuntime(seconds: number, locale: string): string {
  const totalMinutes = Math.floor(seconds / MINUTE_SECONDS);
  const hours = Math.floor(totalMinutes / 60);
  const minutes = totalMinutes % 60;
  const hourPart = new Intl.NumberFormat(locale, { style: "unit", unit: "hour", unitDisplay: "short" });
  const minutePart = new Intl.NumberFormat(locale, { style: "unit", unit: "minute", unitDisplay: "short" });
  if (hours > 0 && minutes > 0) return `${hourPart.format(hours)} ${minutePart.format(minutes)}`;
  if (hours > 0) return hourPart.format(hours);
  return minutePart.format(totalMinutes);
}
