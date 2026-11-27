import type { ReactNode } from "react";
import type { Inventory, SeriesPoint, Summary } from "../api";
import { formatBytes } from "../bytes";
import { useTranslation } from "../i18n";
import { latestValue, memorySnapshot, storageSnapshot } from "../overview";
import Sparkline from "./Sparkline";

// The band of constants: one container with cells divided by lines, not five
// floating cards. It is the dense overview's first answer to "is my server
// OK?" — the five host-wide numbers an operator scans before reading any
// panel, each with its own window trace behind it.
//
// It is a band rather than a card row on purpose: cards put a border, a
// radius and a shadow around every number, which is three pieces of chrome
// per fact and pushes the three columns below the fold on a 1440×900 screen.
//
// Nothing here substitutes zero for an unreported signal. A cell with no
// samples says so in words and draws no trace.

interface CellProps {
  label: string;
  value: string;
  context: ReactNode;
  points?: SeriesPoint[];
  ceiling?: number;
  tone: "cyan" | "gold" | "green";
}

function BandCell({ label, value, context, points, ceiling, tone }: CellProps) {
  return (
    <div className="band-cell">
      {points && points.length > 0 && <Sparkline points={points} ceiling={ceiling} tone={tone} />}
      <div className="band-cell-body">
        <span className="band-cell-label">{label}</span>
        <strong className="band-cell-value">{value}</strong>
        <span className="band-cell-context">{context}</span>
      </div>
    </div>
  );
}

interface Props {
  summary: Summary;
  disks: Inventory | null;
  // The CPU package reading the processor panel already resolved, so the band
  // and the panel can never disagree about which sensor is the package one.
  temperatureCelsius: number | null;
  powerWatts: number | null;
}

export default function ConstantsBand({ summary, disks, temperatureCelsius, powerWatts }: Props) {
  const { t, intlTag } = useTranslation();
  const percent = new Intl.NumberFormat(intlTag, { style: "percent", maximumFractionDigits: 1 });
  const decimal = new Intl.NumberFormat(intlTag, { maximumFractionDigits: 1 });
  const bytes = (value: number) => formatBytes(value, intlTag);
  const rate = (value: number) => t.bytesPerSecond(bytes(value));

  const cpu = latestValue(summary.cpu);
  const memory = memorySnapshot(summary);
  const rx = latestValue(summary.network_rx_bytes_per_second);
  const tx = latestValue(summary.network_tx_bytes_per_second);
  const storage = storageSnapshot(disks);

  const peak = (points: SeriesPoint[]): string | null => {
    const values = points.map((point) => point.value).filter((value) => Number.isFinite(value));
    return values.length === 0 ? null : t.bandPeak(rate(Math.max(...values)));
  };

  const processorContext = [
    temperatureCelsius === null ? null : t.temperatureCelsius(decimal.format(temperatureCelsius)),
    powerWatts === null ? null : t.cpuPowerWatts(decimal.format(powerWatts)),
  ].filter((entry): entry is string => entry !== null);

  const memoryContext = [
    memory.usedBytes !== null && memory.totalBytes !== null ? t.memoryOfTotal(bytes(memory.usedBytes), bytes(memory.totalBytes)) : null,
    memory.swapUsedBytes !== null && memory.swapTotalBytes !== null
      ? t.bandSwapUsed(bytes(memory.swapUsedBytes), bytes(memory.swapTotalBytes))
      : null,
  ].filter((entry): entry is string => entry !== null);

  const storageContext: string[] = [];
  if (storage.deviceCount > 0) storageContext.push(t.bandStorageDevices(storage.deviceCount));
  if (storage.usage) storageContext.push(t.memoryOfTotal(bytes(storage.usage.used), bytes(storage.usage.capacity)));
  if (storage.smart.failed > 0) storageContext.push(t.bandSmartFailed(storage.smart.failed));
  else if (storage.smart.passed > 0) storageContext.push(t.bandSmartPassed(storage.smart.passed));
  if (storage.smartUnreported > 0) storageContext.push(t.bandSmartUnreported(storage.smartUnreported));
  if (storage.degradedArrays > 0) storageContext.push(t.bandArraysDegraded(storage.degradedArrays));

  return (
    <section className="constants-band" aria-label={t.bandSectionLabel}>
      <BandCell
        label={t.cpuCoresTitle}
        value={cpu === null ? t.noSamples : percent.format(cpu)}
        context={processorContext.length === 0 ? t.bandNoContext : processorContext.join(" · ")}
        points={summary.cpu}
        ceiling={1}
        tone="cyan"
      />
      <BandCell
        label={t.bandMemoryLabel}
        value={memory.usedRatio === null ? t.noSamples : percent.format(memory.usedRatio)}
        context={memoryContext.length === 0 ? t.bandNoContext : memoryContext.join(" · ")}
        points={summary.memory}
        ceiling={1}
        tone="gold"
      />
      <BandCell
        label={t.bandNetworkInLabel}
        value={rx === null ? t.noSamples : rate(rx)}
        context={peak(summary.network_rx_bytes_per_second) ?? t.bandNoContext}
        points={summary.network_rx_bytes_per_second}
        tone="cyan"
      />
      <BandCell
        label={t.bandNetworkOutLabel}
        value={tx === null ? t.noSamples : rate(tx)}
        context={peak(summary.network_tx_bytes_per_second) ?? t.bandNoContext}
        points={summary.network_tx_bytes_per_second}
        tone="green"
      />
      <BandCell
        label={t.bandStorageLabel}
        value={storage.usage === null ? t.noSamples : percent.format(storage.usage.ratio)}
        context={storageContext.length === 0 ? t.bandStorageUnreported : storageContext.join(" · ")}
        tone="cyan"
      />
    </section>
  );
}
