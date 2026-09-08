import type { LogEntry } from "../api";
import { useTranslation } from "../i18n";

export default function LogsList({ entries }: { entries: LogEntry[] }) {
  const { t, intlTag } = useTranslation();
  if (entries.length === 0) return <div className="events-empty"><h3>{t.logsEmptyHeading}</h3><p>{t.logsEmptyBody}</p></div>;
  return <ul className="log-list">{entries.map((entry) => <li key={entry.id}><div><time>{new Date(entry.ts).toLocaleString(intlTag)}</time><span>{entry.source}</span>{entry.unit && <span>{entry.unit}</span>}</div><pre>{entry.message}</pre></li>)}</ul>;
}
