import { describe, expect, it } from "vitest";
import { aggregateCPUPoints, groupCPUCores, isolatedCPUCount, readCPUPanelPreferences } from "./CPUCorePanel";

describe("groupCPUCores", () => {
  it("groups hyperthread siblings by core_id without losing their individual series", () => {
    const groups = groupCPUCores([
      { cpu: "1", points: [{ ts: "2026-09-20T10:00:00Z", value: 0.2 }] },
      { cpu: "0", points: [{ ts: "2026-09-20T10:00:00Z", value: 0.8 }] },
      { cpu: "2", points: [{ ts: "2026-09-20T10:00:00Z", value: 0.4 }] },
    ], {
      host_id: "host-a", kind: "cpu_topology", reported_at: "2026-09-20T10:00:00Z", schema: 1,
      items: [
        { id: "cpu0", name: "cpu0", attrs: { core_id: "0", core_type: "p-core", online: "true" } },
        { id: "cpu1", name: "cpu1", attrs: { core_id: "0", core_type: "p-core", online: "true" } },
        { id: "cpu2", name: "cpu2", attrs: { core_id: "1", core_type: "e-core", online: "true" } },
      ],
    });

    expect(groups).toHaveLength(2);
    expect(groups[0].cpus.map((series) => series.cpu)).toEqual(["0", "1"]);
    expect(groups[0].type).toBe("p-core");
    expect(groups[1].cpus.map((series) => series.cpu)).toEqual(["2"]);
  });
});

describe("isolated CPUs", () => {
  const topology = (attrs: Record<string, Record<string, string>>) => ({
    host_id: "host-a", kind: "cpu_topology", reported_at: "2026-09-20T10:00:00Z", schema: 1,
    items: Object.entries(attrs).map(([id, value]) => ({ id, name: id, attrs: value })),
  });
  const series = (cpus: string[]) => cpus.map((cpu) => ({ cpu, points: [{ ts: "2026-09-20T10:00:00Z", value: 0 }] }));

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
      cpu1: { core_id: "1", core_type: "p-core", online: "true" },
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

describe("aggregateCPUPoints", () => {
  it("calculates the mean and the maximum for each selected interval", () => {
    const aggregates = aggregateCPUPoints([
      { ts: "2026-09-20T10:00:01Z", value: 0.2 },
      { ts: "2026-09-20T10:00:20Z", value: 0.8 },
      { ts: "2026-09-20T10:01:02Z", value: 0.4 },
    ], 60);
    expect(aggregates).toEqual([
      { ts: "2026-09-20T10:00:00.000Z", mean: 0.5, max: 0.8, count: 2 },
      { ts: "2026-09-20T10:01:00.000Z", mean: 0.4, max: 0.4, count: 1 },
    ]);
  });

  it("falls back to collapsed defaults when stored preferences are malformed", () => {
    const storage = { getItem: () => "not-json" } as unknown as Storage;
    expect(readCPUPanelPreferences(storage)).toEqual({ expanded: false, averagingWindowSeconds: 60 });
  });
});
