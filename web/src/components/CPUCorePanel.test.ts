import { describe, expect, it } from "vitest";
import { groupCPUCores } from "./CPUCorePanel";

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
