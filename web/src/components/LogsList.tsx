import type { LogEntry } from "../api";
import { useTranslation } from "../i18n";

interface Props {
  entries: LogEntry[];
  // Durable entry ids a selected event or job points at. Marked rows carry a
  // visible label, not only a colour, so the mark survives a monochrome or
  // high-contrast rendering.
  referencedIDs?: readonly string[];
}

export default function LogsList({ entries, referencedIDs }: Props) {
  const { t, intlTag } = useTranslation();
  if (entries.length === 0) return <div className="events-empty"><h3>{t.logsEmptyHeading}</h3><p>{t.logsEmptyBody}</p></div>;
  const referenced = new Set(referencedIDs ?? []);
  return (
    <ul className="log-list">
      {entries.map((entry) => {
        const isReferenced = referenced.has(entry.id);
        return (
          <li key={entry.id} className={isReferenced ? "log-entry--referenced" : undefined} aria-current={isReferenced ? "true" : undefined}>
            <div>
              <time>{new Date(entry.ts).toLocaleString(intlTag)}</time>
              <span>{entry.source}</span>
              {entry.unit && <span>{entry.unit}</span>}
              {isReferenced && <span className="log-referenced-badge">{t.logsReferencedLine}</span>}
            </div>
            <pre>{entry.message}</pre>
          </li>
        );
      })}
    </ul>
  );
}
