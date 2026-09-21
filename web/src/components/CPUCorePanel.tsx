import { useEffect, useState, type CSSProperties } from "react";
import type { CPUSeries, Inventory, InventoryItem, SeriesPoint } from "../api";
import { useTranslation } from "../i18n";

const CPU_PANEL_PREFERENCES_KEY = "bitacora_cpu_panel_preferences";
export const CPU_AVERAGING_WINDOWS = [30, 60, 300, 900] as const;

// isolated lists the logical CPUs the kernel reports in its authoritative
// isolcpus set. It stays empty when the kernel exposes no such list, which is
// a different state from "nothing is isolated" and must not be drawn as one.
interface CoreGroup { id: string; type: string; online: boolean; cpus: CPUSeries[]; isolated: string[]; }
export interface CPUAggregate { ts: string; mean: number; max: number; count: number; }
export interface CPUPanelPreferences { expanded: boolean; averagingWindowSeconds: typeof CPU_AVERAGING_WINDOWS[number]; }
const defaultPreferences: CPUPanelPreferences = { expanded: false, averagingWindowSeconds: 60 };

function cpuNumber(cpu: string): number {
  const value = Number.parseInt(cpu, 10);
  return Number.isNaN(value) ? Number.MAX_SAFE_INTEGER : value;
}

function topologyByCPU(inventory: Inventory | null): Map<string, InventoryItem> {
  const topology = new Map<string, InventoryItem>();
  for (const item of inventory?.items ?? []) {
    const cpu = item.id.match(/^cpu(\d+)$/)?.[1];
    if (cpu !== undefined) topology.set(cpu, item);
  }
  return topology;
}

export function groupCPUCores(series: CPUSeries[], inventory: Inventory | null): CoreGroup[] {
  const topology = topologyByCPU(inventory);
  const groups = new Map<string, CoreGroup>();
  for (const cpu of series) {
    const item = topology.get(cpu.cpu);
    const coreID = item?.attrs.core_id;
    const key = coreID === undefined ? `cpu-${cpu.cpu}` : `core-${coreID}`;
    const group = groups.get(key) ?? { id: coreID ?? cpu.cpu, type: item?.attrs.core_type ?? "unknown", online: item?.attrs.online !== "false", cpus: [], isolated: [] };
    group.online = group.online && item?.attrs.online !== "false";
    if (group.type === "unknown" && item?.attrs.core_type) group.type = item.attrs.core_type;
    if (item?.attrs.isolated === "true") group.isolated.push(cpu.cpu);
    group.cpus.push(cpu);
    groups.set(key, group);
  }
  return [...groups.values()]
    .map((group) => ({ ...group, cpus: group.cpus.sort((left, right) => cpuNumber(left.cpu) - cpuNumber(right.cpu)) }))
    .sort((left, right) => Number(left.id) - Number(right.id));
}

// How many logical CPUs the kernel has reserved. The panel is collapsed by
// default, so this is the number that has to survive into the header: a core
// held back by isolcpus reads as permanently idle without it.
export function isolatedCPUCount(groups: { isolated: string[] }[]): number {
  return groups.reduce((total, group) => total + group.isolated.length, 0);
}

export function aggregateCPUPoints(points: SeriesPoint[], intervalSeconds: number): CPUAggregate[] {
  const buckets = new Map<number, { total: number; max: number; count: number }>();
  for (const point of points) {
    const timestamp = new Date(point.ts).getTime();
    if (!Number.isFinite(timestamp) || !Number.isFinite(point.value)) continue;
    const bucket = Math.floor(timestamp / 1000 / intervalSeconds) * intervalSeconds;
    const current = buckets.get(bucket) ?? { total: 0, max: Number.NEGATIVE_INFINITY, count: 0 };
    current.total += point.value;
    current.max = Math.max(current.max, point.value);
    current.count += 1;
    buckets.set(bucket, current);
  }
  return [...buckets.entries()].sort(([left], [right]) => left - right).map(([bucket, value]) => ({
    ts: new Date(bucket * 1000).toISOString(), mean: value.total / value.count, max: value.max, count: value.count,
  }));
}

export function readCPUPanelPreferences(storage?: Storage | null): CPUPanelPreferences {
  try {
    const target = storage === undefined && typeof window !== "undefined" ? window.localStorage : storage;
    const value = target?.getItem(CPU_PANEL_PREFERENCES_KEY);
    if (!value) return defaultPreferences;
    const parsed: unknown = JSON.parse(value);
    if (typeof parsed !== "object" || parsed === null) return defaultPreferences;
    const candidate = parsed as Partial<CPUPanelPreferences>;
    if (typeof candidate.expanded !== "boolean" || !CPU_AVERAGING_WINDOWS.includes(candidate.averagingWindowSeconds as typeof CPU_AVERAGING_WINDOWS[number])) return defaultPreferences;
    return { expanded: candidate.expanded, averagingWindowSeconds: candidate.averagingWindowSeconds as typeof CPU_AVERAGING_WINDOWS[number] };
  } catch { return defaultPreferences; }
}

function saveCPUPanelPreferences(preferences: CPUPanelPreferences): void {
  try { window.localStorage.setItem(CPU_PANEL_PREFERENCES_KEY, JSON.stringify(preferences)); } catch {
    // Storage can be unavailable in private browsing or restricted embeds.
  }
}

function latestAggregate(series: CPUSeries, intervalSeconds: number): CPUAggregate | null {
  const aggregates = aggregateCPUPoints(series.points, intervalSeconds);
  return aggregates[aggregates.length - 1] ?? null;
}

function severity(max: number): "low" | "moderate" | "high" | "critical" {
  if (max >= 0.9) return "critical";
  if (max >= 0.75) return "high";
  if (max >= 0.5) return "moderate";
  return "low";
}

interface Props { cores: CPUSeries[]; topology: Inventory | null; identity: Inventory | null; }

export default function CPUCorePanel({ cores, topology, identity }: Props) {
  const { t, intlTag } = useTranslation();
  const [preferences, setPreferences] = useState(readCPUPanelPreferences);
  const groups = groupCPUCores(cores, topology);
  const isolatedCount = isolatedCPUCount(groups);
  const system = identity?.items.find((item) => item.id === "system");
  const model = system?.attrs.cpu_model;
  const power = Number(system?.attrs.cpu_power_watts);
  const hasPower = Number.isFinite(power);
  const percentage = new Intl.NumberFormat(intlTag, { style: "percent", minimumFractionDigits: 0, maximumFractionDigits: 1 });
  const detailsID = "cpu-core-details";

  useEffect(() => { saveCPUPanelPreferences(preferences); }, [preferences]);

  return <article className="control-panel cpu-core-panel">
    <div className="panel-title-row cpu-core-panel-header">
      <div><h2>{t.cpuCoresTitle}</h2>{model && <p className="cpu-core-model">{model}</p>}</div>
      <div className="cpu-core-header-meta"><span className="cpu-temperature-slot" aria-hidden="true" />{isolatedCount > 0 && <span className="cpu-core-isolated">{t.cpuIsolatedCount(isolatedCount)}</span>}{hasPower && <span className="cpu-core-power">{t.cpuPowerWatts(new Intl.NumberFormat(intlTag, { maximumFractionDigits: 1 }).format(power))}</span>}</div>
    </div>
    {groups.length === 0 ? <p className="cpu-core-empty">{t.cpuCoresPending}</p> : <>
      <div className="cpu-core-controls">
        <label><span>{t.cpuAveragingLabel}</span><select value={preferences.averagingWindowSeconds} onChange={(event) => setPreferences((current) => ({ ...current, averagingWindowSeconds: Number(event.target.value) as typeof CPU_AVERAGING_WINDOWS[number] }))}>
          {CPU_AVERAGING_WINDOWS.map((seconds) => <option key={seconds} value={seconds}>{t.cpuAveragingWindow(seconds)}</option>)}
        </select></label>
        <button type="button" className="cpu-details-toggle" aria-expanded={preferences.expanded} aria-controls={detailsID} onClick={() => setPreferences((current) => ({ ...current, expanded: !current.expanded }))}>{preferences.expanded ? t.cpuDetailsHide : t.cpuDetailsShow}</button>
      </div>
      {preferences.expanded && <div className="cpu-core-grid" id={detailsID} aria-label={t.cpuCoresTitle}>
        {groups.map((group) => <section className="cpu-core" key={group.id}>
          <div className="cpu-core-heading"><strong>{t.cpuCoreLabel(group.id)}</strong>{group.type !== "unknown" && <span>{t.cpuCoreType(group.type)}</span>}{!group.online && <span>{t.cpuOffline}</span>}{group.isolated.length === group.cpus.length && <span>{t.cpuIsolated}</span>}</div>
          <div className="cpu-thread-list">{group.cpus.map((cpu) => {
            const aggregate = latestAggregate(cpu, preferences.averagingWindowSeconds);
            const isolated = group.isolated.includes(cpu.cpu);
            const mean = aggregate?.mean ?? null;
            const peak = aggregate?.max ?? null;
            const peakWidth = Math.max(0, Math.min(1, peak ?? 0)) * 100;
            return <div className="cpu-thread" key={cpu.cpu}>
              <span>{t.cpuThreadLabel(cpu.cpu)}</span>
              <div className={`cpu-thread-meter cpu-thread-meter--${peak === null ? "empty" : severity(peak)}`} aria-label={mean === null || peak === null ? t.noSamples : t.cpuThreadUsage(cpu.cpu, percentage.format(mean), percentage.format(peak))}>
                <span style={{ width: `${Math.max(0, Math.min(1, mean ?? 0)) * 100}%` }} />
                {peak !== null && <i className="cpu-thread-peak" style={{ "--cpu-peak": `${peakWidth}%` } as CSSProperties} aria-hidden="true" />}
              </div>
              <strong>{mean === null ? t.noSamples : percentage.format(mean)}</strong>
              {peak !== null && <small>{t.cpuPeakLabel(percentage.format(peak))}</small>}
              {isolated && <small className="cpu-thread-isolated"><span className="sr-only">{t.cpuIsolatedThread(cpu.cpu)}</span><span aria-hidden="true">{t.cpuIsolated}</span></small>}
            </div>;
          })}</div>
        </section>)}
      </div>}
    </>}
  </article>;
}
