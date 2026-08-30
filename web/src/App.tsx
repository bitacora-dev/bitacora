import { useEffect, useState } from "react";
import QRCode from "qrcode";
import { claimPairing, fetchSummary, getDeviceToken, setDeviceToken, startPairing, type Summary } from "./api";
import TimeSeriesChart from "./components/TimeSeriesChart";
import EventsList from "./components/EventsList";

const POLL_INTERVAL_MS = 10_000;

function hostIDFromURL(): string {
  return new URLSearchParams(window.location.search).get("host_id") ?? "";
}

function pairCodeFromURL(): string | null {
  return new URLSearchParams(window.location.search).get("pair");
}

function stripPairParam(): void {
  const url = new URL(window.location.href);
  url.searchParams.delete("pair");
  window.history.replaceState(null, "", url);
}

const pct = (v: number) => `${Math.round(v * 100)}%`;

interface PairPanelData {
  url: string;
  qr: string;
  expiresAt: string;
}

export default function App() {
  const [hostID, setHostID] = useState(hostIDFromURL);
  const [summary, setSummary] = useState<Summary | null>(null);
  const [error, setError] = useState<string | null>(null);

  // Device pairing: gates the whole app behind a claimed token, per
  // ADR-0014's device-token design.
  const [token, setToken] = useState<string | null>(getDeviceToken);
  const [claimingFromURL, setClaimingFromURL] = useState(() => pairCodeFromURL() !== null);
  const [pairError, setPairError] = useState<string | null>(null);
  const [bootstrapping, setBootstrapping] = useState(false);

  const [pairPanel, setPairPanel] = useState<PairPanelData | null>(null);
  const [pairPanelError, setPairPanelError] = useState<string | null>(null);
  const [pairPanelOpen, setPairPanelOpen] = useState(false);

  useEffect(() => {
    const code = pairCodeFromURL();
    if (!code) return;

    claimPairing(code)
      .then(({ token: claimed }) => {
        setDeviceToken(claimed);
        setToken(claimed);
        stripPairParam();
      })
      .catch((err) => {
        setPairError(err instanceof Error ? err.message : String(err));
        stripPairParam();
      })
      .finally(() => setClaimingFromURL(false));
  }, []);

  useEffect(() => {
    if (!hostID || !token) return;

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
  }, [hostID, token]);

  const bootstrapPairing = async () => {
    setBootstrapping(true);
    setPairError(null);
    try {
      const { code } = await startPairing();
      const { token: claimed } = await claimPairing(code);
      setDeviceToken(claimed);
      setToken(claimed);
    } catch (err) {
      setPairError(err instanceof Error ? err.message : String(err));
    } finally {
      setBootstrapping(false);
    }
  };

  const openPairPanel = async () => {
    setPairPanelOpen(true);
    setPairPanelError(null);
    setPairPanel(null);
    try {
      const { pair_path, expires_at } = await startPairing();
      const url = `${window.location.origin}${pair_path}`;
      const qr = await QRCode.toDataURL(url);
      setPairPanel({ url, qr, expiresAt: expires_at });
    } catch (err) {
      setPairPanelError(err instanceof Error ? err.message : String(err));
    }
  };

  if (claimingFromURL) {
    return (
      <div className="min-h-screen flex items-center justify-center p-4">
        <p className="text-neutral-500 text-sm">Pairing device…</p>
      </div>
    );
  }

  if (!token) {
    return (
      <div className="min-h-screen flex items-center justify-center p-4 pb-[env(safe-area-inset-bottom)] pl-[env(safe-area-inset-left)] pr-[env(safe-area-inset-right)]">
        <div className="flex flex-col gap-3 w-full max-w-xs">
          <h1 className="text-lg font-semibold text-center">Bitácora</h1>
          <p className="text-sm text-neutral-400 text-center">This device isn't paired yet.</p>
          {pairError && (
            <div className="bg-red-950 border border-red-800 text-red-300 text-sm rounded p-3">{pairError}</div>
          )}
          <button
            type="button"
            onClick={bootstrapPairing}
            disabled={bootstrapping}
            className="bg-sky-600 hover:bg-sky-500 disabled:opacity-50 rounded px-3 py-3"
          >
            {bootstrapping ? "Pairing…" : "Pair this device"}
          </button>
        </div>
      </div>
    );
  }

  if (!hostID) {
    return (
      <div className="min-h-screen flex items-center justify-center p-4 pb-[env(safe-area-inset-bottom)] pl-[env(safe-area-inset-left)] pr-[env(safe-area-inset-right)]">
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
            className="bg-neutral-900 border border-neutral-700 rounded px-3 py-3"
            placeholder="01J8XQ..."
            autoFocus
          />
          <button type="submit" className="bg-sky-600 hover:bg-sky-500 rounded px-3 py-3">
            View
          </button>
        </form>
      </div>
    );
  }

  return (
    <div className="min-h-screen p-4 pb-[env(safe-area-inset-bottom)] pl-[max(1rem,env(safe-area-inset-left))] pr-[max(1rem,env(safe-area-inset-right))] max-w-2xl mx-auto flex flex-col gap-6">
      <header className="flex items-baseline justify-between gap-2">
        <h1 className="text-lg font-semibold shrink-0">Bitácora</h1>
        <div className="flex items-center gap-2 min-w-0">
          <span className="text-sm text-neutral-500 truncate max-w-[40vw]">{hostID}</span>
          <button
            type="button"
            onClick={openPairPanel}
            className="text-sm text-sky-400 hover:text-sky-300 shrink-0 py-2 px-1"
          >
            Add device
          </button>
        </div>
      </header>

      {pairPanelOpen && (
        <div className="bg-neutral-900 border border-neutral-700 rounded p-4 flex flex-col gap-3">
          <div className="flex items-center justify-between">
            <h2 className="text-sm font-medium">Pair a new device</h2>
            <button
              type="button"
              onClick={() => setPairPanelOpen(false)}
              className="text-neutral-500 hover:text-neutral-300 py-2 px-2 -mr-2"
              aria-label="Close"
            >
              ✕
            </button>
          </div>
          {pairPanelError && (
            <div className="bg-red-950 border border-red-800 text-red-300 text-sm rounded p-3">{pairPanelError}</div>
          )}
          {!pairPanel && !pairPanelError && <p className="text-neutral-500 text-sm">Generating code…</p>}
          {pairPanel && (
            <div className="flex flex-col items-center gap-2">
              <img src={pairPanel.qr} alt="Pairing QR code" className="w-48 h-48 bg-white rounded" />
              <p className="text-xs text-neutral-500">
                Expires at {new Date(pairPanel.expiresAt).toLocaleTimeString()}
              </p>
              <p className="text-xs text-neutral-400 break-all select-all text-center">{pairPanel.url}</p>
            </div>
          )}
        </div>
      )}

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
