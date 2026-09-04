import { useCallback, useEffect, useMemo, useState } from "react";
import QRCode from "qrcode";
import { claimPairing, fetchHosts, fetchSummary, getDeviceToken, setDeviceToken, startPairing, type Host, type SeriesPoint, type Summary } from "./api";
import TimeSeriesChart from "./components/TimeSeriesChart";
import EventsList from "./components/EventsList";
import AddServerPanel from "./components/AddServerPanel";
import { useTranslation } from "./i18n";

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

const formatRatio = (v: number, locale: string) =>
  new Intl.NumberFormat(locale, {
    style: "percent",
    minimumFractionDigits: 1,
    maximumFractionDigits: 1,
  }).format(v);

const formatBytes = (value: number, locale: string) => {
  const units = ["B", "KB", "MB", "GB", "TB"];
  let size = value;
  let unitIndex = 0;
  while (size >= 1024 && unitIndex < units.length - 1) {
    size /= 1024;
    unitIndex += 1;
  }
  const amount = new Intl.NumberFormat(locale, {
    maximumFractionDigits: size >= 10 || unitIndex === 0 ? 0 : 1,
  }).format(size);
  return `${amount} ${units[unitIndex]}`;
};

function latest(points: SeriesPoint[]): SeriesPoint | null {
  return points.length > 0 ? points[points.length - 1] : null;
}

interface PairPanelData {
  url: string;
  qr: string;
  expiresAt: string;
}

export default function App() {
  const { t, intlTag } = useTranslation();
  const [hostID, setHostID] = useState(hostIDFromURL);
  const [summary, setSummary] = useState<Summary | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [hosts, setHosts] = useState<Host[]>([]);

  const [token, setToken] = useState<string | null>(getDeviceToken);
  const [claimingFromURL, setClaimingFromURL] = useState(() => pairCodeFromURL() !== null);
  const [pairError, setPairError] = useState<string | null>(null);
  const [bootstrapping, setBootstrapping] = useState(false);

  const [pairPanel, setPairPanel] = useState<PairPanelData | null>(null);
  const [pairPanelError, setPairPanelError] = useState<string | null>(null);
  const [pairPanelOpen, setPairPanelOpen] = useState(false);
  const [addServerOpen, setAddServerOpen] = useState(false);

  const memoryTotalByTS = useMemo(() => {
    const byTS = new Map<string, number>();
    for (const point of summary?.memory_total_bytes ?? []) byTS.set(point.ts, point.value);
    return byTS;
  }, [summary?.memory_total_bytes]);

  const memoryAvailableByTS = useMemo(() => {
    const byTS = new Map<string, number>();
    for (const point of summary?.memory_available_bytes ?? []) byTS.set(point.ts, point.value);
    return byTS;
  }, [summary?.memory_available_bytes]);

  const memoryAvailable = latest(summary?.memory_available_bytes ?? []);
  const swapFree = latest(summary?.memory_swap_free_bytes ?? []);
  const swapTotal = latest(summary?.memory_swap_total_bytes ?? []);
  const generatedAt = summary ? new Date(summary.generated_at).toLocaleTimeString(intlTag) : "";
  const windowMinutes = summary ? Math.round(summary.window_secs / 60) : 0;
  const ratio = useCallback((value: number) => formatRatio(value, intlTag), [intlTag]);
  const bytes = useCallback((value: number) => formatBytes(value, intlTag), [intlTag]);

  const goToHost = (value: string) => {
    const url = new URL(window.location.href);
    url.searchParams.set("host_id", value);
    window.history.replaceState(null, "", url);
    setHostID(value);
    setAddServerOpen(false);
  };

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

  useEffect(() => {
    if (!token) return;
    fetchHosts().then(setHosts).catch(() => setHosts([]));
  }, [token]);

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
      <main className="auth-shell">
        <p>{t.pairingDevice}</p>
      </main>
    );
  }

  if (!token) {
    return (
      <main className="auth-shell">
        <section className="auth-panel">
          <h1>{t.brand}</h1>
          <p>{t.notPaired}</p>
          {pairError && <div className="error-panel">{pairError}</div>}
          <button type="button" onClick={bootstrapPairing} disabled={bootstrapping} className="primary-button">
            {bootstrapping ? t.pairButtonPending : t.pairButton}
          </button>
        </section>
      </main>
    );
  }

  if (!hostID) {
    return (
      <main className="auth-shell">
        <section className="auth-panel auth-panel-wide">
          <h1>{t.brand}</h1>
          {hosts.length > 1 && (
            <select aria-label={t.hostSelectorLabel} value="" onChange={(event) => event.target.value && goToHost(event.target.value)}>
              <option value="">{t.hostSelectorLabel}</option>
              {hosts.map((host) => <option key={host.id} value={host.id}>{host.name || host.hostname || host.id}</option>)}
            </select>
          )}
          <form
            className="host-form"
            onSubmit={(e) => {
              e.preventDefault();
              const value = (e.currentTarget.elements.namedItem("host_id") as HTMLInputElement).value.trim();
              if (!value) return;
              goToHost(value);
            }}
          >
            <label htmlFor="host_id">{t.hostIdLabel}</label>
            <input id="host_id" name="host_id" placeholder={t.hostIdPlaceholder} autoFocus />
            <button type="submit" className="primary-button">
              {t.viewButton}
            </button>
          </form>

          {addServerOpen ? (
            <AddServerPanel onClose={() => setAddServerOpen(false)} onViewHost={goToHost} />
          ) : (
            <button type="button" onClick={() => setAddServerOpen(true)} className="link-button">
              {t.addServerButton}
            </button>
          )}
        </section>
      </main>
    );
  }

  return (
    <main className="dashboard-shell">
      <header className="dashboard-header">
        <div>
          <h1>{t.brand}</h1>
          <p>{t.dashboardSubtitle}</p>
        </div>
        <div className="header-actions">
          {hosts.length > 1 ? (
            <select aria-label={t.hostSelectorLabel} value={hostID} onChange={(event) => goToHost(event.target.value)}>
              {hosts.map((host) => <option key={host.id} value={host.id}>{host.name || host.hostname || host.id}</option>)}
            </select>
          ) : <span title={hostID}>{hostID}</span>}
          <button type="button" onClick={() => setAddServerOpen((open) => !open)} className="link-button">
            {t.addServerButton}
          </button>
          <button type="button" onClick={openPairPanel} className="link-button">
            {t.addDeviceButton}
          </button>
        </div>
      </header>

      {addServerOpen && <AddServerPanel onClose={() => setAddServerOpen(false)} onViewHost={goToHost} />}

      {pairPanelOpen && (
        <section className="control-panel pair-panel">
          <div className="panel-title-row">
            <h2>{t.pairNewDeviceHeading}</h2>
            <button type="button" onClick={() => setPairPanelOpen(false)} className="icon-button" aria-label={t.closeAria} />
          </div>
          {pairPanelError && <div className="error-panel">{pairPanelError}</div>}
          {!pairPanel && !pairPanelError && <p className="muted-text">{t.generatingCode}</p>}
          {pairPanel && (
            <div className="qr-layout">
              <img src={pairPanel.qr} alt={t.qrAlt} />
              <p>{t.expiresAt(new Date(pairPanel.expiresAt).toLocaleTimeString(intlTag))}</p>
              <p>{pairPanel.url}</p>
            </div>
          )}
        </section>
      )}

      {error && <div className="error-panel">{t.hubUnreachable(error)}</div>}

      {summary && (
        <>
          <section className="status-strip" aria-label={t.dashboardSubtitle}>
            <article>
              <span>{t.cpuStatusLabel}</span>
              <strong>{latest(summary.cpu) ? ratio(latest(summary.cpu)?.value ?? 0) : t.noSamples}</strong>
            </article>
            <article>
              <span>{t.memoryStatusLabel}</span>
              <strong>
                {latest(summary.memory_used_bytes) && latest(summary.memory_total_bytes)
                  ? t.memoryOfTotal(bytes(latest(summary.memory_used_bytes)?.value ?? 0), bytes(latest(summary.memory_total_bytes)?.value ?? 0))
                  : t.noSamples}
              </strong>
            </article>
            <article>
              <span>{t.windowLabel(windowMinutes)}</span>
              <strong>{generatedAt ? t.updatedAt(generatedAt) : t.noSamples}</strong>
            </article>
          </section>

          <section className="metrics-grid">
            <TimeSeriesChart
              title={t.cpuUsageTitle}
              points={summary.cpu}
              color="#38bdf8"
              formatAxisValue={ratio}
              describePoint={(point) => ({ primary: ratio(point.value) })}
            />
            <TimeSeriesChart
              title={t.memoryUsedTitle}
              points={summary.memory_used_bytes.length > 0 ? summary.memory_used_bytes : summary.memory}
              color="#f8d66d"
              formatAxisValue={(value) => (summary.memory_used_bytes.length > 0 ? bytes(value) : ratio(value))}
              describePoint={(point) => {
                if (summary.memory_used_bytes.length === 0) return { primary: ratio(point.value) };
                const total = memoryTotalByTS.get(point.ts) ?? latest(summary.memory_total_bytes)?.value;
                const available = memoryAvailableByTS.get(point.ts) ?? memoryAvailable?.value;
                return {
                  primary: total ? t.memoryOfTotal(bytes(point.value), bytes(total)) : bytes(point.value),
                  secondary: available ? t.memoryAvailable(bytes(available)) : undefined,
                };
              }}
            />
          </section>

          <section className="lower-grid">
            <article className="control-panel events-panel">
              <div className="panel-title-row">
                <h2>{t.eventsHeading(windowMinutes)}</h2>
                <span>{summary.events.length}</span>
              </div>
              <EventsList events={summary.events} />
            </article>

            <article className="control-panel signal-panel">
              <h2>{t.collectorStateHeading}</h2>
              <p>{t.collectorStateIntro}</p>
              <dl>
                <div>
                  <dt>{t.collectorCoreLabel}</dt>
                  <dd>{t.collectorCoreValue}</dd>
                </div>
                <div>
                  <dt>{t.collectorOptionalLabel}</dt>
                  <dd>{t.collectorOptionalValue}</dd>
                </div>
                {swapFree && swapTotal && (
                  <div>
                    <dt>{t.memoryUsedTitle}</dt>
                    <dd>{t.swapFree(bytes(swapFree.value), bytes(swapTotal.value))}</dd>
                  </div>
                )}
              </dl>
            </article>
          </section>
        </>
      )}

      {!summary && !error && <p className="loading-text">{t.loading}</p>}
    </main>
  );
}
