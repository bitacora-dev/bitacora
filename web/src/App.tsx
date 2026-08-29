import { useEffect, useState } from "react";
import { fetchSummary, type Summary } from "./api";
import TimeSeriesChart from "./components/TimeSeriesChart";
import EventsList from "./components/EventsList";

const POLL_INTERVAL_MS = 10_000;

function hostIDFromURL(): string {
  return new URLSearchParams(window.location.search).get("host_id") ?? "";
}

const pct = (v: number) => `${Math.round(v * 100)}%`;

export default function App() {
  const [hostID, setHostID] = useState(hostIDFromURL);
  const [summary, setSummary] = useState<Summary | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!hostID) return;

    let cancelled = false;

    const poll = async () => {
      try {
        const s = await fetchSummary(hostID);
        if (!cancelled) {
          setSummary(s);
          setError(null);
        }
      } catch (err) {
        if (!cancelled) setError(err instanceof Error ? err.message : String(err));
      }
    };

    poll();
    const id = setInterval(poll, POLL_INTERVAL_MS);
    return () => {
      cancelled = true;
      clearInterval(id);
    };
  }, [hostID]);

  if (!hostID) {
    return (
      <div className="min-h-screen flex items-center justify-center p-4">
        <form
          className="flex flex-col gap-3 w-full max-w-xs"
          onSubmit={(e) => {
            e.preventDefault();
            const value = (e.currentTarget.elements.namedItem("host_id") as HTMLInputElement).value.trim();
            if (!value) return;
            const url = new URL(window.location.href);
            url.searchParams.set("host_id", value);
            window.history.replaceState(null, "", url);
            setHostID(value);
          }}
        >
          <label className="text-sm text-neutral-400" htmlFor="host_id">
            Host ID
          </label>
          <input
            id="host_id"
            name="host_id"
            className="bg-neutral-900 border border-neutral-700 rounded px-3 py-2"
            placeholder="01J8XQ..."
            autoFocus
          />
          <button type="submit" className="bg-sky-600 hover:bg-sky-500 rounded px-3 py-2">
            View
          </button>
        </form>
      </div>
    );
  }

  return (
    <div className="min-h-screen p-4 max-w-2xl mx-auto flex flex-col gap-6">
      <header className="flex items-baseline justify-between">
        <h1 className="text-lg font-semibold">Bitácora</h1>
        <span className="text-sm text-neutral-500 truncate max-w-[50%]">{hostID}</span>
      </header>

      {error && (
        <div className="bg-red-950 border border-red-800 text-red-300 text-sm rounded p-3">
          Couldn't reach the hub: {error}
        </div>
      )}

      {summary && (
        <>
          <section className="flex flex-col gap-4">
            <TimeSeriesChart title="CPU usage" points={summary.cpu} color="#38bdf8" formatValue={pct} />
            <TimeSeriesChart title="Memory used" points={summary.memory} color="#a78bfa" formatValue={pct} />
          </section>

          <section>
            <h2 className="text-sm font-medium text-neutral-400 mb-2">
              Events (last {Math.round(summary.window_secs / 60)}m)
            </h2>
            <EventsList events={summary.events} />
          </section>
        </>
      )}

      {!summary && !error && <p className="text-neutral-500 text-sm">Loading…</p>}
    </div>
  );
}
