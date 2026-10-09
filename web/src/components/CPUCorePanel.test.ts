import { describe, expect, it } from "vitest";
import type { Inventory } from "../api";
import { cpuTemperature, groupCPUCores, isolatedCPUCount, offlineCPUCount, powerWatts, readCPUPanelPreferences, severity, totalLoadSeries, windowStats, withTopologyOnlyCPUs } from "./CPUCorePanel";
import { sparklineData } from "./CPULoadSparkline";

const topology = (attrs: Record<string, Record<string, string>>) => ({
  host_id: "host-a", kind: "cpu_topology", reported_at: "2026-09-20T10:00:00Z", schema: 1,
  items: Object.entries(attrs).map(([id, value]) => ({ id, name: id, attrs: value })),
});
const series = (cpus: string[]) => cpus.map((cpu) => ({ cpu, points: [{ ts: "2026-09-20T10:00:00Z", value: 0 }] }));

describe("groupCPUCores", () => {
  it("groups hyperthread siblings by core_id without losing their individual series", () => {
    const groups = groupCPUCores([
      { cpu: "1", points: [{ ts: "2026-09-20T10:00:00Z", value: 0.2 }] },
      { cpu: "0", points: [{ ts: "2026-09-20T10:00:00Z", value: 0.8 }] },
      { cpu: "2", points: [{ ts: "2026-09-20T10:00:00Z", value: 0.4 }] },
    ], topology({
      cpu0: { core_id: "0", core_type: "p-core", online: "true" },
      cpu1: { core_id: "0", core_type: "p-core", online: "true" },
      cpu2: { core_id: "1", core_type: "e-core", online: "true" },
    }));

    expect(groups).toHaveLength(2);
    expect(groups[0].cpus.map((entry) => entry.cpu)).toEqual(["0", "1"]);
    expect(groups[0].type).toBe("p-core");
    expect(groups[1].cpus.map((entry) => entry.cpu)).toEqual(["2"]);
  });
});

describe("isolated CPUs", () => {
  it("keeps the kernel's reserved CPUs attached to the core that owns them", () => {
    const groups = groupCPUCores(series(["0", "1", "2"]), topology({
      cpu0: { core_id: "0", core_type: "p-core", online: "true", isolated: "false" },
      cpu1: { core_id: "1", core_type: "p-core", online: "true", isolated: "true" },
      cpu2: { core_id: "2", core_type: "p-core", online: "true", isolated: "true" },
    }));

    expect(groups.map((group) => group.isolated)).toEqual([[], ["1"], ["2"]]);
    expect(isolatedCPUCount(groups)).toBe(2);
  });

  it("reports nothing isolated when the kernel exposes no authoritative list", () => {
    const groups = groupCPUCores(series(["0", "1"]), topology({
      cpu0: { core_id: "0", core_type: "p-core", online: "true" },
      cpu1: { core_id: "0", core_type: "p-core", online: "true" },
    }));

    expect(isolatedCPUCount(groups)).toBe(0);
  });

  it("counts every isolated thread of a hyperthreaded core, not the core once", () => {
    const groups = groupCPUCores(series(["0", "1"]), topology({
      cpu0: { core_id: "0", core_type: "p-core", online: "true", isolated: "true" },
      cpu1: { core_id: "0", core_type: "p-core", online: "true", isolated: "true" },
    }));

    expect(groups).toHaveLength(1);
    expect(groups[0].isolated).toEqual(["0", "1"]);
    expect(isolatedCPUCount(groups)).toBe(2);
  });
});

describe("offline CPUs", () => {
  it("records which thread is offline instead of only dimming the whole core", () => {
    const groups = groupCPUCores(series(["0", "1"]), topology({
      cpu0: { core_id: "0", core_type: "p-core", online: "true" },
      cpu1: { core_id: "0", core_type: "p-core", online: "false" },
    }));

    expect(groups).toHaveLength(1);
    expect(groups[0].offline).toEqual(["1"]);
    expect(groups[0].cpus.map((cpu) => cpu.cpu)).toEqual(["0", "1"]);
    expect(offlineCPUCount(groups)).toBe(1);
  });

  it("does not read an absent online attribute as offline", () => {
    const groups = groupCPUCores(series(["0"]), topology({
      cpu0: { core_id: "0", core_type: "p-core" },
    }));

    expect(groups[0].offline).toEqual([]);
    expect(offlineCPUCount(groups)).toBe(0);
  });
});

// Ported from the card layout's offline-CPU model (task #1297): the real
// icloudserver layout has eight SMT P-cores numbered 0,4,8,...,28 over
// cpu0..cpu15, then single-thread E-cores from core 32 over cpu16..cpu31.
// cpu8 and cpu9 are the two threads of core 16, offlined because that P-core
// is degraded; the agent reconstructs their core and marks it inferred.
describe("offline CPUs on icloudserver's layout", () => {
  const raptorLake = (offline: string[]): Inventory => ({
    host_id: "host-a", kind: "cpu_topology", reported_at: "2026-10-08T10:00:00Z", schema: 1,
    items: Array.from({ length: 32 }, (_unused, cpu) => ({
      id: `cpu${cpu}`,
      name: `cpu${cpu}`,
      attrs: {
        core_id: String(cpu < 16 ? 4 * Math.floor(cpu / 2) : 16 + cpu),
        core_type: cpu < 16 ? "p-core" : "e-core",
        online: offline.includes(String(cpu)) ? "false" : "true",
        ...(offline.includes(String(cpu)) ? { core_id_inferred: "true" } : {}),
      },
    })),
  });
  const sampled = (cpus: string[]) => cpus.map((cpu) => ({ cpu, points: [{ ts: "2026-10-08T10:00:00Z", value: 0.1 }] }));
  const allOffline = (group: { cpus: { cpu: string }[]; offline: string[] }) => group.offline.length === group.cpus.length;

  it("keeps an offline core's threads together and off every other core", () => {
    const groups = groupCPUCores(sampled(["4", "5", "8", "9", "10", "11"]), raptorLake(["8", "9"]));

    const offline = groups.filter(allOffline);
    expect(offline).toHaveLength(1);
    expect(offline[0].id).toBe("16");
    expect(offline[0].cpus.map((cpu) => cpu.cpu)).toEqual(["8", "9"]);
    expect(offline[0].type).toBe("p-core");

    // cpu4 and cpu5 run on core 8, which is also the number cpu8 carries.
    // They must keep exactly their own two threads and stay running.
    const core8 = groups.find((group) => group.key === "core-8");
    expect(core8?.cpus.map((cpu) => cpu.cpu)).toEqual(["4", "5"]);
    expect(core8?.offline).toEqual([]);
  });

  it("draws an offline core even once its last samples have aged out", () => {
    const groups = groupCPUCores(sampled(["4", "5"]), raptorLake(["8", "9"]));

    const core16 = groups.find((group) => group.key === "core-16");
    expect(core16 && allOffline(core16)).toBe(true);
    expect(core16?.cpus.map((cpu) => cpu.cpu)).toEqual(["8", "9"]);
    expect(core16?.cpus.every((cpu) => cpu.points.length === 0)).toBe(true);
    expect(offlineCPUCount(groups)).toBe(2);
  });

  it("marks a single downed thread on the thread, not on its running core", () => {
    const groups = groupCPUCores(sampled(["4", "5"]), raptorLake(["5"]));

    const core8 = groups.find((group) => group.key === "core-8");
    expect(core8?.offline).toEqual(["5"]);
    expect(core8 && allOffline(core8)).toBe(false);
  });

  it("gives every group its own render key even when two share an id", () => {
    // A CPU with no core_id at all (one the agent could not place) falls back
    // to its own number as id, which is also some other core's core_id.
    const groups = groupCPUCores(sampled(["4", "5", "8"]), {
      host_id: "host-a", kind: "cpu_topology", reported_at: "2026-10-08T10:00:00Z", schema: 1,
      items: [
        { id: "cpu4", name: "cpu4", attrs: { core_id: "8", core_type: "p-core", online: "true" } },
        { id: "cpu5", name: "cpu5", attrs: { core_id: "8", core_type: "p-core", online: "true" } },
        { id: "cpu8", name: "cpu8", attrs: { core_type: "unknown", online: "false" } },
      ],
    });

    expect(groups.map((group) => group.id)).toEqual(["8", "8"]);
    expect(groups.map((group) => group.key)).toEqual(["core-8", "cpu-8"]);
    expect(groups[1].offline).toEqual(["8"]);
  });

  it("orders cores by their lowest thread so a reconstructed core stays in place", () => {
    const groups = groupCPUCores(sampled(["0", "1", "10", "11"]), raptorLake(["8", "9"]));

    expect(groups).toHaveLength(24);
    expect(groups.slice(0, 8).map((group) => group.id)).toEqual(["0", "4", "8", "12", "16", "20", "24", "28"]);
    expect(groups.map((group) => group.cpus[0].cpu)).toEqual(
      Array.from({ length: 8 }, (_unused, core) => String(core * 2)).concat(Array.from({ length: 16 }, (_unused, core) => String(16 + core))),
    );
  });
});

describe("withTopologyOnlyCPUs", () => {
  const known = new Map([
    ["0", { id: "cpu0", name: "cpu0", attrs: { core_id: "0", online: "true" } }],
    ["1", { id: "cpu1", name: "cpu1", attrs: { core_id: "0", online: "false" } }],
  ]);

  it("draws nothing when no CPU reports load at all", () => {
    const empty: { cpu: string; points: { ts: string; value: number }[] }[] = [];
    expect(withTopologyOnlyCPUs(empty, known)).toBe(empty);
    expect(groupCPUCores([], topology({ cpu0: { core_id: "0", online: "false" } }))).toEqual([]);
  });

  it("adds an empty series for every topology CPU the metrics lack", () => {
    const reporting = [{ cpu: "0", points: [{ ts: "2026-10-08T10:00:00Z", value: 0.5 }] }];
    expect(withTopologyOnlyCPUs(reporting, known)).toEqual([...reporting, { cpu: "1", points: [] }]);
  });
});

describe("windowStats", () => {
  it("averages a trailing window anchored on the newest sample", () => {
    const stats = windowStats([
      { ts: "2026-09-20T10:00:00Z", value: 0.1 },
      { ts: "2026-09-20T10:00:40Z", value: 0.2 },
      { ts: "2026-09-20T10:00:50Z", value: 0.8 },
      { ts: "2026-09-20T10:01:00Z", value: 0.6 },
    ], 30);

    expect(stats).toEqual({ mean: (0.2 + 0.8 + 0.6) / 3, max: 0.8, latest: 0.6, count: 3 });
  });

  it("keeps the selected window honest right after a clock-minute boundary", () => {
    // The old fixed-bucket average reset at every minute, so a sample at
    // 10:01:02 was reported as the "30 s average" on its own.
    const stats = windowStats([
      { ts: "2026-09-20T10:00:42Z", value: 1 },
      { ts: "2026-09-20T10:00:52Z", value: 1 },
      { ts: "2026-09-20T10:01:02Z", value: 0 },
    ], 30);

    expect(stats?.count).toBe(3);
    expect(stats?.mean).toBeCloseTo(2 / 3);
  });

  it("ignores unusable samples and reports absence rather than zero", () => {
    expect(windowStats([], 30)).toBeNull();
    expect(windowStats([{ ts: "not-a-date", value: 0.5 }], 30)).toBeNull();
    expect(windowStats([{ ts: "2026-09-20T10:00:00Z", value: Number.NaN }], 30)).toBeNull();
  });
});

describe("totalLoadSeries", () => {
  it("uses the host-wide series the hub already computed", () => {
    const total = [{ ts: "2026-09-20T10:00:00Z", value: 0.42 }];
    expect(totalLoadSeries(total, series(["0", "1"]))).toBe(total);
  });

  it("falls back to the per-core mean at each timestamp", () => {
    const points = totalLoadSeries([], [
      { cpu: "0", points: [{ ts: "2026-09-20T10:00:10Z", value: 0.8 }, { ts: "2026-09-20T10:00:00Z", value: 0.2 }] },
      { cpu: "1", points: [{ ts: "2026-09-20T10:00:10Z", value: 0.4 }, { ts: "2026-09-20T10:00:00Z", value: 0.6 }] },
    ]);

    expect(points).toEqual([
      { ts: "2026-09-20T10:00:00Z", value: 0.4 },
      { ts: "2026-09-20T10:00:10Z", value: 0.6000000000000001 },
    ]);
  });

  it("reports no total load at all when nothing was sampled", () => {
    expect(totalLoadSeries([], [])).toEqual([]);
  });

  // The aggregate path above returns the caller's own array, so the sparkline
  // sees a stable `points` prop between the panel's one-second ticks. The
  // fallback cannot: it derives a new array on every call. That is why the
  // panel memoises the result, and this test records the difference so nobody
  // removes the memo after reading only the aggregate case.
  it("derives a fresh array on the fallback path, which the caller must memoise", () => {
    const cores = [{ cpu: "0", points: [{ ts: "2026-09-20T10:00:00Z", value: 0.5 }] }];
    const first = totalLoadSeries([], cores);
    const second = totalLoadSeries([], cores);
    expect(second).toEqual(first);
    expect(second).not.toBe(first);
  });
});

describe("cpuTemperature", () => {
  it("prefers the package sensor over the individual cores", () => {
    expect(cpuTemperature([
      { chip: "coretemp", sensor: "core_0", points: [{ ts: "2026-09-20T10:00:00Z", value: 71 }] },
      { chip: "coretemp", sensor: "package_id_0", points: [{ ts: "2026-09-20T10:00:00Z", value: 55 }] },
    ])).toEqual({ chip: "coretemp", sensor: "package_id_0", value: 55, package: true });
  });

  it("falls back to the hottest core and says so when no package sensor reported", () => {
    expect(cpuTemperature([
      { chip: "k10temp", sensor: "core_0", points: [{ ts: "2026-09-20T10:00:00Z", value: 48 }] },
      { chip: "k10temp", sensor: "core_1", points: [{ ts: "2026-09-20T10:00:00Z", value: 62 }] },
    ])).toEqual({ chip: "k10temp", sensor: "core_1", value: 62, package: false });
  });

  it("reports absence rather than a zero reading", () => {
    expect(cpuTemperature([])).toBeNull();
    expect(cpuTemperature([{ chip: "coretemp", sensor: "package_id_0", points: [] }])).toBeNull();
  });
});

describe("severity", () => {
  it("escalates only at the documented thresholds", () => {
    expect(severity(0)).toBe("low");
    expect(severity(0.49)).toBe("low");
    expect(severity(0.5)).toBe("moderate");
    expect(severity(0.75)).toBe("high");
    expect(severity(0.9)).toBe("critical");
  });
});

describe("readCPUPanelPreferences", () => {
  it("falls back to the 30 second window when stored preferences are malformed", () => {
    const storage = { getItem: () => "not-json" } as unknown as Storage;
    expect(readCPUPanelPreferences(storage)).toEqual({ averagingWindowSeconds: 30 });
  });

  it("keeps the window chosen under the previous card layout", () => {
    const storage = { getItem: () => JSON.stringify({ expanded: true, averagingWindowSeconds: 300 }) } as unknown as Storage;
    expect(readCPUPanelPreferences(storage)).toEqual({ averagingWindowSeconds: 300 });
  });

  it("rejects a window the selector does not offer", () => {
    const storage = { getItem: () => JSON.stringify({ averagingWindowSeconds: 7 }) } as unknown as Storage;
    expect(readCPUPanelPreferences(storage)).toEqual({ averagingWindowSeconds: 30 });
  });
});

describe("powerWatts", () => {
  it("reads a formatted RAPL draw", () => {
    expect(powerWatts("29.74")).toBe(29.74);
  });

  it("reports a measured zero as a reading", () => {
    expect(powerWatts("0.00")).toBe(0);
  });

  it("rejects an absent or blank reading instead of drawing it as no power", () => {
    expect(powerWatts(undefined)).toBeNull();
    expect(powerWatts("")).toBeNull();
    expect(powerWatts("   ")).toBeNull();
  });

  it("rejects a reading it cannot describe", () => {
    expect(powerWatts("not-a-number")).toBeNull();
    expect(powerWatts("-3.5")).toBeNull();
  });
});

describe("sparklineData", () => {
  it("sorts samples by time and drops the unusable ones", () => {
    const seconds = (ts: string) => Math.floor(new Date(ts).getTime() / 1000);
    expect(sparklineData([
      { ts: "2026-09-20T10:00:10Z", value: 0.4 },
      { ts: "not-a-date", value: 0.9 },
      { ts: "2026-09-20T10:00:00Z", value: 0.2 },
    ])).toEqual([
      [seconds("2026-09-20T10:00:00Z"), seconds("2026-09-20T10:00:10Z")],
      [0.2, 0.4],
    ]);
  });
});
