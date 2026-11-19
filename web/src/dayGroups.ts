// The events and operations panels used to show a fixed 15-minute window, so
// every row was implicitly "today, a moment ago" and a bare clock time was
// enough. They now show the newest N rows whatever their age, and a bare
// "10:14:49" no longer says whether that was this morning or last March.
//
// Stamping the full date on every row answers the question and creates a new
// one: five identical "29/9/2026" prefixes are noise the eye has to step over
// on each line. So the date is hoisted out of the rows and into one heading
// per calendar day, and the rows keep the clock time that distinguishes them
// from each other.

export interface DayGroup<T> {
  // Local calendar day as YYYY-MM-DD. Stable as a React key and as the
  // `datetime` attribute of the heading's <time>.
  key: string;
  items: T[];
}

function dayKey(date: Date): string {
  // Local components, not toISOString(): an event at 00:30 local time on the
  // 30th belongs under the 30th for the person reading the screen, even when
  // UTC still calls it the 29th.
  const month = `${date.getMonth() + 1}`.padStart(2, "0");
  const day = `${date.getDate()}`.padStart(2, "0");
  return `${date.getFullYear()}-${month}-${day}`;
}

// Groups already-sorted-descending items into consecutive runs of the same
// local calendar day. Items whose instant does not parse keep their position
// rather than disappearing: a row the hub sent is a row the operator must
// see, even when its timestamp is unusable.
export function groupByDay<T>(items: readonly T[], instantOf: (item: T) => string): DayGroup<T>[] {
  const groups: DayGroup<T>[] = [];
  for (const item of items) {
    const parsed = new Date(instantOf(item));
    const key = Number.isFinite(parsed.getTime()) ? dayKey(parsed) : "";
    const last = groups[groups.length - 1];
    if (last && last.key === key) {
      last.items.push(item);
    } else {
      groups.push({ key, items: [item] });
    }
  }
  return groups;
}

export interface DayLabelNames {
  today: string;
  yesterday: string;
  unknownDay: string;
}

// Formats a day key as the heading above its rows. "Today" and "Yesterday"
// carry more meaning than a numeric date for the two days an operator is
// almost always asking about; anything older gets weekday, day and month, and
// the year only when it is not the current one — a year repeated on every
// heading is the same noise as a date repeated on every row.
export function formatDayLabel(key: string, locale: string, names: DayLabelNames, now = new Date()): string {
  if (key === "") return names.unknownDay;

  const todayKey = dayKey(now);
  if (key === todayKey) return names.today;

  const yesterday = new Date(now);
  yesterday.setDate(yesterday.getDate() - 1);
  if (key === dayKey(yesterday)) return names.yesterday;

  const [year, month, day] = key.split("-").map(Number);
  const date = new Date(year, month - 1, day);
  return new Intl.DateTimeFormat(locale, {
    weekday: "short",
    day: "numeric",
    month: "short",
    ...(year === now.getFullYear() ? {} : { year: "numeric" }),
  }).format(date);
}

// The clock time shown on the row itself. Seconds are kept: two events a few
// seconds apart during the same incident are a sequence, and rounding them to
// the minute erases the order.
export function formatRowTime(instant: string, locale: string): string {
  return new Date(instant).toLocaleTimeString(locale);
}

// The full instant, for the row's `title` and for screen readers. The day
// heading covers the common case; this covers the operator who is comparing
// two panels and needs the unabbreviated answer without leaving the page.
export function formatFullInstant(instant: string, locale: string): string {
  return new Date(instant).toLocaleString(locale, { dateStyle: "full", timeStyle: "medium" });
}
