import { formatDayLabel, formatFullInstant, formatRowTime } from "../dayGroups";
import { useTranslation } from "../i18n";

// Events and operations both render a timestamped row, and both used to print
// a bare clock time. Once the panels stopped being a 15-minute window that
// became ambiguous: "10:14:49" reads as "just now" whether it happened this
// morning or last March. The date now lives in one heading per day, and the
// row's own <time> still carries the full instant for anyone who hovers, uses
// a screen reader, or is comparing two panels against each other.

interface DayHeadingProps {
  dayKey: string;
}

export function DayHeading({ dayKey }: DayHeadingProps) {
  const { t, intlTag } = useTranslation();
  const label = formatDayLabel(dayKey, intlTag, { today: t.dayToday, yesterday: t.dayYesterday, unknownDay: t.dayUnknown });
  return (
    <h3 className="event-day-heading">
      {dayKey === "" ? <span>{label}</span> : <time dateTime={dayKey}>{label}</time>}
    </h3>
  );
}

interface RowInstantProps {
  instant: string;
}

export default function RowInstant({ instant }: RowInstantProps) {
  const { t, intlTag } = useTranslation();
  const parsed = new Date(instant);
  if (!Number.isFinite(parsed.getTime())) return <span className="event-time">{t.dayUnknown}</span>;

  const full = formatFullInstant(instant, intlTag);
  return (
    <time className="event-time" dateTime={parsed.toISOString()} title={full} aria-label={t.rowInstantAria(full)}>
      {formatRowTime(instant, intlTag)}
    </time>
  );
}
