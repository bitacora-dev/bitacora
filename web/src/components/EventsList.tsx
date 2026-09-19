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

interface Props {
  events: BitacoraEvent[];
  emptyHeading?: string;
  emptyBody?: string;
}

const MAX_EVENT_ATTRIBUTES = 4;
const MAX_EVENT_KEY_LENGTH = 64;
const MAX_EVENT_VALUE_LENGTH = 160;
const SUMMARY_ATTRIBUTES = ["data_name", "reason"];

export interface EventDetail {
  key: string;
  value: string;
}

function safeEventValue(value: unknown): string {
  const normalized = String(value).replace(/\s+/g, " ").trim();
  return normalized.length > MAX_EVENT_VALUE_LENGTH
    ? `${normalized.slice(0, MAX_EVENT_VALUE_LENGTH - 1)}…`
    : normalized;
}

function safeEventKey(key: string): string {
  const normalized = key.replace(/\s+/g, " ").trim();
  return normalized.length > MAX_EVENT_KEY_LENGTH ? `${normalized.slice(0, MAX_EVENT_KEY_LENGTH - 1)}…` : normalized;
}

export function eventDetails(event: BitacoraEvent): EventDetail[] {
  const attrs = Object.entries(event.attrs ?? {})
    .filter(([, value]) => value !== null && value !== undefined)
    .sort(([left], [right]) => {
      const leftPriority = SUMMARY_ATTRIBUTES.indexOf(left);
      const rightPriority = SUMMARY_ATTRIBUTES.indexOf(right);
      if (leftPriority !== -1 || rightPriority !== -1) {
        return (leftPriority === -1 ? SUMMARY_ATTRIBUTES.length : leftPriority) - (rightPriority === -1 ? SUMMARY_ATTRIBUTES.length : rightPriority);
      }
      return left.localeCompare(right);
    })
    .slice(0, MAX_EVENT_ATTRIBUTES)
    .map(([key, value]) => ({ key: safeEventKey(key), value: safeEventValue(value) }));

  const subject = event.subject
    ? ([
        ["subject.kind", event.subject.kind],
        ["subject.name", event.subject.name],
        ["subject.pid", event.subject.pid],
      ] as const)
        .filter(([, value]) => value !== undefined && value !== "")
        .map(([key, value]) => ({ key, value: safeEventValue(value) }))
    : [];

  return [...attrs, ...subject];
}

export default function EventsList({ events, emptyHeading, emptyBody }: Props) {
  const { t, intlTag } = useTranslation();

  if (events.length === 0) {
    return (
      <div className="events-empty">
        <h3>{emptyHeading ?? t.eventsEmptyHeading}</h3>
        <p>{emptyBody ?? t.eventsEmptyBody}</p>
      </div>
    );
  }

  // Most recent first.
  const sorted = [...events].sort((a, b) => b.ts.localeCompare(a.ts));

  return (
    <ul className="event-list">
      {sorted.map((e) => {
        const details = eventDetails(e);
        const summary = details.slice(0, 2);
        return (
          <li key={e.id}>
            <div>
              <span>{new Date(e.ts).toLocaleTimeString(intlTag)}</span>
              <span className={SEVERITY_COLOR[e.severity]}>{t.severity[e.severity]}</span>
              <span>{e.type}</span>
            </div>
            <p>{e.title}</p>
            {details.length > 0 && (
              <details className="event-details">
                <summary>
                  {t.eventDetails}
                  <span>{summary.map(({ key, value }) => `${key}: ${value}`).join(" · ")}</span>
                </summary>
                <dl>
                  {details.map(({ key, value }, index) => (
                    <div key={`${key}-${index}`}>
                      <dt>{key}</dt>
                      <dd>{value}</dd>
                    </div>
                  ))}
                </dl>
              </details>
            )}
          </li>
        );
      })}
    </ul>
  );
}
