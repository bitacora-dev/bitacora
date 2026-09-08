import { useCallback, useEffect, useMemo, useState } from "react";
import QRCode from "qrcode";
import { claimPairing, fetchEventHistory, fetchHosts, fetchInventory, fetchLogHistory, fetchSummary, getDeviceToken, setDeviceToken, startPairing, type BitacoraEvent, type Host, type Inventory, type LogEntry, type SeriesPoint, type Summary } from "./api";
import TimeSeriesChart from "./components/TimeSeriesChart";
import EventsList from "./components/EventsList";
import LogsList from "./components/LogsList";
import AddServerPanel from "./components/AddServerPanel";
import InventoryPanel from "./components/InventoryPanel";
import JobsList from "./components/JobsList";
import { formatBytes } from "./bytes";
import { useTranslation } from "./i18n";

const POLL_INTERVAL_MS = 10_000;
const CPU_Y_RANGE: [number, number] = [0, 1];

function hostIDFromURL(): string {
  return new URLSearchParams(window.location.search).get("host_id") ?? "";
}

function pairCodeFromURL(): string | null {
  return new URLSearchParams(window.location.search).get("pair");
}

function viewFromURL(): "summary" | "events" | "logs" {
  const view = new URLSearchParams(window.location.search).get("view");
  return view === "events" || view === "logs" ? view : "summary";
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
  const [disks, setDisks] = useState<Inventory | null>(null);
  const [updates, setUpdates] = useState<Inventory | null>(null);

  const [token, setToken] = useState<string | null>(getDeviceToken);
  const [claimingFromURL, setClaimingFromURL] = useState(() => pairCodeFromURL() !== null);
  const [pairError, setPairError] = useState<string | null>(null);
  const [bootstrapping, setBootstrapping] = useState(false);

  const [pairPanel, setPairPanel] = useState<PairPanelData | null>(null);
  const [pairPanelError, setPairPanelError] = useState<string | null>(null);
  const [pairPanelOpen, setPairPanelOpen] = useState(false);
  const [addServerOpen, setAddServerOpen] = useState(false);
  const [hostIDCopyStatus, setHostIDCopyStatus] = useState<"idle" | "copied" | "failed">("idle");
  const [view, setView] = useState<"summary" | "events" | "logs">(viewFromURL);
  const [historyEvents, setHistoryEvents] = useState<BitacoraEvent[]>([]);
  const [historyTotal, setHistoryTotal] = useState(0);
  const [historyError, setHistoryError] = useState<string | null>(null);
  const [historyOffset, setHistoryOffset] = useState(0);
  const [historySeverity, setHistorySeverity] = useState<BitacoraEvent["severity"] | "">("");
  const [historyType, setHistoryType] = useState("");
  const [historyFrom, setHistoryFrom] = useState(() => new Date(Date.now() - 24 * 60 * 60 * 1000).toISOString().slice(0, 16));
  const [historyTo, setHistoryTo] = useState(() => new Date().toISOString().slice(0, 16));
  const [logEntries, setLogEntries] = useState<LogEntry[]>([]);
  const [logTotal, setLogTotal] = useState(0);
  const [logError, setLogError] = useState<string | null>(null);
  const [logOffset, setLogOffset] = useState(0);
  const [logText, setLogText] = useState("");
  const [logSource, setLogSource] = useState("");
  const [logUnit, setLogUnit] = useState("");
  const [logFrom, setLogFrom] = useState(() => new Date(Date.now() - 24 * 60 * 60 * 1000).toISOString().slice(0, 16));
  const [logTo, setLogTo] = useState(() => new Date().toISOString().slice(0, 16));

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
  const bytesPerSecond = useCallback((value: number) => t.bytesPerSecond(formatBytes(value, intlTag)), [intlTag, t]);
  const selectedHost = hosts.find((host) => host.id === hostID);
  const hostName = selectedHost?.name || selectedHost?.hostname || hostID;

  const copyHostID = async () => {
    try {
      await navigator.clipboard.writeText(hostID);
      setHostIDCopyStatus("copied");
    } catch {
      setHostIDCopyStatus("failed");
    }
  };

  const goToHost = (value: string) => {
    const url = new URL(window.location.href);
    url.searchParams.set("host_id", value);
    window.history.replaceState(null, "", url);
    setHostID(value);
    setHostIDCopyStatus("idle");
    setAddServerOpen(false);
  };

  const goToView = (next: "summary" | "events" | "logs") => {
    const url = new URL(window.location.href);
    if (next === "summary") url.searchParams.delete("view"); else url.searchParams.set("view", next);
    window.history.pushState(null, "", url);
    setView(next);
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
    if (!hostID || !token || view !== "events") return;
    const from = new Date(historyFrom).toISOString();
    const to = new Date(historyTo).toISOString();
    fetchEventHistory(hostID, { from, to, severity: historySeverity, type: historyType, limit: 50, offset: historyOffset })
      .then((page) => { setHistoryEvents(page.events); setHistoryTotal(page.total); setHistoryError(null); })
      .catch((err) => setHistoryError(err instanceof Error ? err.message : String(err)));
  }, [hostID, token, view, historyFrom, historyTo, historySeverity, historyType, historyOffset]);

  useEffect(() => {
    if (!hostID || !token || view !== "logs") return;
    fetchLogHistory(hostID, { from: new Date(logFrom).toISOString(), to: new Date(logTo).toISOString(), text: logText, source: logSource, unit: logUnit, limit: 50, offset: logOffset })
      .then((page) => { setLogEntries(page.entries); setLogTotal(page.total); setLogError(null); })
      .catch((err) => setLogError(err instanceof Error ? err.message : String(err)));
  }, [hostID, token, view, logFrom, logTo, logText, logSource, logUnit, logOffset]);

  useEffect(() => {
    if (!hostID || !token) return;

    let cancelled = false;
    const poll = async () => {
      try {
        const [nextDisks, nextUpdates] = await Promise.all([
          fetchInventory(hostID, "disk"),
          fetchInventory(hostID, "package_update"),
        ]);
        if (!cancelled) {
          setDisks(nextDisks);
          setUpdates(nextUpdates);
        }
      } catch {
        // Inventory is optional. Keep the latest readable snapshot while a
        // collector or its dedicated endpoint is temporarily unavailable.
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
    <main className="dashboard-shell dashboard-shell--tall-portrait">
      <header className="dashboard-header">
        <div>
          <h1>{t.brand}</h1>
          <p>{t.dashboardSubtitle}</p>
          <div className="host-identity">
            <strong>{hostName}</strong>
            <span className="host-id">{hostID}</span>
            <button type="button" onClick={copyHostID} className="host-id-copy-button">
              {hostIDCopyStatus === "copied" ? t.hostIdCopied : hostIDCopyStatus === "failed" ? t.hostIdCopyFailed : t.copyHostId}
            </button>
            <span className="sr-only" aria-live="polite">
              {hostIDCopyStatus === "copied" ? t.hostIdCopied : hostIDCopyStatus === "failed" ? t.hostIdCopyFailed : ""}
            </span>
          </div>
        </div>
        <div className="header-actions">
          {hosts.length > 1 ? (
            <select aria-label={t.hostSelectorLabel} value={hostID} onChange={(event) => goToHost(event.target.value)}>
              {hosts.map((host) => <option key={host.id} value={host.id}>{host.name || host.hostname || host.id}</option>)}
            </select>
          ) : null}
          {summary && (
            <dl className="dashboard-metadata">
              <div>
                <dt>{t.windowMetadataLabel}</dt>
                <dd>{t.windowLabel(windowMinutes)}</dd>
              </div>
              <div>
                <dt>{t.updatedAtLabel}</dt>
                <dd>{generatedAt || t.noSamples}</dd>
              </div>
            </dl>
          )}
          <button type="button" onClick={() => setAddServerOpen((open) => !open)} className="link-button">
            {t.addServerButton}
          </button>
          <button type="button" onClick={openPairPanel} className="link-button">
            {t.addDeviceButton}
          </button>
          <button type="button" onClick={() => goToView("events")} className="link-button">{t.eventsHistoryButton}</button>
          <button type="button" onClick={() => goToView("logs")} className="link-button">{t.logsHistoryButton}</button>
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

      {view === "events" ? (
        <article className="control-panel events-history-panel">
          <div className="panel-title-row"><h2>{t.eventsHistoryHeading}</h2><button type="button" onClick={() => goToView("summary")} className="link-button">{t.dashboardButton}</button></div>
          <p>{t.eventsHistoryIntro}</p>
          <p className="events-retention-notice">{t.eventsRetentionNotice}</p>
          <div className="events-history-filters">
            <label>{t.eventsFromLabel}<input type="datetime-local" value={historyFrom} onChange={(e) => { setHistoryOffset(0); setHistoryFrom(e.target.value); }} /></label>
            <label>{t.eventsToLabel}<input type="datetime-local" value={historyTo} onChange={(e) => { setHistoryOffset(0); setHistoryTo(e.target.value); }} /></label>
            <label>{t.eventsSeverityLabel}<select value={historySeverity} onChange={(e) => { setHistoryOffset(0); setHistorySeverity(e.target.value as BitacoraEvent["severity"] | ""); }}><option value="">{t.eventsAnySeverity}</option>{Object.entries(t.severity).map(([value, label]) => <option key={value} value={value}>{label}</option>)}</select></label>
            <label>{t.eventsTypeLabel}<input value={historyType} onChange={(e) => { setHistoryOffset(0); setHistoryType(e.target.value); }} /></label>
          </div>
          {historyError && <div className="error-panel">{t.hubUnreachable(historyError)}</div>}
          <EventsList events={historyEvents} emptyHeading={t.eventsHistoryEmptyHeading} emptyBody={t.eventsHistoryEmptyBody} />
          <div className="events-history-pagination"><button type="button" className="link-button" disabled={historyOffset === 0} onClick={() => setHistoryOffset((offset) => Math.max(0, offset - 50))}>{t.eventsPreviousPage}</button><span>{t.eventsPage(historyTotal === 0 ? 0 : historyOffset + 1, Math.min(historyOffset + historyEvents.length, historyTotal), historyTotal)}</span><button type="button" className="link-button" disabled={historyOffset + historyEvents.length >= historyTotal} onClick={() => setHistoryOffset((offset) => offset + 50)}>{t.eventsNextPage}</button></div>
        </article>
      ) : view === "logs" ? (
        <article className="control-panel events-history-panel">
          <div className="panel-title-row"><h2>{t.logsHistoryHeading}</h2><button type="button" onClick={() => goToView("summary")} className="link-button">{t.dashboardButton}</button></div>
          <p>{t.logsHistoryIntro}</p><p className="events-retention-notice">{t.logsRetentionNotice}</p>
          <div className="events-history-filters">
            <label>{t.logsFromLabel}<input type="datetime-local" value={logFrom} onChange={(e) => { setLogOffset(0); setLogFrom(e.target.value); }} /></label>
            <label>{t.logsToLabel}<input type="datetime-local" value={logTo} onChange={(e) => { setLogOffset(0); setLogTo(e.target.value); }} /></label>
            <label>{t.logsTextLabel}<input value={logText} onChange={(e) => { setLogOffset(0); setLogText(e.target.value); }} /></label>
            <label>{t.logsSourceLabel}<input value={logSource} onChange={(e) => { setLogOffset(0); setLogSource(e.target.value); }} /></label>
            <label>{t.logsUnitLabel}<input value={logUnit} onChange={(e) => { setLogOffset(0); setLogUnit(e.target.value); }} /></label>
          </div>
          {logError && <div className="error-panel">{t.hubUnreachable(logError)}</div>}
          <LogsList entries={logEntries} />
          <div className="events-history-pagination"><button type="button" className="link-button" disabled={logOffset === 0} onClick={() => setLogOffset((offset) => Math.max(0, offset - 50))}>{t.eventsPreviousPage}</button><span>{t.eventsPage(logTotal === 0 ? 0 : logOffset + 1, Math.min(logOffset + logEntries.length, logTotal), logTotal)}</span><button type="button" className="link-button" disabled={logOffset + logEntries.length >= logTotal} onClick={() => setLogOffset((offset) => offset + 50)}>{t.eventsNextPage}</button></div>
        </article>
      ) : summary && (
        <>
          <section className="metrics-grid">
            <TimeSeriesChart
              title={t.cpuUsageTitle}
              points={summary.cpu}
              color="#38bdf8"
              yRange={CPU_Y_RANGE}
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
            <TimeSeriesChart
              title={t.networkTrafficTitle}
              series={[
                { name: t.networkReceiveLabel, points: summary.network_rx_bytes_per_second, color: "#38bdf8", describePoint: (point) => ({ primary: bytesPerSecond(point.value) }) },
                { name: t.networkTransmitLabel, points: summary.network_tx_bytes_per_second, color: "#4ade80", describePoint: (point) => ({ primary: bytesPerSecond(point.value) }) },
              ]}
              formatAxisValue={bytesPerSecond}
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

            <article className="control-panel events-panel">
              <div className="panel-title-row"><h2>{t.jobsHeading(windowMinutes)}</h2><span>{summary.jobs.length}</span></div>
              <JobsList jobs={summary.jobs} />
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

          <section className="inventory-grid" aria-label={t.inventorySectionLabel}>
            <InventoryPanel inventory={disks} kind="disk" />
            <InventoryPanel inventory={updates} kind="package_update" />
          </section>
        </>
      )}

      {!summary && !error && <p className="loading-text">{t.loading}</p>}
    </main>
  );
}
