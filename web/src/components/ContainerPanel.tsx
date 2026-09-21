import { useEffect, useState } from "react";
import type { ContainerSeries, SeriesPoint } from "../api";
import { useTranslation } from "../i18n";

const CONTAINER_PANEL_PREFERENCES_KEY = "bitacora_container_panel_preferences";

// One logical CPU is the reference a per-container meter is read against, so a
// quarter-core container does not look saturated on an idle host. A container
// that genuinely exceeds one core raises the ceiling for everyone, keeping the
// bars comparable to each other instead of clipping the busiest one.
const CPU_METER_FLOOR_CORES = 1;

export interface ContainerPanelPreferences { expanded: boolean; }
const defaultPreferences: ContainerPanelPreferences = { expanded: false };

// ContainerSnapshot is one container's current state: the latest point of each
// of its own series, plus whether it is still reporting.
//
// A null value is "no reading", never zero. The two are different answers and
// only one of them is true for a container that has stopped.
export interface ContainerSnapshot {
  container: ContainerSeries;
  cpuCoresUsed: number | null;
  memoryBytes: number | null;
  lastSeen: string | null;
  active: boolean;
}

export interface ContainerOverview {
  rows: ContainerSnapshot[];
  activeCount: number;
  totalCPUCoresUsed: number | null;
  totalMemoryBytes: number | null;
  cpuMeterCeiling: number;
  memoryMeterCeiling: number;
}

function latestPoint(points: SeriesPoint[]): SeriesPoint | null {
  let latest: SeriesPoint | null = null;
  for (const point of points) {
    if (!Number.isFinite(point.value) || !Number.isFinite(new Date(point.ts).getTime())) continue;
    if (latest === null || new Date(point.ts).getTime() > new Date(latest.ts).getTime()) latest = point;
  }
  return latest;
}

function laterInstant(left: string | null, right: string | null): string | null {
  if (left === null) return right;
  if (right === null) return left;
  return new Date(right).getTime() > new Date(left).getTime() ? right : left;
}

// summariseContainers reduces each container's series to its current reading.
//
// "Current" is the most recent collection cycle present in the window, not
// "each container's own last point". The agent stamps every metric of one
// cycle with the same instant (collector.CycleSink), so a container missing
// from that instant is a container that is no longer running. Carrying its
// last known values into the header totals would report resources that are not
// being used; reporting it as zero would claim it is running and idle. It is
// listed, marked, and left out of the totals.
//
// The CPU values here are the per-container rates the hub already derived
// inside each container's own series. They are only ever added up afterwards,
// which is the whole reason a container starting or stopping mid-window does
// not show up as a spike.
export function summariseContainers(containers: ContainerSeries[]): ContainerOverview {
  const snapshots: ContainerSnapshot[] = containers.map((container) => {
    const cpu = latestPoint(container.cpu_cores_used);
    const memory = latestPoint(container.memory_bytes);
    return {
      container,
      cpuCoresUsed: cpu?.value ?? null,
      memoryBytes: memory?.value ?? null,
      lastSeen: laterInstant(cpu?.ts ?? null, memory?.ts ?? null),
      active: false,
    };
  });

  const currentCycle = snapshots.reduce<string | null>((latest, snapshot) => laterInstant(latest, snapshot.lastSeen), null);
  const rows = snapshots.map((snapshot) => ({ ...snapshot, active: snapshot.lastSeen !== null && snapshot.lastSeen === currentCycle }));

  rows.sort((left, right) => {
    if (left.active !== right.active) return left.active ? -1 : 1;
    const byLoad = (right.cpuCoresUsed ?? -1) - (left.cpuCoresUsed ?? -1);
    if (byLoad !== 0) return byLoad;
    return left.container.container_name.localeCompare(right.container.container_name);
  });

  const active = rows.filter((row) => row.active);
  const cpuReadings = active.map((row) => row.cpuCoresUsed).filter((value): value is number => value !== null);
  const memoryReadings = active.map((row) => row.memoryBytes).filter((value): value is number => value !== null);

  return {
    rows,
    activeCount: active.length,
    totalCPUCoresUsed: cpuReadings.length === 0 ? null : cpuReadings.reduce((total, value) => total + value, 0),
    totalMemoryBytes: memoryReadings.length === 0 ? null : memoryReadings.reduce((total, value) => total + value, 0),
    cpuMeterCeiling: Math.max(CPU_METER_FLOOR_CORES, ...cpuReadings),
    memoryMeterCeiling: Math.max(0, ...memoryReadings),
  };
}

export function readContainerPanelPreferences(storage?: Storage | null): ContainerPanelPreferences {
  try {
    const target = storage === undefined && typeof window !== "undefined" ? window.localStorage : storage;
    const value = target?.getItem(CONTAINER_PANEL_PREFERENCES_KEY);
    if (!value) return defaultPreferences;
    const parsed: unknown = JSON.parse(value);
    if (typeof parsed !== "object" || parsed === null) return defaultPreferences;
    const candidate = parsed as Partial<ContainerPanelPreferences>;
    if (typeof candidate.expanded !== "boolean") return defaultPreferences;
    return { expanded: candidate.expanded };
  } catch { return defaultPreferences; }
}

function saveContainerPanelPreferences(preferences: ContainerPanelPreferences): void {
  try { window.localStorage.setItem(CONTAINER_PANEL_PREFERENCES_KEY, JSON.stringify(preferences)); } catch {
    // Storage can be unavailable in private browsing or restricted embeds.
  }
}

// NO_READING is the numeric column's placeholder, matching formatBytes' own
// dash for an unusable value. The readable sentence lives in the row's
// screen-reader text: "Sin muestras todavía" does not fit a numeric slot and
// pushed the value out of its column when it was rendered there.
const NO_READING = "\u2014";

function meterWidth(value: number | null, ceiling: number): string {
  if (value === null || ceiling <= 0) return "0%";
  return `${Math.max(0, Math.min(1, value / ceiling)) * 100}%`;
}

interface Props { containers: ContainerSeries[]; formatBytes: (value: number) => string; }

export default function ContainerPanel({ containers, formatBytes }: Props) {
  const { t, intlTag } = useTranslation();
  const [preferences, setPreferences] = useState(readContainerPanelPreferences);
  const overview = summariseContainers(containers);
  const cores = new Intl.NumberFormat(intlTag, { minimumFractionDigits: 2, maximumFractionDigits: 2 });
  const detailsID = "container-details";

  useEffect(() => { saveContainerPanelPreferences(preferences); }, [preferences]);

  return <article className="control-panel container-panel">
    <div className="panel-title-row container-panel-header">
      <div>
        <h2>{t.containersTitle}</h2>
        <p className="container-panel-note">{t.containersSubtitle}</p>
      </div>
      {/* The totals have to survive the collapsed state: a panel that hides
          both the detail and the sum answers nothing. The toggle sits with
          them so the collapsed panel stays one compact band. */}
      <div className="container-panel-totals">
        <span className="container-panel-count">{t.containersActiveCount(overview.activeCount)}</span>
        {overview.totalCPUCoresUsed !== null && <strong>{t.containersCoresUsed(cores.format(overview.totalCPUCoresUsed))}</strong>}
        {overview.totalMemoryBytes !== null && <strong className="container-panel-memory">{t.containersMemoryUsed(formatBytes(overview.totalMemoryBytes))}</strong>}
        {overview.rows.length > 0 && <button type="button" className="cpu-details-toggle" aria-expanded={preferences.expanded} aria-controls={detailsID} onClick={() => setPreferences((current) => ({ expanded: !current.expanded }))}>
          {preferences.expanded ? t.containersDetailsHide : t.containersDetailsShow}
        </button>}
      </div>
    </div>

    {overview.rows.length === 0 ? <p className="container-empty">{t.containersPending}</p> : <>
      {preferences.expanded && <ul className="container-list" id={detailsID} aria-label={t.containersTitle}>
        {overview.rows.map((row) => <li className={`container-row${row.active ? "" : " container-row--stopped"}`} key={row.container.container_id}>
          <div className="container-identity">
            <strong title={row.container.container_name}>{row.container.container_name}</strong>
            <small>{row.container.container_id}</small>
            {!row.active && <small className="container-stopped-badge">{t.containersStopped}</small>}
          </div>

          <div className="container-reading">
            <span className="container-meter" aria-hidden="true"><span style={{ width: meterWidth(row.active ? row.cpuCoresUsed : null, overview.cpuMeterCeiling) }} /></span>
            <strong>{row.cpuCoresUsed === null ? NO_READING : t.containersCoresUsed(cores.format(row.cpuCoresUsed))}</strong>
            <span className="sr-only">{t.containersCPUReading(row.container.container_name, row.cpuCoresUsed === null ? t.noSamples : cores.format(row.cpuCoresUsed))}</span>
          </div>

          <div className="container-reading">
            <span className="container-meter container-meter--memory" aria-hidden="true"><span style={{ width: meterWidth(row.active ? row.memoryBytes : null, overview.memoryMeterCeiling) }} /></span>
            <strong>{row.memoryBytes === null ? NO_READING : formatBytes(row.memoryBytes)}</strong>
            <span className="sr-only">{t.containersMemoryReading(row.container.container_name, row.memoryBytes === null ? t.noSamples : formatBytes(row.memoryBytes))}</span>
          </div>
        </li>)}
      </ul>}
    </>}
  </article>;
}
