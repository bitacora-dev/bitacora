import type { Inventory, InventoryItem, Job, SeriesPoint, Summary } from "./api";
import { groupDisks, sumUsage, type DiskUsage, type SmartStatus } from "./disks";

// Pure derivation of the dense overview's readouts. The constants band and
// the host bar both need the same answers ("how much RAM is in use", "how
// full is the storage", "how many updates are pending"), so they are derived
// once here, free of React, and tested on their own.
//
// Every reading is `number | null`, never a number that may be zero. A host
// that has not reported a signal and a host reporting zero are different
// answers, and the band says the first one in words.

export function latestPoint(points: SeriesPoint[]): SeriesPoint | null {
  let newest: SeriesPoint | null = null;
  for (const point of points) {
    if (!Number.isFinite(point.value) || !Number.isFinite(new Date(point.ts).getTime())) continue;
    if (newest === null || new Date(point.ts).getTime() > new Date(newest.ts).getTime()) newest = point;
  }
  return newest;
}

export function latestValue(points: SeriesPoint[]): number | null {
  return latestPoint(points)?.value ?? null;
}

export interface MemorySnapshot {
  usedBytes: number | null;
  totalBytes: number | null;
  availableBytes: number | null;
  // Share of RAM in use. The hub already computes it, so it is read rather
  // than divided again; the division is only a fallback for a host that
  // reports bytes but no ratio.
  usedRatio: number | null;
  swapTotalBytes: number | null;
  swapUsedBytes: number | null;
}

export function memorySnapshot(summary: Summary): MemorySnapshot {
  const usedBytes = latestValue(summary.memory_used_bytes);
  const totalBytes = latestValue(summary.memory_total_bytes);
  const ratio = latestValue(summary.memory);
  const swapTotalBytes = latestValue(summary.memory_swap_total_bytes);
  const swapFreeBytes = latestValue(summary.memory_swap_free_bytes);
  return {
    usedBytes,
    totalBytes,
    availableBytes: latestValue(summary.memory_available_bytes),
    usedRatio: ratio ?? (usedBytes !== null && totalBytes !== null && totalBytes > 0 ? usedBytes / totalBytes : null),
    swapTotalBytes,
    // Swap in use is only derivable when both halves arrived. One of the two
    // alone proves nothing, and `total - null` would read as "swap is full".
    swapUsedBytes: swapTotalBytes !== null && swapFreeBytes !== null ? Math.max(0, swapTotalBytes - swapFreeBytes) : null,
  };
}

export interface StorageSnapshot {
  usage: DiskUsage | null;
  deviceCount: number;
  smart: Record<SmartStatus, number>;
  // Devices no collector reported a SMART verdict for. Counted separately
  // because "no verdict" is not "passed": a host without bitacora-smart
  // would otherwise look fully audited.
  smartUnreported: number;
  degradedArrays: number;
}

export function storageSnapshot(inventory: Inventory | null): StorageSnapshot {
  const groups = inventory ? groupDisks(inventory.items) : [];
  const devices = groups.flatMap((group) => group.disks);
  const smart: Record<SmartStatus, number> = { passed: 0, failed: 0 };
  let smartUnreported = 0;
  for (const device of devices) {
    if (device.smartStatus === null) smartUnreported += 1;
    else smart[device.smartStatus] += 1;
  }
  return {
    usage: sumUsage(devices),
    deviceCount: devices.length,
    smart,
    smartUnreported,
    degradedArrays: groups.filter((group) => group.arrayHealth === "degraded").length,
  };
}

// The package inventory carries one synthetic item describing which actions
// the host enabled, and candidates from NotAutomatic suites that `apt
// upgrade` never installs. Neither is a pending update, so neither is
// counted in the host bar.
export function pendingUpdateItems(inventory: Inventory | null): InventoryItem[] {
  return (inventory?.items ?? []).filter((item) => item.id !== "package-actions" && item.attrs.candidate_automatic !== "false");
}

// A package row's two versions. Both are optional strings on the wire, so an
// unreported one stays null instead of becoming an empty cell that reads as
// "no version".
export interface PackageVersions {
  installed: string | null;
  candidate: string | null;
  source: string | null;
}

export function packageVersions(item: InventoryItem): PackageVersions {
  const read = (key: string) => {
    const value = item.attrs[key];
    return typeof value === "string" && value.trim() !== "" ? value.trim() : null;
  };
  return { installed: read("installed_version"), candidate: read("candidate_version"), source: read("source") };
}

// A running job's completion ratio, when the extractor reported both halves
// of it. ADR-0010 lets `stats` carry free-form keys beside the canonical
// ones, so a tool that reports a total is honoured; a tool that does not
// gets no bar rather than a bar derived from a number that is not a total.
// `files_checked` is deliberately not read as a total: it counts work
// already done, not work still to do.
const PROGRESS_PAIRS: ReadonlyArray<readonly [string, string]> = [
  ["bytes_transferred", "bytes_total"],
  ["files_transferred", "files_total"],
];

function statNumber(stats: Record<string, unknown> | undefined, key: string): number | null {
  const value = stats?.[key];
  if (value === null || value === undefined || value === "") return null;
  const parsed = typeof value === "number" ? value : Number(value);
  return Number.isFinite(parsed) && parsed >= 0 ? parsed : null;
}

export function jobProgressRatio(job: Job): number | null {
  for (const [doneKey, totalKey] of PROGRESS_PAIRS) {
    const done = statNumber(job.stats, doneKey);
    const total = statNumber(job.stats, totalKey);
    if (done === null || total === null || total <= 0) continue;
    return Math.max(0, Math.min(1, done / total));
  }
  return null;
}

export function isRunning(job: Job): boolean {
  return job.status === "running";
}

// Running runs first, then the rest newest-first. The hub already orders
// jobs by when they last moved, but a run that started an hour ago and is
// still going would sink below three fast ones that finished since.
export function splitJobs(jobs: Job[]): { running: Job[]; finished: Job[] } {
  return { running: jobs.filter(isRunning), finished: jobs.filter((job) => !isRunning(job)) };
}
