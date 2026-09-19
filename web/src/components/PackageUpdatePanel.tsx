import { useEffect, useMemo, useRef, useState } from "react";
import { confirmAction, fetchJob, issueActionToken, type ActionToken, type Inventory, type JobOutputLine, type PackageOperation } from "../api";
import { useTranslation } from "../i18n";

const POLL_INTERVAL_MS = 3_000;
const DEFAULT_MAX_CACHE_AGE_SECONDS = 24 * 60 * 60;

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

// A successful apt update can still leave one active source stale (for
// example, a repository that failed during a partial update). Return to the
// recoverable state so the stale explanation and refresh action remain
// available; never silently strand the operator in a success notice.
export function phaseAfterSuccessfulCacheRefresh(inventory: Inventory | null, maxAge: number): Phase {
  const age = cacheAge(inventory);
  return age !== null && age > maxAge ? "refresh_still_stale" : "refreshed";
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
  const packageItems = useMemo(() => inventory?.items.filter((item) => item.id !== "package-actions") ?? [], [inventory]);

  useEffect(() => {
    if (!issued || (phase !== "pending" && phase !== "running")) return;
    let cancelled = false;
    const poll = async () => {
      try {
        const result = await fetchJob(hostID, issued.request_id, afterRef.current);
        if (cancelled) return;
        afterRef.current = result.next_after;
        setLines((previous) => [...previous, ...result.lines]);
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

  return (
    <article className="control-panel package-update-panel">
      <div className="panel-title-row"><div><h2>{t.updatesHeading}</h2>{inventory && <p className="inventory-reported">{t.inventoryReportedAt(new Date(inventory.reported_at).toLocaleString(intlTag))}</p>}</div><span className="inventory-count">{packageItems.length}</span></div>
      {!inventory ? <p className="inventory-empty">{t.inventoryPending}</p> : packageItems.length === 0 ? <p className="inventory-empty">{t.updatesEmpty}</p> : <ul className="inventory-list">{packageItems.map((item) => <li key={item.id}><strong>{item.name}</strong><dl>{Object.entries(item.attrs).filter(([key]) => key !== "cache_age_seconds").map(([key, value]) => <div key={key}><dt>{t.inventoryAttribute(key)}</dt><dd>{value}</dd></div>)}</dl></li>)}</ul>}
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
