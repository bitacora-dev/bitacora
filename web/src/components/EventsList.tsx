import type { BitacoraEvent } from "../api";
import { useTranslation } from "../i18n";

const SEVERITY_COLOR: Record<BitacoraEvent["severity"], string> = {
  debug: "text-neutral-500",
  info: "text-sky-400",
  notice: "text-teal-400",
  warn: "text-amber-400",
  error: "text-red-400",
  critical: "text-red-300 font-semibold",
};

export default function EventsList({ events }: { events: BitacoraEvent[] }) {
  const { t, intlTag } = useTranslation();

  if (events.length === 0) {
    return (
      <div className="events-empty">
        <h3>{t.eventsEmptyHeading}</h3>
        <p>{t.eventsEmptyBody}</p>
      </div>
    );
  }

  // Most recent first.
  const sorted = [...events].sort((a, b) => b.ts.localeCompare(a.ts));

  return (
    <ul className="event-list">
      {sorted.map((e) => (
        <li key={e.id}>
          <div>
            <span>{new Date(e.ts).toLocaleTimeString(intlTag)}</span>
            <span className={SEVERITY_COLOR[e.severity]}>{t.severity[e.severity]}</span>
            <span>{e.type}</span>
          </div>
          <p>{e.title}</p>
        </li>
      ))}
    </ul>
  );
}
