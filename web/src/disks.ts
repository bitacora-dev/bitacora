import type { InventoryItem } from "./api";

// Pure derivation of the disks panel's model out of a `disk` Inventory
// (ADR-0016). The collector reports one item per mounted filesystem, which
// is the right thing to collect and the wrong thing to show: on a host
// whose root device carries `/`, `/tmp`, `/var/tmp` and
// `/var/lib/bitacora`, four items are one disk, and listing them as four
// disks also counted that disk's capacity four times in the totals.
//
// So the view groups items by device and shows the mountpoints inside the
// row. Everything here is deliberately free of React so the grouping and
// the totals are testable on their own.

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

export const NEARLY_FULL_RATIO = 0.9;

export function isNearlyFull(usage: DiskUsage) {
  return usage.ratio >= NEARLY_FULL_RATIO;
}

export type SmartStatus = "passed" | "failed";
export type ArrayHealth = "healthy" | "degraded" | "unknown";

// The state behind the row's status light. `unknown` is not a failure: it
// means no collector reported a health verdict for this device, which is
// exactly what a host without bitacora-smart looks like. Nothing here
// claims whether a disk is spinning or parked — `smartctl --json` does not
// report a power mode, so that part of UnRaid's light has no honest source
// yet and the light reports health instead.
export type DiskState = "ok" | "warning" | "critical" | "unknown";

export interface DiskMount {
  mountpoint: string;
  fstype: string | null;
  usage: DiskUsage | null;
}

export interface Disk {
  key: string;
  device: string | null;
  model: string | null;
  serial: string | null;
  temperatureCelsius: number | null;
  smartStatus: SmartStatus | null;
  arrayType: string | null;
  arrayLevel: string | null;
  arrayMemberCount: string | null;
  arrayHealth: ArrayHealth | null;
  arrayRole: string | null;
  mounts: DiskMount[];
  usage: DiskUsage | null;
  state: DiskState;
}

export interface DiskGroup {
  key: string;
  arrayType: string | null;
  arrayLevel: string | null;
  arrayMemberCount: string | null;
  arrayHealth: ArrayHealth | null;
  disks: Disk[];
  total: DiskUsage | null;
}

export const STANDALONE_GROUP = "standalone";

// Parity before data mirrors how UnRaid's array panel reads: the disks
// that protect the array, then the disks that hold it, then whatever else
// the host mounts. Any array type not listed here still groups on its own,
// ordered after the known ones and before the standalone disks.
const GROUP_ORDER = ["storage.snapraid:parity", "storage.snapraid:data", "storage.mdraid", "storage.unraid_array", "storage.mergerfs"];

function groupKey(attrs: Record<string, string>): string {
  const arrayType = attrs.array_type;
  if (!arrayType) return STANDALONE_GROUP;
  return attrs.array_role ? `${arrayType}:${attrs.array_role}` : arrayType;
}

function groupRank(key: string): number {
  if (key === STANDALONE_GROUP) return GROUP_ORDER.length + 1;
  const known = GROUP_ORDER.indexOf(key);
  return known === -1 ? GROUP_ORDER.length : known;
}

function optional(value: string | undefined): string | null {
  return value ? value : null;
}

function temperature(attrs: Record<string, string>): number | null {
  if (!attrs.temperature_celsius) return null;
  const celsius = Number(attrs.temperature_celsius);
  return Number.isFinite(celsius) ? celsius : null;
}

function smartStatus(attrs: Record<string, string>): SmartStatus | null {
  return attrs.smart_status === "passed" || attrs.smart_status === "failed" ? attrs.smart_status : null;
}

function arrayHealth(attrs: Record<string, string>): ArrayHealth | null {
  const health = attrs.array_health;
  return health === "healthy" || health === "degraded" || health === "unknown" ? health : null;
}

// Several mountpoints on one device report the same filesystem, so their
// usages must not be added up — that was the bug. The widest capacity is
// the closest thing to the device itself; the shortest mountpoint breaks a
// tie towards the mount nearest the filesystem root.
function representativeUsage(mounts: DiskMount[]): DiskUsage | null {
  let best: DiskMount | null = null;
  for (const mount of mounts) {
    if (!mount.usage) continue;
    if (
      !best?.usage ||
      mount.usage.capacity > best.usage.capacity ||
      (mount.usage.capacity === best.usage.capacity && mount.mountpoint.length < best.mountpoint.length)
    ) {
      best = mount;
    }
  }
  return best?.usage ?? null;
}

// One degraded member degrades the array, so the worst verdict in the
// group wins. A group whose members report no verdict at all stays
// `unknown` rather than borrowing the health of the ones that did.
function groupHealth(disks: Disk[]): ArrayHealth | null {
  let health: ArrayHealth | null = null;
  for (const disk of disks) {
    if (disk.arrayHealth === "degraded") return "degraded";
    if (disk.arrayHealth === "healthy") health = "healthy";
    else if (disk.arrayHealth === "unknown" && health === null) health = "unknown";
  }
  return health;
}

function diskState(disk: Omit<Disk, "state">): DiskState {
  if (disk.smartStatus === "failed" || disk.arrayHealth === "degraded") return "critical";
  if (disk.usage && isNearlyFull(disk.usage)) return "warning";
  if (disk.smartStatus === "passed") return "ok";
  return "unknown";
}

// sumUsage adds up already-deduplicated disks, so a device mounted four
// times contributes its capacity once. Disks without usage data are left
// out of the sum instead of counted as empty.
export function sumUsage(disks: Disk[]): DiskUsage | null {
  let capacity = 0;
  let used = 0;
  let counted = 0;
  for (const disk of disks) {
    if (!disk.usage) continue;
    capacity += disk.usage.capacity;
    used += disk.usage.used;
    counted += 1;
  }
  if (counted === 0) return null;
  return diskUsage({ capacity_bytes: String(capacity), used_bytes: String(used), available_bytes: String(Math.max(capacity - used, 0)) });
}

// groupDisks collapses the mount-level inventory into one entry per device
// and buckets those by array function. An item with no `device` attribute
// keeps its own entry keyed by name rather than merging with every other
// nameless one.
export function groupDisks(items: InventoryItem[]): DiskGroup[] {
  const byGroup = new Map<string, Map<string, Disk>>();

  for (const item of items) {
    const attrs = item.attrs ?? {};
    const device = optional(attrs.device);
    const key = device ?? `mount:${item.id || item.name}`;
    const group = groupKey(attrs);

    let disks = byGroup.get(group);
    if (!disks) {
      disks = new Map<string, Disk>();
      byGroup.set(group, disks);
    }

    const mount: DiskMount = {
      mountpoint: item.name || item.id,
      fstype: optional(attrs.fstype),
      usage: diskUsage(attrs),
    };

    const existing = disks.get(key);
    if (existing) {
      existing.mounts.push(mount);
      // Identity and health come from the device, so any mount of it
      // carries them; keep the first non-empty value seen.
      existing.model ??= optional(attrs.model);
      existing.serial ??= optional(attrs.serial);
      existing.temperatureCelsius ??= temperature(attrs);
      existing.smartStatus ??= smartStatus(attrs);
      continue;
    }

    disks.set(key, {
      key,
      device,
      model: optional(attrs.model),
      serial: optional(attrs.serial),
      temperatureCelsius: temperature(attrs),
      smartStatus: smartStatus(attrs),
      arrayType: optional(attrs.array_type),
      arrayLevel: optional(attrs.array_level),
      arrayMemberCount: optional(attrs.array_member_count),
      arrayHealth: arrayHealth(attrs),
      arrayRole: optional(attrs.array_role),
      mounts: [mount],
      usage: null,
      state: "unknown",
    });
  }

  const groups: DiskGroup[] = [];
  for (const [key, disks] of byGroup) {
    const resolved = [...disks.values()].map((disk) => {
      disk.mounts.sort((a, b) => a.mountpoint.localeCompare(b.mountpoint));
      disk.usage = representativeUsage(disk.mounts);
      disk.state = diskState(disk);
      return disk;
    });
    resolved.sort((a, b) => (a.device ?? a.key).localeCompare(b.device ?? b.key, undefined, { numeric: true }));
    const first = resolved[0];
    groups.push({
      key,
      arrayType: first?.arrayType ?? null,
      arrayLevel: first?.arrayLevel ?? null,
      arrayMemberCount: first?.arrayMemberCount ?? null,
      arrayHealth: groupHealth(resolved),
      disks: resolved,
      total: sumUsage(resolved),
    });
  }

  groups.sort((a, b) => groupRank(a.key) - groupRank(b.key) || a.key.localeCompare(b.key));
  return groups;
}
