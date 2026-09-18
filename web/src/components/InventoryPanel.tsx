import type { Inventory } from "../api";
import type { CSSProperties } from "react";
import { formatBytes } from "../bytes";
import { useTranslation } from "../i18n";

interface InventoryPanelProps {
  inventory: Inventory | null;
  kind: "disk" | "package_update";
}

const diskAttributes = ["device", "model", "serial", "fstype", "capacity_bytes", "used_bytes", "available_bytes"] as const;
const updateAttributes = ["source", "installed_version", "candidate_version", "current_digest", "registry_digest", "arch", "repo", "cache_age_seconds"] as const;
const diskDetailAttributes = ["device", "model", "serial", "fstype"] as const;

export interface DiskUsage {
  capacity: number;
  used: number;
  available: number | null;
  ratio: number;
}

export function diskUsage(attrs: Record<string, string>): DiskUsage | null {
  const capacity = Number(attrs.capacity_bytes);
  const used = Number(attrs.used_bytes);
  if (!Number.isFinite(capacity) || !Number.isFinite(used) || capacity <= 0 || used < 0) return null;

  const available = Number(attrs.available_bytes);
  return {
    capacity,
    used,
    available: Number.isFinite(available) && available >= 0 ? available : null,
    ratio: Math.min(used / capacity, 1),
  };
}

function isNearlyFull(usage: DiskUsage) {
  return usage.ratio >= 0.9;
}

export default function InventoryPanel({ inventory, kind }: InventoryPanelProps) {
  const { t, intlTag } = useTranslation();
  const isDisk = kind === "disk";
  const title = isDisk ? t.disksHeading : t.updatesHeading;
  const attributes = isDisk ? diskAttributes : updateAttributes;
  const usages = isDisk && inventory ? inventory.items.map((item) => diskUsage(item.attrs)).filter((usage): usage is DiskUsage => usage !== null) : [];
  const totalUsage = usages.length
    ? diskUsage({
        capacity_bytes: String(usages.reduce((total, usage) => total + usage.capacity, 0)),
        used_bytes: String(usages.reduce((total, usage) => total + usage.used, 0)),
      })
    : null;

  return (
    <article className="control-panel inventory-panel">
        <div className="panel-title-row">
          <div>
            <h2>{title}</h2>
            {inventory && <p className="inventory-reported">{t.inventoryReportedAt(new Date(inventory.reported_at).toLocaleString(intlTag))}</p>}
          </div>
          <div className="inventory-title-metrics">
            {isDisk && totalUsage && <UsageRing usage={totalUsage} label={t.disksUsageSummary} intlTag={intlTag} compact />}
            <span className="inventory-count">{inventory?.items.length ?? 0}</span>
          </div>
      </div>
      {!inventory ? (
        <p className="inventory-empty">{t.inventoryPending}</p>
      ) : inventory.items.length === 0 ? (
        <p className="inventory-empty">{isDisk ? t.disksEmpty : t.updatesEmpty}</p>
      ) : (
        <ul className={isDisk ? "inventory-list disk-list" : "inventory-list"}>
          {inventory.items.map((item) =>
            isDisk ? (
              <DiskRow key={item.id} item={item} intlTag={intlTag} />
            ) : (
              <li key={item.id}>
                <strong>{item.name}</strong>
                <dl>
                  {attributes.map((attribute) => {
                    const value = item.attrs[attribute];
                    if (!value) return null;
                    return (
                      <div key={attribute}>
                        <dt>{t.inventoryAttribute(attribute)}</dt>
                        <dd>{attribute.endsWith("_bytes") ? formatBytes(value, intlTag) : value}</dd>
                      </div>
                    );
                  })}
                </dl>
              </li>
            ),
          )}
        </ul>
      )}
    </article>
  );
}

function DiskRow({ item, intlTag }: { item: Inventory["items"][number]; intlTag: string }) {
  const { t } = useTranslation();
  const usage = diskUsage(item.attrs);
  const health = item.attrs.array_health;
  const isDegraded = health === "degraded";

  return (
    <li className={isDegraded ? "disk-row disk-row--degraded" : "disk-row"}>
      <div className="disk-row-heading">
        <strong>{item.name}</strong>
        {item.attrs.array_type && (
          <span className={isDegraded ? "array-badge array-badge--degraded" : "array-badge"}>
            {t.diskArrayMembership(t.diskArrayType(item.attrs.array_type), item.attrs.array_level, item.attrs.array_member_count)}
          </span>
        )}
      </div>
      {health === "degraded" && <p className="disk-state disk-state--critical">{t.diskArrayHealth.degraded}</p>}
      {usage ? (
        <div className="disk-usage">
          <UsageRing usage={usage} label={item.name} intlTag={intlTag} />
          <div className="disk-usage-main">
            <div className="disk-usage-values">
              <span><b>{formatBytes(usage.used, intlTag)}</b>{t.diskUsedLabel}</span>
              <span>{t.diskCapacityLabel(formatBytes(usage.capacity, intlTag))}</span>
              {usage.available !== null && <span>{t.diskAvailableLabel(formatBytes(usage.available, intlTag))}</span>}
            </div>
            <div className={isNearlyFull(usage) ? "usage-bar usage-bar--nearly-full" : "usage-bar"} aria-hidden="true">
              <span style={{ width: `${usage.ratio * 100}%` }} />
            </div>
            {isNearlyFull(usage) && <p className="disk-state disk-state--warning">{t.diskNearlyFull}</p>}
          </div>
        </div>
      ) : (
        <p className="disk-usage-unavailable">{t.diskUsageUnavailable}</p>
      )}
      <dl className="disk-details">
        {diskDetailAttributes.map((attribute) => {
          const value = item.attrs[attribute];
          if (!value) return null;
          return <div key={attribute}><dt>{t.inventoryAttribute(attribute)}</dt><dd>{value}</dd></div>;
        })}
      </dl>
    </li>
  );
}

function UsageRing({ usage, label, intlTag, compact = false }: { usage: DiskUsage; label: string; intlTag: string; compact?: boolean }) {
  const { t } = useTranslation();
  const percentage = new Intl.NumberFormat(intlTag, { style: "percent", maximumFractionDigits: 0 }).format(usage.ratio);
  return (
    <div
      className={compact ? "usage-ring usage-ring--compact" : "usage-ring"}
      style={{ "--usage": `${usage.ratio * 100}%` } as CSSProperties}
      role="img"
      aria-label={t.diskUsagePercentage(label, percentage)}
    >
      <span>{percentage}</span>
    </div>
  );
}
