import type { CSSProperties } from "react";
import type { Inventory } from "../api";
import type { Disk, DiskGroup, DiskUsage } from "../disks";
import { groupDisks, isNearlyFull, sumUsage } from "../disks";
import { formatBytes } from "../bytes";
import { useTranslation } from "../i18n";

// The disks panel as an array table rather than one card per mountpoint
// (ADR-0016 asks for "a row per disk"): device, state, temperature, SMART
// verdict and usage, grouped by the disk's function in its array with a
// usage summary per group. The grouping and the totals live in ../disks so
// they can be tested without rendering anything.

export default function DiskArrayPanel({ inventory }: { inventory: Inventory | null }) {
  const { t, intlTag } = useTranslation();
  const groups = inventory ? groupDisks(inventory.items) : [];
  const devices = groups.flatMap((group) => group.disks);
  const totalUsage = sumUsage(devices);

  return (
    <article className="control-panel inventory-panel disk-array-panel">
      <div className="panel-title-row">
        <div>
          <h2>{t.disksHeading}</h2>
          {inventory && <p className="inventory-reported">{t.inventoryReportedAt(new Date(inventory.reported_at).toLocaleString(intlTag))}</p>}
        </div>
        <div className="inventory-title-metrics">
          {totalUsage && <UsageRing usage={totalUsage} label={t.disksUsageSummary} intlTag={intlTag} />}
          <span className="inventory-count">{devices.length}</span>
        </div>
      </div>
      {!inventory ? (
        <p className="inventory-empty">{t.inventoryPending}</p>
      ) : devices.length === 0 ? (
        <p className="inventory-empty">{t.disksEmpty}</p>
      ) : (
        groups.map((group) => <DiskGroupTable key={group.key} group={group} intlTag={intlTag} />)
      )}
    </article>
  );
}

function DiskGroupTable({ group, intlTag }: { group: DiskGroup; intlTag: string }) {
  const { t } = useTranslation();
  const degraded = group.arrayHealth === "degraded";

  return (
    <section className="disk-group">
      <header className="disk-group-head">
        <h3>{t.diskGroupLabel(group.key)}</h3>
        {group.arrayType && (
          <span className={degraded ? "array-badge array-badge--degraded" : "array-badge"}>
            {t.diskArrayMembership(t.diskArrayType(group.arrayType), group.arrayLevel ?? "", group.arrayMemberCount ?? "")}
            {/* Spelled out, not only coloured: a red border alone says
                nothing to a reader who cannot tell it apart. `unknown` is
                left unsaid because the badge never claimed a verdict. */}
            {group.arrayHealth && group.arrayHealth !== "unknown" && ` · ${t.diskArrayHealth[group.arrayHealth]}`}
          </span>
        )}
        <p className="disk-group-summary">
          {group.total
            ? t.diskGroupSummary(
                formatBytes(group.total.used, intlTag),
                formatBytes(group.total.capacity, intlTag),
                formatPercentage(group.total.ratio, intlTag),
              )
            : t.diskUsageUnavailable}
        </p>
      </header>
      <table className="disk-table">
        <colgroup>
          <col className="disk-col-device" />
          <col className="disk-col-state" />
          <col className="disk-col-temperature" />
          <col className="disk-col-smart" />
          <col className="disk-col-usage" />
        </colgroup>
        <thead>
          <tr>
            <th scope="col">{t.diskTableHeading.device}</th>
            <th scope="col">{t.diskTableHeading.state}</th>
            <th scope="col">{t.diskTableHeading.temperature}</th>
            <th scope="col">{t.diskTableHeading.smart}</th>
            <th scope="col">{t.diskTableHeading.usage}</th>
          </tr>
        </thead>
        <tbody>
          {group.disks.map((disk) => (
            <DiskTableRow key={disk.key} disk={disk} intlTag={intlTag} />
          ))}
        </tbody>
      </table>
    </section>
  );
}

function DiskTableRow({ disk, intlTag }: { disk: Disk; intlTag: string }) {
  const { t } = useTranslation();
  const identity = [disk.model, disk.serial].filter((value): value is string => value !== null);

  return (
    <tr className={disk.state === "critical" ? "disk-table-row disk-table-row--critical" : "disk-table-row"}>
      <th scope="row" className="disk-cell-device">
        <strong>{disk.device ?? t.diskValueUnknown}</strong>
        {identity.length > 0 && <span className="disk-cell-identity">{identity.join(" · ")}</span>}
        <DiskMounts disk={disk} intlTag={intlTag} />
      </th>
      <td className="disk-cell-state" data-label={t.diskTableHeading.state}>
        <span className={`disk-status-light disk-status-light--${disk.state}`} aria-hidden="true" />
        {t.diskStateLabel[disk.state]}
      </td>
      <td className="disk-cell-numeric" data-label={t.diskTableHeading.temperature}>
        {disk.temperatureCelsius === null ? t.diskValueUnknown : t.diskTemperatureValue(String(disk.temperatureCelsius))}
      </td>
      <td className="disk-cell-smart" data-label={t.diskTableHeading.smart}>{disk.smartStatus === null ? t.diskValueUnknown : t.diskSmartVerdict[disk.smartStatus]}</td>
      <td className="disk-cell-usage">
        {disk.usage ? <UsageBar usage={disk.usage} label={disk.device ?? disk.key} intlTag={intlTag} /> : <span className="disk-usage-unavailable">{t.diskUsageUnavailable}</span>}
      </td>
    </tr>
  );
}

// One device can carry several mountpoints — a root device commonly carries
// `/`, `/tmp` and `/var/tmp` at once. They fold into a disclosure so the
// table stays a table instead of growing a row per mount.
function DiskMounts({ disk, intlTag }: { disk: Disk; intlTag: string }) {
  const { t } = useTranslation();
  if (disk.mounts.length === 1) {
    const [mount] = disk.mounts;
    return <span className="disk-cell-mounts">{mount.fstype ? `${mount.mountpoint} · ${mount.fstype}` : mount.mountpoint}</span>;
  }

  return (
    <details className="disk-mounts">
      <summary>{t.diskMountpointsSummary(String(disk.mounts.length))}</summary>
      <ul>
        {disk.mounts.map((mount) => (
          <li key={mount.mountpoint}>
            <code>{mount.mountpoint}</code>
            {mount.fstype && <span>{mount.fstype}</span>}
            {mount.usage && mount.usage.available !== null && <span>{t.diskAvailableLabel(formatBytes(mount.usage.available, intlTag))}</span>}
          </li>
        ))}
      </ul>
    </details>
  );
}

function UsageBar({ usage, label, intlTag }: { usage: DiskUsage; label: string; intlTag: string }) {
  const { t } = useTranslation();
  const percentage = formatPercentage(usage.ratio, intlTag);
  return (
    <div className="disk-usage-cell">
      <div className="disk-usage-cell-values">
        <b>{percentage}</b>
        <span>
          {formatBytes(usage.used, intlTag)}
          {t.diskUsedLabel} {t.diskCapacityLabel(formatBytes(usage.capacity, intlTag))}
        </span>
      </div>
      <div className={isNearlyFull(usage) ? "usage-bar usage-bar--nearly-full" : "usage-bar"} role="img" aria-label={t.diskUsagePercentage(label, percentage)}>
        <span style={{ width: `${usage.ratio * 100}%` }} />
      </div>
    </div>
  );
}

function UsageRing({ usage, label, intlTag }: { usage: DiskUsage; label: string; intlTag: string }) {
  const { t } = useTranslation();
  // Whole percent only: the ring is sized to hold "100 %" and a decimal
  // place would push past it. The per-disk bars still carry the decimal.
  const percentage = formatPercentage(usage.ratio, intlTag, 0);
  return (
    <div
      className="usage-ring usage-ring--compact"
      style={{ "--usage": `${usage.ratio * 100}%` } as CSSProperties}
      role="img"
      aria-label={t.diskUsagePercentage(label, percentage)}
    >
      <span>{percentage}</span>
    </div>
  );
}

function formatPercentage(ratio: number, intlTag: string, maximumFractionDigits = 1) {
  return new Intl.NumberFormat(intlTag, { style: "percent", maximumFractionDigits }).format(ratio);
}
