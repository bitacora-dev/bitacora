import { describe, expect, it } from "vitest";
import type { Inventory, InventoryItem, Job, Summary } from "./api";
import { jobProgressRatio, latestValue, memorySnapshot, packageVersions, pendingUpdateItems, splitJobs, storageSnapshot } from "./overview";

const point = (ts: string, value: number) => ({ ts, value });

const emptySummary = (): Summary => ({
  host_id: "host-a",
  generated_at: "2026-11-27T10:00:00Z",
  window_secs: 900,
  cpu: [],
  cpu_cores: [],
  temperatures: [],
  memory: [],
  memory_total_bytes: [],
  memory_available_bytes: [],
  memory_used_bytes: [],
  memory_swap_total_bytes: [],
  memory_swap_free_bytes: [],
  network_rx_bytes_per_second: [],
  network_tx_bytes_per_second: [],
  public_surface: {
    ssh_failed_logins_total: [],
    ssh_failed_logins_per_minute: [],
    fail2ban_jails_total: [],
    fail2ban_banned_total: [],
    firewall_rules_total: [],
    ovh_traffic_used_ratio: [],
  },
  containers: [],
  events: [],
  jobs: [],
});

const diskInventory = (items: InventoryItem[]): Inventory => ({
  host_id: "host-a",
  kind: "disk",
  reported_at: "2026-11-27T10:00:00Z",
  schema: 1,
  items,
});

const job = (overrides: Partial<Job>): Job => ({
  id: "job-1",
  job_name: "backup",
  host_id: "host-a",
  started_at: "2026-11-27T09:00:00Z",
  finished_at: "2026-11-27T09:05:00Z",
  duration_seconds: 300,
  status: "success",
  exit_code: 0,
  ...overrides,
});

describe("latestValue", () => {
  it("reads the newest sample regardless of array order", () => {
    expect(latestValue([point("2026-11-27T10:00:10Z", 2), point("2026-11-27T10:00:00Z", 1)])).toBe(2);
  });

  it("returns null rather than zero for an empty series", () => {
    expect(latestValue([])).toBeNull();
  });

  it("skips a non-finite sample instead of propagating NaN", () => {
    expect(latestValue([point("2026-11-27T10:00:00Z", 1), point("2026-11-27T10:00:10Z", Number.NaN)])).toBe(1);
  });
});

describe("memorySnapshot", () => {
  it("prefers the hub's own used ratio over dividing bytes again", () => {
    const summary = emptySummary();
    summary.memory = [point("2026-11-27T10:00:00Z", 0.42)];
    summary.memory_used_bytes = [point("2026-11-27T10:00:00Z", 900)];
    summary.memory_total_bytes = [point("2026-11-27T10:00:00Z", 1000)];
    expect(memorySnapshot(summary).usedRatio).toBe(0.42);
  });

  it("falls back to the division when no ratio series arrived", () => {
    const summary = emptySummary();
    summary.memory_used_bytes = [point("2026-11-27T10:00:00Z", 250)];
    summary.memory_total_bytes = [point("2026-11-27T10:00:00Z", 1000)];
    expect(memorySnapshot(summary).usedRatio).toBe(0.25);
  });

  it("derives swap in use only when both halves arrived", () => {
    const summary = emptySummary();
    summary.memory_swap_total_bytes = [point("2026-11-27T10:00:00Z", 1000)];
    expect(memorySnapshot(summary).swapUsedBytes).toBeNull();
    summary.memory_swap_free_bytes = [point("2026-11-27T10:00:00Z", 600)];
    expect(memorySnapshot(summary).swapUsedBytes).toBe(400);
  });

  it("reports no memory at all as absent, never as zero", () => {
    const snapshot = memorySnapshot(emptySummary());
    expect(snapshot.usedRatio).toBeNull();
    expect(snapshot.usedBytes).toBeNull();
    expect(snapshot.swapTotalBytes).toBeNull();
  });
});

describe("storageSnapshot", () => {
  it("counts a device with no SMART verdict apart from a passing one", () => {
    const snapshot = storageSnapshot(diskInventory([
      { id: "/", name: "/", attrs: { device: "/dev/sda1", smart_status: "passed", capacity_bytes: "1000", used_bytes: "500" } },
      { id: "/srv", name: "/srv", attrs: { device: "/dev/sdb1", capacity_bytes: "1000", used_bytes: "100" } },
    ]));
    expect(snapshot.deviceCount).toBe(2);
    expect(snapshot.smart).toEqual({ passed: 1, failed: 0 });
    expect(snapshot.smartUnreported).toBe(1);
  });

  it("sums capacity once per device, not once per mountpoint", () => {
    const snapshot = storageSnapshot(diskInventory([
      { id: "/", name: "/", attrs: { device: "/dev/sda1", capacity_bytes: "1000", used_bytes: "400" } },
      { id: "/tmp", name: "/tmp", attrs: { device: "/dev/sda1", capacity_bytes: "1000", used_bytes: "400" } },
    ]));
    expect(snapshot.usage?.capacity).toBe(1000);
  });

  it("has no usage and no devices when the inventory never arrived", () => {
    const snapshot = storageSnapshot(null);
    expect(snapshot.usage).toBeNull();
    expect(snapshot.deviceCount).toBe(0);
  });
});

describe("pendingUpdateItems", () => {
  it("excludes the action-capability item and backports candidates", () => {
    const inventory: Inventory = {
      host_id: "host-a",
      kind: "package_update",
      reported_at: "2026-11-27T10:00:00Z",
      schema: 1,
      items: [
        { id: "package-actions", name: "package-actions", attrs: { apply_pending_package_updates: "true" } },
        { id: "curl", name: "curl", attrs: { installed_version: "8.5.0", candidate_version: "8.6.0" } },
        { id: "nginx", name: "nginx", attrs: { candidate_automatic: "false", candidate_version: "1.27" } },
      ],
    };
    expect(pendingUpdateItems(inventory).map((item) => item.id)).toEqual(["curl"]);
  });

  it("reports nothing for an inventory that never arrived", () => {
    expect(pendingUpdateItems(null)).toEqual([]);
  });
});

describe("packageVersions", () => {
  it("rejects a blank version rather than showing an empty cell", () => {
    expect(packageVersions({ id: "curl", name: "curl", attrs: { installed_version: "  ", candidate_version: "8.6.0" } })).toEqual({
      installed: null,
      candidate: "8.6.0",
      source: null,
    });
  });
});

describe("jobProgressRatio", () => {
  it("uses a reported total when the extractor published one", () => {
    expect(jobProgressRatio(job({ stats: { bytes_transferred: 500, bytes_total: 1000 } }))).toBe(0.5);
  });

  it("accepts the figures as strings, the way JSON stats often arrive", () => {
    expect(jobProgressRatio(job({ stats: { files_transferred: "3", files_total: "4" } }))).toBe(0.75);
  });

  it("refuses to guess a ratio from work already done", () => {
    // files_checked counts what was inspected, not what remains.
    expect(jobProgressRatio(job({ stats: { files_transferred: 20, files_checked: 100 } }))).toBeNull();
  });

  it("has no ratio without stats, and none from a zero total", () => {
    expect(jobProgressRatio(job({}))).toBeNull();
    expect(jobProgressRatio(job({ stats: { bytes_transferred: 0, bytes_total: 0 } }))).toBeNull();
  });

  it("clamps a tool that overshoots its own total", () => {
    expect(jobProgressRatio(job({ stats: { bytes_transferred: 1500, bytes_total: 1000 } }))).toBe(1);
  });
});

describe("splitJobs", () => {
  it("separates the run in progress from the finished history", () => {
    const running = job({ id: "job-running", status: "running", finished_at: "0001-01-01T00:00:00Z" });
    const done = job({ id: "job-done" });
    const { running: live, finished } = splitJobs([done, running]);
    expect(live.map((entry) => entry.id)).toEqual(["job-running"]);
    expect(finished.map((entry) => entry.id)).toEqual(["job-done"]);
  });
});
