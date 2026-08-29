import type { BitacoraEvent } from "../api";

const SEVERITY_COLOR: Record<BitacoraEvent["severity"], string> = {
  debug: "text-neutral-500",
  info: "text-sky-400",
  notice: "text-teal-400",
  warn: "text-amber-400",
  error: "text-red-400",
  critical: "text-red-300 font-semibold",
};

export default function EventsList({ events }: { events: BitacoraEvent[] }) {
  if (events.length === 0) {
    return <p className="text-neutral-500 text-sm">No events in this window.</p>;
  }

  // Most recent first.
  const sorted = [...events].sort((a, b) => b.ts.localeCompare(a.ts));

  return (
    <ul className="divide-y divide-neutral-800">
      {sorted.map((e) => (
        <li key={e.id} className="py-2 flex flex-col gap-0.5 min-w-0">
          <div className="flex flex-wrap items-center gap-x-2 gap-y-0.5 text-xs text-neutral-500">
            <span className="shrink-0">{new Date(e.ts).toLocaleTimeString()}</span>
            <span className={SEVERITY_COLOR[e.severity]}>{e.severity}</span>
            <span className="text-neutral-600 truncate">{e.type}</span>
          </div>
          <div className="text-sm break-words">{e.title}</div>
        </li>
      ))}
    </ul>
  );
}
