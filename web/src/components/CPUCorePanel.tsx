import type { CPUSeries, Inventory, InventoryItem } from "../api";
import { useTranslation } from "../i18n";

interface CoreGroup {
  id: string;
  type: string;
  online: boolean;
  cpus: CPUSeries[];
}

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
    const group = groups.get(key) ?? {
      id: coreID ?? cpu.cpu,
      type: item?.attrs.core_type ?? "unknown",
      online: item?.attrs.online !== "false",
      cpus: [],
    };
    group.online = group.online && item?.attrs.online !== "false";
    if (group.type === "unknown" && item?.attrs.core_type) group.type = item.attrs.core_type;
    group.cpus.push(cpu);
    groups.set(key, group);
  }
  return [...groups.values()]
    .map((group) => ({ ...group, cpus: group.cpus.sort((left, right) => cpuNumber(left.cpu) - cpuNumber(right.cpu)) }))
    .sort((left, right) => Number(left.id) - Number(right.id));
}

function latest(series: CPUSeries): number | null {
  const point = series.points[series.points.length - 1];
  return point?.value ?? null;
}

interface Props {
  cores: CPUSeries[];
  topology: Inventory | null;
  identity: Inventory | null;
}

export default function CPUCorePanel({ cores, topology, identity }: Props) {
  const { t, intlTag } = useTranslation();
  const groups = groupCPUCores(cores, topology);
  const system = identity?.items.find((item) => item.id === "system");
  const model = system?.attrs.cpu_model;
  const power = Number(system?.attrs.cpu_power_watts);
  const hasPower = Number.isFinite(power);
  const percentage = new Intl.NumberFormat(intlTag, { style: "percent", minimumFractionDigits: 0, maximumFractionDigits: 1 });

  return (
    <article className="control-panel cpu-core-panel">
      <div className="panel-title-row">
        <div>
          <h2>{t.cpuCoresTitle}</h2>
          {model && <p className="cpu-core-model">{model}</p>}
        </div>
        {hasPower && <span className="cpu-core-power">{t.cpuPowerWatts(new Intl.NumberFormat(intlTag, { maximumFractionDigits: 1 }).format(power))}</span>}
      </div>
      {groups.length === 0 ? <p className="cpu-core-empty">{t.cpuCoresPending}</p> : (
        <div className="cpu-core-grid" aria-label={t.cpuCoresTitle}>
          {groups.map((group) => (
            <section className="cpu-core" key={group.id}>
              <div className="cpu-core-heading">
                <strong>{t.cpuCoreLabel(group.cpus.length > 1 ? `${group.cpus[0].cpu} · HT ${group.cpus.slice(1).map((cpu) => cpu.cpu).join(", ")}` : group.cpus[0].cpu)}</strong>
                {group.type !== "unknown" && <span>{t.cpuCoreType(group.type)}</span>}
                {!group.online && <span>{t.cpuOffline}</span>}
              </div>
              <div className="cpu-thread-list">
                {group.cpus.map((cpu) => {
                  const value = latest(cpu);
                  return <div className="cpu-thread" key={cpu.cpu}>
                    <span>{t.cpuThreadLabel(cpu.cpu)}</span>
                    <div className="cpu-thread-meter" aria-label={value === null ? t.noSamples : t.cpuThreadUsage(cpu.cpu, percentage.format(value))}>
                      <span style={{ width: `${Math.max(0, Math.min(1, value ?? 0)) * 100}%` }} />
                    </div>
                    <strong>{value === null ? t.noSamples : percentage.format(value)}</strong>
                  </div>;
                })}
              </div>
            </section>
          ))}
        </div>
      )}
    </article>
  );
}
