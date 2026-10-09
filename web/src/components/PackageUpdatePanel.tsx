import { useEffect, useMemo, useRef, useState } from "react";
import { confirmAction, fetchJob, issueActionToken, type ActionToken, type Inventory, type InventoryItem, type JobOutputLine, type PackageOperation } from "../api";
import { useTranslation, type Dictionary } from "../i18n";
import { ageSeconds, formatAge } from "../relativeTime";

const POLL_INTERVAL_MS = 3_000;
const DEFAULT_MAX_CACHE_AGE_SECONDS = 24 * 60 * 60;

// pkgupdates runs every 6 hours (cmd/bitacora-agent/main.go), so an inventory
// that is a few hours old is normal, not late. The threshold sits half a cycle
// past the cadence: at exactly 6 hours the next cycle simply has not run yet,
// and calling that stale would paint every healthy host gold.
const PACKAGE_INVENTORY_INTERVAL_SECONDS = 6 * 60 * 60;
export const INVENTORY_STALE_AFTER_SECONDS = PACKAGE_INVENTORY_INTERVAL_SECONDS * 1.5;

type Phase = "idle" | "confirming" | "pending" | "running" | "failed" | "refreshed" | "refresh_still_stale" | "complete";

interface Props {
  hostID: string;
  inventory: Inventory | null;
  secondFactorAvailable: boolean;
  onRefreshInventory: () => Promise<Inventory | null>;
}

function actionMetadata(inventory: Inventory | null) {
  return inventory?.items.find((item) => item.id === "package-actions")?.attrs ?? {};
}

function cacheAge(inventory: Inventory | null) {
  const value = inventory?.items.find((item) => item.attrs.cache_age_seconds !== undefined)?.attrs.cache_age_seconds;
  const age = Number(value);
  return Number.isFinite(age) && age >= 0 ? age : null;
}

// The panel used to print the report instant alone, in small muted type:
// "Reportado: 29/9/2026, 10:14:49". That is literal and still says nothing —
// an operator read a four-hour-old package list as the current one, because
// nothing on screen subtracted the two numbers for them. The age is the fact
// that decides whether the list can be trusted, so it is the fact that is
// shown; the exact instant stays one hover or one screen reader away.
export function inventoryIsStale(reportedAt: string | undefined | null, now = Date.now()): boolean {
  const age = ageSeconds(reportedAt, now);
  return age !== null && age > INVENTORY_STALE_AFTER_SECONDS;
}

// A successful apt update can still leave the reported age above the limit.
// The age no longer tracks per-source staleness — it tracks when apt last
// refreshed, so this means the inventory on screen predates the refresh, or
// nothing on the host recorded it (the update stamp ships with
// update-notifier-common, and an index nobody republished is never replaced).
// Return to the recoverable state so the stale explanation and refresh action
// remain available; never silently strand the operator in a success notice.
export function phaseAfterSuccessfulCacheRefresh(inventory: Inventory | null, maxAge: number): Phase {
  const age = cacheAge(inventory);
  return age !== null && age > maxAge ? "refresh_still_stale" : "refreshed";
}

// Every output line names the job it belongs to. A poll issued before the
// operator started another operation can still land after begin() cleared the
// buffer, so foreign lines are dropped instead of being appended to the wrong
// run. An unset job_id is treated as belonging to the polled job: that is what
// a hub older than this field sends.
export function linesForJob(jobID: string, lines: JobOutputLine[]): JobOutputLine[] {
  return lines.filter((line) => !line.job_id || line.job_id === jobID);
}

// A candidate from a NotAutomatic suite (Ubuntu's backports) is a real newer
// version, but `apt upgrade` never installs it. It is listed on its own and is
// neither counted as a pending update nor part of what "apply" is confirmed
// against; the agent only reports one when nothing apt would take is newer.
export function splitPackageItems(items: InventoryItem[]): { pending: InventoryItem[]; notAutomatic: InventoryItem[] } {
  const pending: InventoryItem[] = [];
  const notAutomatic: InventoryItem[] = [];
  for (const item of items) (item.attrs.candidate_automatic === "false" ? notAutomatic : pending).push(item);
  return { pending, notAutomatic };
}

// Inventory attributes are strings on the wire; a boolean one is shown as a
// translated yes/no, never as a raw "true"/"false".
export function packageAttributeValue(value: string, t: Pick<Dictionary, "inventoryBoolean">): string {
  if (value === "true") return t.inventoryBoolean(true);
  if (value === "false") return t.inventoryBoolean(false);
  return value;
}

function PackageItemList({ items, t }: { items: InventoryItem[]; t: Dictionary }) {
  return <ul className="inventory-list">{items.map((item) => <li key={item.id}><strong>{item.name}</strong><dl>{Object.entries(item.attrs).filter(([key]) => key !== "cache_age_seconds").map(([key, value]) => <div key={key}><dt>{t.inventoryAttribute(key)}</dt><dd>{packageAttributeValue(value, t)}</dd></div>)}</dl></li>)}</ul>;
}

export function packageActionVisibility(canRefresh: boolean, canApply: boolean, stale: boolean, phase: Phase) {
  return {
    showApply: canApply && !stale && (phase === "idle" || phase === "refreshed"),
    showRefresh: canRefresh && stale && (phase === "idle" || phase === "refresh_still_stale"),
  };
}

export default function PackageUpdatePanel({ hostID, inventory, secondFactorAvailable, onRefreshInventory }: Props) {
  const { t, intlTag } = useTranslation();
  const [phase, setPhase] = useState<Phase>("idle");
  const [operation, setOperation] = useState<PackageOperation | null>(null);
  const [issued, setIssued] = useState<ActionToken | null>(null);
  const [lines, setLines] = useState<JobOutputLine[]>([]);
  const [error, setError] = useState<string | null>(null);
  const afterRef = useRef(0);
  const metadata = actionMetadata(inventory);
  const age = cacheAge(inventory);
  const maxAge = Number(metadata.package_cache_max_age_seconds) || DEFAULT_MAX_CACHE_AGE_SECONDS;
  const stale = age !== null && age > maxAge;
  const canRefresh = metadata.refresh_package_cache === "true" && secondFactorAvailable;
  const canApply = metadata.apply_pending_package_updates === "true" && secondFactorAvailable;
  const { pending: packageItems, notAutomatic } = useMemo(() => splitPackageItems(inventory?.items.filter((item) => item.id !== "package-actions") ?? []), [inventory]);

  useEffect(() => {
    if (!issued || (phase !== "pending" && phase !== "running")) return;
    let cancelled = false;
    const poll = async () => {
      try {
        const result = await fetchJob(hostID, issued.request_id, afterRef.current);
        if (cancelled) return;
        afterRef.current = result.next_after;
        setLines((previous) => [...previous, ...linesForJob(issued.request_id, result.lines)]);
        if (!result.complete) {
          setPhase("running");
          return;
        }
        if (result.job.status === "success") {
          if (operation === "REFRESH_PACKAGE_CACHE") {
            const refreshedInventory = await onRefreshInventory();
            if (!cancelled) setPhase(phaseAfterSuccessfulCacheRefresh(refreshedInventory, maxAge));
          } else if (!cancelled) {
            setPhase("complete");
          }
        } else if (!cancelled) {
          setError(t.packageActionFailed);
          setPhase("failed");
        }
      } catch (err) {
        // A confirmed order has no Job until the disconnected host collects it.
        // This is an honest pending state, not an animation or inferred start.
        if (!cancelled) setPhase("pending");
      }
    };
    poll();
    const id = window.setInterval(poll, POLL_INTERVAL_MS);
    return () => { cancelled = true; window.clearInterval(id); };
  }, [hostID, issued, maxAge, operation, onRefreshInventory, phase, t.packageActionFailed]);

  const begin = async (next: PackageOperation) => {
    setError(null);
    setLines([]);
    afterRef.current = 0;
    try {
      setIssued(await issueActionToken(hostID, next));
      setOperation(next);
      setPhase("confirming");
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
      setPhase("failed");
    }
  };

  const confirm = async () => {
    if (!operation || !issued) return;
    try {
      await confirmAction(hostID, operation, issued);
      setPhase("pending");
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
      setPhase("failed");
    }
  };

  const { showApply, showRefresh } = packageActionVisibility(canRefresh, canApply, stale, phase);
  const reportedAt = inventory ? new Date(inventory.reported_at) : null;
  const reportedInstant = reportedAt && Number.isFinite(reportedAt.getTime()) ? reportedAt.toLocaleString(intlTag) : "";
  const reportedAge = inventory ? formatAge(inventory.reported_at, intlTag) : null;
  const inventoryStale = inventoryIsStale(inventory?.reported_at);

  return (
    <article className="control-panel package-update-panel">
      <div className="panel-title-row"><div><h2>{t.updatesHeading}</h2>{inventory && <p className={inventoryStale ? "inventory-reported inventory-reported--stale" : "inventory-reported"}>{reportedAge === null
        ? t.inventoryReportedAt(reportedInstant)
        : <time dateTime={reportedAt?.toISOString()} title={reportedInstant} aria-label={t.inventoryReportedAria(reportedInstant)}>{t.inventoryAge(reportedAge, inventoryStale)}</time>}</p>}</div><span className="inventory-count">{packageItems.length}</span></div>
      {!inventory ? <p className="inventory-empty">{t.inventoryPending}</p> : packageItems.length === 0 ? <p className="inventory-empty">{t.updatesEmpty}</p> : <PackageItemList items={packageItems} t={t} />}
      {notAutomatic.length > 0 && <section className="package-not-automatic" aria-labelledby="package-not-automatic-heading"><h3 id="package-not-automatic-heading">{t.updatesNotAutomaticHeading}</h3><PackageItemList items={notAutomatic} t={t} /></section>}
      {age !== null && <p className={stale ? "cache-age cache-age--stale" : "cache-age"}>{t.cacheAge(new Intl.NumberFormat(intlTag, { maximumFractionDigits: 1 }).format(age / 86400), stale)}</p>}
      <div className="package-action-controls">
        {showRefresh && <button type="button" className="primary-button package-action-button" onClick={() => begin("REFRESH_PACKAGE_CACHE")}>{t.refreshPackageCache}</button>}
        {showApply && <button type="button" className="primary-button package-action-button" onClick={() => begin("APPLY_PENDING_PACKAGE_UPDATES")}>{t.applyPackageUpdates}</button>}
      </div>
      {phase === "confirming" && operation && <section className="action-confirmation" aria-labelledby="action-confirmation-heading"><h3 id="action-confirmation-heading">{t.actionConfirmHeading}</h3><dl><div><dt>{t.actionHost}</dt><dd>{hostID}</dd></div><div><dt>{t.actionOperation}</dt><dd>{t.packageOperation(operation)}</dd></div><div><dt>{t.actionReason}</dt><dd>{stale ? t.actionReasonStaleCache : t.actionReasonApply}</dd></div><div><dt>{t.actionSnapshot}</dt><dd>{t.actionSnapshotCount(packageItems.length)}</dd></div><div><dt>{t.actionConsequences}</dt><dd>{operation === "REFRESH_PACKAGE_CACHE" ? t.refreshConsequences : t.applyConsequences}</dd></div></dl><div className="action-confirmation-controls"><button type="button" className="link-button" onClick={() => setPhase("idle")}>{t.actionCancel}</button><button type="button" className="primary-button package-action-button" onClick={confirm}>{t.actionConfirm}</button></div></section>}
      {(phase === "pending" || phase === "running") && <section className="action-progress" aria-live="polite"><strong>{phase === "pending" ? t.actionPending : t.actionRunning}</strong><p>{phase === "pending" ? t.actionPendingBody : t.actionRunningBody}</p>{lines.length > 0 && <pre>{lines.map((line) => line.message).join("\n")}</pre>}</section>}
      {phase === "refreshed" && <p className="action-notice">{t.refreshCompletedReviewPlan}</p>}
      {phase === "refresh_still_stale" && <section className="action-notice" aria-live="polite">{t.refreshCompletedStillStale}</section>}
      {phase === "complete" && <p className="action-notice">{t.packageActionComplete}</p>}
      {phase === "failed" && <section className="error-panel" aria-live="assertive"><strong>{t.packageActionFailed}</strong>{error && <pre>{error}</pre>}{lines.length > 0 && <pre>{lines.map((line) => line.message).join("\n")}</pre>}</section>}
    </article>
  );
}
