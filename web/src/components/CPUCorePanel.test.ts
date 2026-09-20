import { describe, expect, it } from "vitest";
import { aggregateCPUPoints, groupCPUCores, readCPUPanelPreferences } from "./CPUCorePanel";

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
