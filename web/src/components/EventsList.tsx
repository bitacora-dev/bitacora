import { useEffect, useRef } from "react";
import { hasInstant, type BitacoraEvent } from "../api";
import { groupByDay } from "../dayGroups";
import { eventLogTarget, logEntryID, type LogRefTarget } from "../logrefs";
import { useTranslation } from "../i18n";
import RowInstant, { DayHeading } from "./RowInstant";

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
  // Present only where the log viewer can be reached. The summary panel and
  // the history panel both render events; only the dashboard can navigate.
  onShowLogs?: (event: BitacoraEvent, target: LogRefTarget) => void;
  // Caps the overview panel so one noisy hour cannot push the three columns
  // of the dense layout off the screen. The history view passes nothing and
  // keeps showing its full page.
  maxRows?: number;
  // Only the live overview highlights arrivals. In the history view the whole
  // set changes on every page turn, and flashing twenty rows at once would
  // mean "you paged", not "something happened".
  highlightNew?: boolean;
}

const MAX_EVENT_ATTRIBUTES = 4;
const MAX_EVENT_KEY_LENGTH = 64;
const MAX_EVENT_VALUE_LENGTH = 160;
const SUMMARY_ATTRIBUTES = ["data_name", "reason"];
const MAX_SHOWN_LOG_REFS = 3;

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

  // Provenance: when the hub received the event, the identity it deduplicates
  // on, and the log coordinates it came from. All three reach the browser and
  // all three used to stop at the type definition.
  const refs = event.log_refs ?? [];
  const shownRefs = refs.slice(0, MAX_SHOWN_LOG_REFS).map((ref) => logEntryID(ref.block_id, ref.line));
  const provenance = ([
    ["ts_received", hasInstant(event.ts_received) && event.ts_received !== event.ts ? event.ts_received : undefined],
    ["fingerprint", event.fingerprint],
    ["log_refs", shownRefs.length > 0 ? `${shownRefs.join(", ")}${refs.length > shownRefs.length ? ", …" : ""}` : undefined],
  ] as const)
    .filter(([, value]) => value !== undefined && value !== "")
    .map(([key, value]) => ({ key, value: safeEventValue(value) }));

  return [...attrs, ...subject, ...provenance];
}

// Tracks which event ids this list has already drawn, so an arrival can be
// marked without the hub having to tell us what is new.
//
// Nothing is highlighted on the first paint: every row would qualify, and a
// page that flashes its whole contents on load has said nothing. The set is
// reset to exactly what is on screen after each render, which is bounded and
// safe here because the newest-first list never re-admits a row it dropped.
export function useArrivedIDs(ids: string[], enabled: boolean): Set<string> {
  const seenRef = useRef<Set<string> | null>(null);
  const seen = seenRef.current;
  const arrived = !enabled || seen === null ? new Set<string>() : new Set(ids.filter((id) => !seen.has(id)));

  useEffect(() => {
    seenRef.current = new Set(ids);
  });

  return arrived;
}

export default function EventsList({ events, emptyHeading, emptyBody, onShowLogs, maxRows, highlightNew = false }: Props) {
  const { t } = useTranslation();

  // Most recent first, then split into one run per calendar day so the date
  // is stated once above each run instead of repeated on every row.
  const sorted = [...events].sort((a, b) => b.ts.localeCompare(a.ts));
  const shown = maxRows === undefined ? sorted : sorted.slice(0, maxRows);
  const arrived = useArrivedIDs(shown.map((event) => event.id), highlightNew);

  if (events.length === 0) {
    return (
      <div className="events-empty">
        <h3>{emptyHeading ?? t.eventsEmptyHeading}</h3>
        <p>{emptyBody ?? t.eventsEmptyBody}</p>
      </div>
    );
  }

  return (
    <div className="event-days">
      {groupByDay(shown, (e) => e.ts).map((day) => (
        <section className="event-day" key={day.key}>
          <DayHeading dayKey={day.key} />
          <ul className="event-list event-list--rows">
            {day.items.map((e) => {
              const details = eventDetails(e);
              const logTarget = onShowLogs ? eventLogTarget(e) : null;
              return (
                <li key={e.id} className={arrived.has(e.id) ? "event-row event-row--new" : "event-row"}>
                  {/* The whole row is the disclosure, so the operator opens
                      what they were already reading instead of hunting for a
                      separate "details" control underneath it. */}
                  <details>
                    <summary>
                      <RowInstant instant={e.ts} />
                      <span className={`event-severity ${SEVERITY_COLOR[e.severity]}`}>{t.severity[e.severity]}</span>
                      <span className="event-row-title">{e.title}</span>
                      <span className="event-row-type">{e.type}</span>
                    </summary>
                    <div className="event-row-body">
                      <p className="event-row-source">{t.eventSource(e.source)}</p>
                      {details.length > 0 && (
                        <dl>
                          {details.map(({ key, value }, index) => (
                            <div key={`${key}-${index}`}>
                              <dt>{key}</dt>
                              <dd>{value}</dd>
                            </div>
                          ))}
                        </dl>
                      )}
                      {logTarget && onShowLogs && (
                        <div className="event-actions">
                          <button type="button" className="link-button" aria-label={t.eventLogsAria(e.title)} onClick={() => onShowLogs(e, logTarget)}>
                            {t.eventLogsButton}
                          </button>
                        </div>
                      )}
                    </div>
                  </details>
                </li>
              );
            })}
          </ul>
        </section>
      ))}
    </div>
  );
}
