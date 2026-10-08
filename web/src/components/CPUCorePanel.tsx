import { useEffect, useState, type CSSProperties } from "react";
import type { CPUSeries, Inventory, InventoryItem, SeriesPoint, TemperatureSeries } from "../api";
import { useTranslation } from "../i18n";
import { ageSeconds, formatAge } from "../relativeTime";
import CPULoadSparkline from "./CPULoadSparkline";

const CPU_PANEL_PREFERENCES_KEY = "bitacora_cpu_panel_preferences";
export const CPU_AVERAGING_WINDOWS = [30, 60, 300, 900] as const;
export type CPUAveragingWindow = typeof CPU_AVERAGING_WINDOWS[number];

// The cpu collector runs every ten seconds and the dashboard polls on the same
// cadence, so a bar can only take a new real value once per poll. The width
// transition below interpolates between two measured samples; it never invents
// a value, and it is the whole reason the bars read as alive instead of
// snapping. Raising either cadence to animate would spend the agent's
// ADR-0024 budget on a visual effect.
export const CPU_METER_TRANSITION_MS = 1200;

// Six collector cycles without a sample. Past this the pulse stops and turns
// gold, matching the stale treatment the rest of the dashboard already uses:
// an animated dot over a silent agent asserts freshness the page cannot know.
export const CPU_REFRESH_STALE_AFTER_SECONDS = 60;

// isolated lists the logical CPUs the kernel reports in its authoritative
// isolcpus set. It stays empty when the kernel exposes no such list, which is
// a different state from "nothing is isolated" and must not be drawn as one.
//
// offline is tracked per thread for the same reason: a core whose second
// hyperthread was taken offline still runs on the first one, so dimming the
// whole row would claim the core is gone when it is not.
interface CoreGroup {
  id: string;
  type: string;
  online: boolean;
  cpus: CPUSeries[];
  isolated: string[];
  offline: string[];
}

export interface CPUWindowStats { mean: number; max: number; latest: number; count: number }
export interface CPUPanelPreferences { averagingWindowSeconds: CPUAveragingWindow }
const defaultPreferences: CPUPanelPreferences = { averagingWindowSeconds: 30 };

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
    const group = groups.get(key) ?? { id: coreID ?? cpu.cpu, type: item?.attrs.core_type ?? "unknown", online: item?.attrs.online !== "false", cpus: [], isolated: [], offline: [] };
    group.online = group.online && item?.attrs.online !== "false";
    if (group.type === "unknown" && item?.attrs.core_type) group.type = item.attrs.core_type;
    if (item?.attrs.isolated === "true") group.isolated.push(cpu.cpu);
    if (item?.attrs.online === "false") group.offline.push(cpu.cpu);
    group.cpus.push(cpu);
    groups.set(key, group);
  }
  return [...groups.values()]
    .map((group) => ({ ...group, cpus: group.cpus.sort((left, right) => cpuNumber(left.cpu) - cpuNumber(right.cpu)) }))
    .sort((left, right) => Number(left.id) - Number(right.id));
}

// How many logical CPUs the kernel has reserved. A core held back by isolcpus
// reads as permanently idle without the header saying so.
export function isolatedCPUCount(groups: { isolated: string[] }[]): number {
  return groups.reduce((total, group) => total + group.isolated.length, 0);
}

export function offlineCPUCount(groups: { offline: string[] }[]): number {
  return groups.reduce((total, group) => total + group.offline.length, 0);
}

// The averaging window is a trailing window over the newest sample, not a
// fixed epoch bucket. Bucketing by epoch made the selected "30 s average"
// collapse to a single sample for the first seconds of every bucket, so the
// bar jumped at each boundary and the label was wrong while it did.
export function windowStats(points: SeriesPoint[], windowSeconds: number): CPUWindowStats | null {
  const usable = points.filter((point) => Number.isFinite(point.value) && Number.isFinite(new Date(point.ts).getTime()));
  if (usable.length === 0) return null;
  const newest = usable.reduce((latest, point) => Math.max(latest, new Date(point.ts).getTime()), Number.NEGATIVE_INFINITY);
  const from = newest - windowSeconds * 1000;
  let total = 0;
  let max = Number.NEGATIVE_INFINITY;
  let count = 0;
  let latest = 0;
  for (const point of usable) {
    const timestamp = new Date(point.ts).getTime();
    if (timestamp < from) continue;
    total += point.value;
    max = Math.max(max, point.value);
    count += 1;
    if (timestamp === newest) latest = point.value;
  }
  if (count === 0) return null;
  return { mean: total / count, max, latest, count };
}

// Total load prefers the host-wide cpu series the hub already computes. The
// per-core mean is a fallback for a host that reports per-CPU usage but no
// aggregate, so the header is never blank while the rows below are full.
export function totalLoadSeries(total: SeriesPoint[], cores: CPUSeries[]): SeriesPoint[] {
  if (total.length > 0) return total;
  const sums = new Map<string, { total: number; count: number }>();
  for (const core of cores) {
    for (const point of core.points) {
      if (!Number.isFinite(point.value)) continue;
      const current = sums.get(point.ts) ?? { total: 0, count: 0 };
      current.total += point.value;
      current.count += 1;
      sums.set(point.ts, current);
    }
  }
  return [...sums.entries()]
    .sort(([left], [right]) => new Date(left).getTime() - new Date(right).getTime())
    .map(([ts, value]) => ({ ts, value: value.total / value.count }));
}

export interface CPUTemperatureReading { chip: string; sensor: string; value: number; package: boolean }

// The hwmon collector lowercases driver labels, so an Intel package sensor
// arrives as "package_id_0". The package reading is the one UnRAID shows; a
// host that only exposes per-core sensors gets the hottest core instead, and
// the readout says which it is rather than passing a core off as the package.
export function cpuTemperature(series: TemperatureSeries[]): CPUTemperatureReading | null {
  const readings: CPUTemperatureReading[] = [];
  for (const entry of series) {
    const point = entry.points[entry.points.length - 1];
    if (!point || !Number.isFinite(point.value)) continue;
    readings.push({ chip: entry.chip, sensor: entry.sensor, value: point.value, package: entry.sensor.startsWith("package") });
  }
  if (readings.length === 0) return null;
  const packages = readings.filter((reading) => reading.package);
  const candidates = packages.length > 0 ? packages : readings;
  return candidates.reduce((hottest, reading) => (reading.value > hottest.value ? reading : hottest));
}

export function readCPUPanelPreferences(storage?: Storage | null): CPUPanelPreferences {
  try {
    const target = storage === undefined && typeof window !== "undefined" ? window.localStorage : storage;
    const value = target?.getItem(CPU_PANEL_PREFERENCES_KEY);
    if (!value) return defaultPreferences;
    const parsed: unknown = JSON.parse(value);
    if (typeof parsed !== "object" || parsed === null) return defaultPreferences;
    const candidate = parsed as Partial<CPUPanelPreferences>;
    // Preferences stored by the previous card layout also carried `expanded`.
    // Unknown keys are ignored rather than rejected so the window a user
    // already chose survives the redesign.
    if (!CPU_AVERAGING_WINDOWS.includes(candidate.averagingWindowSeconds as CPUAveragingWindow)) return defaultPreferences;
    return { averagingWindowSeconds: candidate.averagingWindowSeconds as CPUAveragingWindow };
  } catch { return defaultPreferences; }
}

function saveCPUPanelPreferences(preferences: CPUPanelPreferences): void {
  try { window.localStorage.setItem(CPU_PANEL_PREFERENCES_KEY, JSON.stringify(preferences)); } catch {
    // Storage can be unavailable in private browsing or restricted embeds.
  }
}

export function severity(value: number): "low" | "moderate" | "high" | "critical" {
  if (value >= 0.9) return "critical";
  if (value >= 0.75) return "high";
  if (value >= 0.5) return "moderate";
  return "low";
}

function meterWidth(value: number): string {
  return `${Math.max(0, Math.min(1, value)) * 100}%`;
}

interface Props {
  cores: CPUSeries[];
  topology: Inventory | null;
  identity: Inventory | null;
  total: SeriesPoint[];
  temperatures: TemperatureSeries[];
  generatedAt: string | null;
}

export default function CPUCorePanel({ cores, topology, identity, total, temperatures, generatedAt }: Props) {
  const { t, intlTag } = useTranslation();
  const [preferences, setPreferences] = useState(readCPUPanelPreferences);
  const [now, setNow] = useState(() => Date.now());
  const groups = groupCPUCores(cores, topology);
  const isolatedCount = isolatedCPUCount(groups);
  const offlineCount = offlineCPUCount(groups);
  const system = identity?.items.find((item) => item.id === "system");
  const model = system?.attrs.cpu_model;
  const power = Number(system?.attrs.cpu_power_watts);
  const hasPower = system?.attrs.cpu_power_watts !== undefined && Number.isFinite(power);
  const temperature = cpuTemperature(temperatures);
  const totalPoints = totalLoadSeries(total, cores);
  const totalStats = windowStats(totalPoints, preferences.averagingWindowSeconds);
  const percentage = new Intl.NumberFormat(intlTag, { style: "percent", minimumFractionDigits: 0, maximumFractionDigits: 1 });
  const decimal = new Intl.NumberFormat(intlTag, { maximumFractionDigits: 1 });
  const refreshedAge = formatAge(generatedAt, intlTag, now);
  const refreshedSeconds = ageSeconds(generatedAt, now);
  const refreshStale = refreshedSeconds !== null && refreshedSeconds > CPU_REFRESH_STALE_AFTER_SECONDS;

  useEffect(() => { saveCPUPanelPreferences(preferences); }, [preferences]);

  // The agent's cadence is what it is; the one-second tick only keeps the
  // "updated N seconds ago" line honest between polls, so the operator can
  // tell a quiet host from a stalled page.
  useEffect(() => {
    const tick = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(tick);
  }, []);

  return <article className="control-panel cpu-core-panel">
    <div className="panel-title-row cpu-core-panel-header">
      <div className="cpu-core-identity">
        <h2>{t.cpuCoresTitle}</h2>
        {model && <p className="cpu-core-model">{model}</p>}
      </div>
      <dl className="cpu-core-header-meta">
        {hasPower && <div><dt>{t.cpuPowerHeading}</dt><dd className="cpu-core-power">{t.cpuPowerWatts(decimal.format(power))}</dd></div>}
        {temperature && <div>
          <dt>{t.cpuTemperatureHeading}</dt>
          <dd className="cpu-core-temperature" title={temperature.package ? t.cpuTemperatureSource(temperature.chip, temperature.sensor) : t.cpuTemperatureHottestCore(temperature.chip, temperature.sensor)}>
            {t.temperatureCelsius(decimal.format(temperature.value))}
            {!temperature.package && <small>{t.cpuTemperatureHottestCoreTag}</small>}
          </dd>
        </div>}
      </dl>
    </div>

    <div className="cpu-total-load">
      <div className="cpu-total-load-head">
        <span>{t.cpuTotalLoadLabel}</span>
        <strong>{totalStats === null ? t.noSamples : percentage.format(totalStats.mean)}</strong>
      </div>
      <div
        className={`cpu-meter cpu-meter--total cpu-meter--${totalStats === null ? "empty" : severity(totalStats.mean)}`}
        role="img"
        aria-label={totalStats === null ? t.noSamples : t.cpuTotalLoadUsage(percentage.format(totalStats.mean), percentage.format(totalStats.max))}
      >
        <span className="cpu-meter-fill" style={{ width: meterWidth(totalStats?.mean ?? 0), transitionDuration: `${CPU_METER_TRANSITION_MS}ms` }} />
        {totalStats !== null && <i className="cpu-meter-peak" style={{ "--cpu-peak": meterWidth(totalStats.max) } as CSSProperties} aria-hidden="true" />}
      </div>
    </div>

    <div className="cpu-core-controls">
      <label>
        <span>{t.cpuAveragingLabel}</span>
        <select value={preferences.averagingWindowSeconds} onChange={(event) => setPreferences({ averagingWindowSeconds: Number(event.target.value) as CPUAveragingWindow })}>
          {CPU_AVERAGING_WINDOWS.map((seconds) => <option key={seconds} value={seconds}>{t.cpuAveragingWindow(seconds)}</option>)}
        </select>
      </label>
      <p className={`cpu-core-refresh${refreshStale ? " cpu-core-refresh--stale" : ""}`}>
        <span className="cpu-core-pulse" aria-hidden="true" />
        {refreshedAge === null ? t.noSamples : t.cpuRefreshedAge(refreshedAge)}
      </p>
    </div>

    {(isolatedCount > 0 || offlineCount > 0) && <p className="cpu-core-reservations">
      {offlineCount > 0 && <span className="cpu-core-offline-count">{t.cpuOfflineCount(offlineCount)}</span>}
      {isolatedCount > 0 && <span className="cpu-core-isolated">{t.cpuIsolatedCount(isolatedCount)}</span>}
    </p>}

    {groups.length === 0 ? <p className="cpu-core-empty">{t.cpuCoresPending}</p> : <>
      <ul className={`cpu-core-rows${isolatedCount > 0 ? " cpu-core-rows--reserved" : ""}`} aria-label={t.cpuCoreRowsLabel}>
        {groups.map((group) => {
          const siblings = group.cpus.slice(1).map((cpu) => cpu.cpu);
          const allOffline = group.offline.length === group.cpus.length;
          return <li className={`cpu-core-row${allOffline ? " cpu-core-row--offline" : ""}`} key={group.id}>
            <div className="cpu-core-row-label">
              <strong>{t.cpuCoreLabel(group.cpus[0].cpu)}</strong>
              {group.type !== "unknown" && <span className="cpu-core-row-type">{t.cpuCoreType(group.type)}</span>}
              {siblings.length > 0 && <span className="cpu-core-row-ht">{t.cpuHyperthreadLabel(siblings.join(", "))}</span>}
              {allOffline && <span className="cpu-core-row-state">{t.cpuOffline}</span>}
            </div>
            <div className="cpu-core-row-threads">
              {group.cpus.map((cpu) => {
                const stats = windowStats(cpu.points, preferences.averagingWindowSeconds);
                const isolated = group.isolated.includes(cpu.cpu);
                const offline = group.offline.includes(cpu.cpu);
                const state = offline ? "offline" : stats === null ? "empty" : severity(stats.mean);
                return <div className={`cpu-thread-row${offline ? " cpu-thread-row--offline" : ""}`} key={cpu.cpu}>
                  <span className="cpu-thread-id" aria-hidden="true">{group.cpus.length > 1 ? cpu.cpu : ""}</span>
                  <div
                    className={`cpu-meter cpu-meter--${state}`}
                    role="img"
                    aria-label={offline ? t.cpuOfflineThread(cpu.cpu) : stats === null ? t.cpuThreadNoSamples(cpu.cpu) : t.cpuThreadUsage(cpu.cpu, percentage.format(stats.mean), percentage.format(stats.max))}
                  >
                    {!offline && <span className="cpu-meter-fill" style={{ width: meterWidth(stats?.mean ?? 0), transitionDuration: `${CPU_METER_TRANSITION_MS}ms` }} />}
                    {!offline && stats !== null && <i className="cpu-meter-peak" style={{ "--cpu-peak": meterWidth(stats.max) } as CSSProperties} aria-hidden="true" />}
                  </div>
                  <strong className="cpu-thread-value">{offline ? t.cpuOfflineShort : stats === null ? t.cpuNoValue : percentage.format(stats.mean)}</strong>
                  {isolated && <small className="cpu-thread-isolated">
                    <span className="sr-only">{t.cpuIsolatedThread(cpu.cpu)}</span>
                    <span aria-hidden="true">{t.cpuIsolated}</span>
                  </small>}
                </div>;
              })}
            </div>
          </li>;
        })}
      </ul>
      <CPULoadSparkline
        points={totalPoints}
        label={t.cpuHistoryLabel}
        current={totalStats === null ? t.noSamples : percentage.format(totalStats.latest)}
      />
    </>}
  </article>;
}
