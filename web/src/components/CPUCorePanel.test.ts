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

describe("offline CPUs", () => {
  // The real icloudserver layout: eight SMT P-cores numbered 0,4,8,...,28
  // over cpu0..cpu15, then single-thread E-cores from core 32 over
  // cpu16..cpu31. cpu8 and cpu9 are the two threads of core 16, offlined
  // because that P-core is degraded.
  const raptorLake = (offline: string[]) => ({
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
  const series = (cpus: string[]) => cpus.map((cpu) => ({ cpu, points: [{ ts: "2026-10-08T10:00:00Z", value: 0.1 }] }));

  it("keeps an offline core's threads together and off every other core", () => {
    const groups = groupCPUCores(series(["4", "5", "8", "9", "10", "11"]), raptorLake(["8", "9"]));

    const offline = groups.filter((group) => !group.online);
    expect(offline).toHaveLength(1);
    expect(offline[0].id).toBe("16");
    expect(offline[0].cpus.map((cpu) => cpu.cpu)).toEqual(["8", "9"]);
    expect(offline[0].type).toBe("p-core");

    // cpu4 and cpu5 are alive at 15.6% and 6%: their core is core 8, which
    // is also the number cpu8 carries. It must stay online and keep exactly
    // its own two threads.
    const core8 = groups.find((group) => group.id === "8" && group.online);
    expect(core8?.cpus.map((cpu) => cpu.cpu)).toEqual(["4", "5"]);
    expect(core8?.offline).toEqual([]);
  });

  it("draws an offline core even once its last samples have aged out", () => {
    const groups = groupCPUCores(series(["4", "5"]), raptorLake(["8", "9"]));

    const core16 = groups.find((group) => group.id === "16");
    expect(core16?.online).toBe(false);
    expect(core16?.cpus.map((cpu) => cpu.cpu)).toEqual(["8", "9"]);
    expect(core16?.cpus.every((cpu) => cpu.points.length === 0)).toBe(true);
  });

  it("marks a single downed thread on the thread, not on its running core", () => {
    const groups = groupCPUCores(series(["4", "5"]), raptorLake(["5"]));

    const core8 = groups.find((group) => group.id === "8");
    expect(core8?.online).toBe(true);
    expect(core8?.offline).toEqual(["5"]);
  });

  it("gives every group its own render key even when two share an id", () => {
    // A CPU with no topology row at all falls back to its own number as id,
    // which is also some other core's core_id. Distinct keys are what keeps
    // the two from being drawn as one card.
    const groups = groupCPUCores(series(["4", "5", "8"]), {
      host_id: "host-a", kind: "cpu_topology", reported_at: "2026-10-08T10:00:00Z", schema: 1,
      items: [
        { id: "cpu4", name: "cpu4", attrs: { core_id: "8", core_type: "p-core", online: "true" } },
        { id: "cpu5", name: "cpu5", attrs: { core_id: "8", core_type: "p-core", online: "true" } },
      ],
    });

    expect(groups.map((group) => group.id)).toEqual(["8", "8"]);
    expect(new Set(groups.map((group) => group.key)).size).toBe(2);
    expect(groups[0].cpus.map((cpu) => cpu.cpu)).toEqual(["4", "5"]);
    expect(groups[1].cpus.map((cpu) => cpu.cpu)).toEqual(["8"]);
  });

  it("orders cores by their lowest thread so a reconstructed core stays in place", () => {
    const groups = groupCPUCores(series(["0", "1", "10", "11"]), raptorLake(["8", "9"]));

    // 24 cores: 8 P + 16 E, in thread order, with the offlined one between
    // its neighbours rather than wherever its core id happens to sort.
    expect(groups).toHaveLength(24);
    expect(groups.slice(0, 8).map((group) => group.id)).toEqual(["0", "4", "8", "12", "16", "20", "24", "28"]);
    expect(groups[8].id).toBe("32");
    expect(groups.map((group) => group.cpus[0].cpu)).toEqual(
      Array.from({ length: 8 }, (_unused, core) => String(core * 2)).concat(Array.from({ length: 16 }, (_unused, core) => String(16 + core))),
    );
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
