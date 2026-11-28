import { useEffect, useState } from "react";
import type { Host, Inventory } from "../api";
import { useTranslation } from "../i18n";
import { ageSeconds, formatAge } from "../relativeTime";
import { pendingUpdateItems } from "../overview";
import { PowerChip } from "./PowerPanel";

// Six collector cycles without a sample, the same threshold the processor
// panel uses. Past it the dot stops moving and turns gold: an animated dot
// over a silent agent asserts freshness the page cannot know.
export const HOST_BAR_STALE_AFTER_SECONDS = 60;

// The host bar is the dense overview's single line of identity and liveness:
// who this is, whether the page is still hearing from it, whether the power
// is holding, how many updates are waiting, and the ways out of this view.
//
// It replaces the old two-column dashboard header, which spent a 2.25rem
// brand lockup, a subtitle and a four-entry definition list on facts the
// operator reads once. Everything it dropped is either still here in one
// line or lives in the panel that owns it.

interface Props {
  hostID: string;
  host: Host | undefined;
  sampleAt: string | null;
  // Null until the first summary lands. The averaging window is the hub's
  // fact, so before it arrives the chip is absent rather than reading
  // "0 min window" — a window nothing reported.
  windowMinutes: number | null;
  // The agent's own last ingest, which is a different fact from the sample
  // instant above: the hub can be answering happily about a host that went
  // quiet ten minutes ago.
  lastSeenAt: string | null;
  hostStale: boolean;
  ups: Inventory | null;
  updates: Inventory | null;
  hostIDCopyStatus: "idle" | "copied" | "failed";
  onCopyHostID: () => void;
  onAddServer: () => void;
  onAddDevice: () => void;
  onShowEvents: () => void;
  onShowLogs: () => void;
  hosts: Host[];
  onSelectHost: (hostID: string) => void;
}

export default function HostBar({
  hostID,
  host,
  sampleAt,
  windowMinutes,
  lastSeenAt,
  hostStale,
  ups,
  updates,
  hostIDCopyStatus,
  onCopyHostID,
  onAddServer,
  onAddDevice,
  onShowEvents,
  onShowLogs,
  hosts,
  onSelectHost,
}: Props) {
  const { t, intlTag } = useTranslation();
  const name = host?.name || host?.hostname;
  const lastSeenAge = formatAge(lastSeenAt, intlTag);
  // Absent inventory is "the collector has not reported", which is not
  // "nothing is pending": the count only appears once a list exists.
  const pending = updates === null ? null : pendingUpdateItems(updates).length;

  return (
    <header className="host-bar">
      <div className="host-bar-identity">
        <img className="host-bar-mark" src="/bitacora-logo.png" alt="" aria-hidden="true" />
        <h1>{t.brand}</h1>
        {name && <strong className="host-bar-name">{name}</strong>}
        {hosts.length > 1 && (
          <select
            className="host-bar-select"
            aria-label={t.hostSelectorLabel}
            value={hostID}
            onChange={(event) => onSelectHost(event.target.value)}
          >
            {hosts.map((entry) => (
              <option key={entry.id} value={entry.id}>{entry.name || entry.hostname || entry.id}</option>
            ))}
          </select>
        )}
      </div>

      <div className="host-bar-state">
        <HostFreshness sampleAt={sampleAt} />
        {windowMinutes !== null && <span className="host-bar-window">{t.windowLabel(windowMinutes)}</span>}
        {/* The agent's own last ingest only earns a chip once it is late.
            While the host is reporting normally the sample age beside it
            already answers "is this page hearing from the server", and
            printing both filled a third of the bar with one fact said
            twice. When they diverge — a hub answering happily about a host
            that went quiet — the chip appears and says so in words. */}
        {hostStale && lastSeenAge !== null && (
          <span className="host-bar-lastseen host-bar-lastseen--stale">
            {t.hostBarLastSeen(lastSeenAge)}
          </span>
        )}
        <PowerChip inventory={ups} />
        {pending !== null && (
          <span className={pending > 0 ? "host-bar-updates host-bar-updates--pending" : "host-bar-updates"}>
            {t.hostBarUpdatesPending(pending)}
          </span>
        )}
        {host?.agent_version && <span className="host-bar-agent">{t.hostBarAgent(host.agent_version)}</span>}
      </div>

      <div className="host-bar-actions">
        <span className="host-bar-id">{hostID}</span>
        <button type="button" onClick={onCopyHostID} className="host-id-copy-button">
          {hostIDCopyStatus === "copied" ? t.hostIdCopied : hostIDCopyStatus === "failed" ? t.hostIdCopyFailed : t.copyHostId}
        </button>
        <span className="sr-only" aria-live="polite">
          {hostIDCopyStatus === "copied" ? t.hostIdCopied : hostIDCopyStatus === "failed" ? t.hostIdCopyFailed : ""}
        </span>
        <button type="button" onClick={onAddServer} className="link-button">{t.addServerButton}</button>
        <button type="button" onClick={onAddDevice} className="link-button">{t.addDeviceButton}</button>
        <button type="button" onClick={onShowEvents} className="link-button">{t.eventsHistoryButton}</button>
        <button type="button" onClick={onShowLogs} className="link-button">{t.logsHistoryButton}</button>
      </div>
    </header>
  );
}

// Owns its own one-second tick so only this line re-renders between polls,
// the same contract CPURefreshAge follows. The dot is keyed on the sample
// instant, which remounts it and restarts its one-shot animation: it beats
// once per arriving sample rather than looping forever, so motion means a
// sample actually landed instead of only meaning the page is open.
export function HostFreshness({ sampleAt }: { sampleAt: string | null }) {
  const { t, intlTag } = useTranslation();
  const [now, setNow] = useState(() => Date.now());

  useEffect(() => {
    const tick = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(tick);
  }, []);

  const age = formatAge(sampleAt, intlTag, now);
  const seconds = ageSeconds(sampleAt, now);
  const stale = seconds !== null && seconds > HOST_BAR_STALE_AFTER_SECONDS;

  return (
    <p className={stale ? "host-bar-freshness host-bar-freshness--stale" : "host-bar-freshness"}>
      {/* The beat means "a sample just landed", so with no sample it must not
          beat: an idle dot keeps the line's shape without asserting liveness
          the page has never observed. */}
      <span
        key={sampleAt ?? "none"}
        className={sampleAt === null ? "host-bar-pulse host-bar-pulse--idle" : "host-bar-pulse"}
        aria-hidden="true"
      />
      <span>{age === null ? t.noSamples : t.hostBarSampleAge(age)}</span>
      {stale && <span className="host-bar-freshness-note">{t.hostStale}</span>}
    </p>
  );
}
