import { describe, expect, it } from "vitest";
import { cpuTemperature, groupCPUCores, isolatedCPUCount, offlineCPUCount, powerWatts, readCPUPanelPreferences, severity, totalLoadSeries, windowStats } from "./CPUCorePanel";
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
    expect(groups[0].online).toBe(false);
    expect(offlineCPUCount(groups)).toBe(1);
  });

  it("does not read an absent online attribute as offline", () => {
    const groups = groupCPUCores(series(["0"]), topology({
      cpu0: { core_id: "0", core_type: "p-core" },
    }));

    expect(groups[0].offline).toEqual([]);
    expect(groups[0].online).toBe(true);
    expect(offlineCPUCount(groups)).toBe(0);
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
