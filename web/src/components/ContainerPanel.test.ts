import { describe, expect, it } from "vitest";
import type { ContainerSeries } from "../api";
import { readContainerPanelPreferences, summariseContainers } from "./ContainerPanel";

function container(name: string, cpu: [string, number][], memory: [string, number][]): ContainerSeries {
  return {
    container_id: name.slice(0, 12).padEnd(12, "0"),
    container_name: name,
    cpu_cores_used: cpu.map(([ts, value]) => ({ ts, value })),
    memory_bytes: memory.map(([ts, value]) => ({ ts, value })),
  };
}

const T1 = "2026-09-21T10:00:30.000Z";
const T2 = "2026-09-21T10:01:00.000Z";

describe("summariseContainers", () => {
  it("keeps one row per container instead of merging them into a single reading", () => {
    const { rows } = summariseContainers([
      container("postgres", [[T1, 0.5], [T2, 0.6]], [[T1, 500], [T2, 520]]),
      container("traefik", [[T1, 0.1], [T2, 0.05]], [[T1, 60], [T2, 61]]),
    ]);

    expect(rows).toHaveLength(2);
    expect(rows.map((row) => row.container.container_name)).toEqual(["postgres", "traefik"]);
    expect(rows.map((row) => row.cpuCoresUsed)).toEqual([0.6, 0.05]);
    expect(rows.map((row) => row.memoryBytes)).toEqual([520, 61]);
  });

  it("puts the busiest container first, because that is the question the panel answers", () => {
    const { rows } = summariseContainers([
      container("idle", [[T2, 0.01]], [[T2, 10]]),
      container("hungry", [[T2, 2.5]], [[T2, 4000]]),
      container("middling", [[T2, 0.4]], [[T2, 300]]),
    ]);

    expect(rows.map((row) => row.container.container_name)).toEqual(["hungry", "middling", "idle"]);
  });

  it("totals the header from the per-container rates the hub already derived", () => {
    const { activeCount, totalCPUCoresUsed, totalMemoryBytes } = summariseContainers([
      container("a", [[T2, 0.5]], [[T2, 100]]),
      container("b", [[T2, 0.25]], [[T2, 200]]),
    ]);

    expect(activeCount).toBe(2);
    expect(totalCPUCoresUsed).toBe(0.75);
    expect(totalMemoryBytes).toBe(300);
  });

  it("excludes a container that stopped reporting from the totals rather than counting it as zero", () => {
    const summary = summariseContainers([
      container("running", [[T1, 0.2], [T2, 0.3]], [[T1, 100], [T2, 110]]),
      // Stopped after T1: it has no sample in the most recent cycle at all.
      container("stopped", [[T1, 1.5]], [[T1, 900]]),
    ]);

    const [running, stopped] = summary.rows;
    expect(running.container.container_name).toBe("running");
    expect(running.active).toBe(true);
    expect(stopped.container.container_name).toBe("stopped");
    expect(stopped.active).toBe(false);

    expect(summary.activeCount).toBe(1);
    // 0.3, not 1.8: the stopped container's last known rate is history, and it
    // is not zero either — it is simply no longer part of "now".
    expect(summary.totalCPUCoresUsed).toBe(0.3);
    expect(summary.totalMemoryBytes).toBe(110);
  });

  it("sorts a stopped container below every running one whatever its last rate was", () => {
    const { rows } = summariseContainers([
      container("stopped-but-was-busy", [[T1, 9]], [[T1, 9000]]),
      container("running-and-quiet", [[T2, 0.01]], [[T2, 10]]),
    ]);

    expect(rows.map((row) => row.container.container_name)).toEqual(["running-and-quiet", "stopped-but-was-busy"]);
  });

  it("reports a just-started container as active with no rate yet, not as idle at zero", () => {
    // One counter sample cannot be differentiated, so the hub sends no rate
    // point; memory, a gauge, arrives immediately.
    const { rows, activeCount, totalCPUCoresUsed } = summariseContainers([
      container("running", [[T1, 0.2], [T2, 0.3]], [[T1, 100], [T2, 110]]),
      container("just-started", [], [[T2, 50]]),
    ]);

    const started = rows.find((row) => row.container.container_name === "just-started");
    expect(started?.active).toBe(true);
    expect(started?.cpuCoresUsed).toBeNull();
    expect(activeCount).toBe(2);
    expect(totalCPUCoresUsed).toBe(0.3);
  });

  it("answers a host with no containers with nothing to show, not with zeroes", () => {
    const summary = summariseContainers([]);

    expect(summary.rows).toEqual([]);
    expect(summary.activeCount).toBe(0);
    expect(summary.totalCPUCoresUsed).toBeNull();
    expect(summary.totalMemoryBytes).toBeNull();
  });

  it("scales the meters against a full core and the largest container in view", () => {
    const quiet = summariseContainers([container("a", [[T2, 0.25]], [[T2, 100]])]);
    // Nothing reaches a core, so the meter still reads against one whole core
    // rather than making a quarter-core container look saturated.
    expect(quiet.cpuMeterCeiling).toBe(1);
    expect(quiet.memoryMeterCeiling).toBe(100);

    const busy = summariseContainers([
      container("a", [[T2, 3.5]], [[T2, 100]]),
      container("b", [[T2, 0.25]], [[T2, 900]]),
    ]);
    expect(busy.cpuMeterCeiling).toBe(3.5);
    expect(busy.memoryMeterCeiling).toBe(900);
  });

  it("ignores malformed samples instead of rendering NaN", () => {
    const { rows, totalCPUCoresUsed } = summariseContainers([
      container("broken", [[T2, Number.NaN]], [["not-a-date", 10]]),
      container("fine", [[T2, 0.5]], [[T2, 20]]),
    ]);

    const broken = rows.find((row) => row.container.container_name === "broken");
    expect(broken?.cpuCoresUsed).toBeNull();
    expect(broken?.memoryBytes).toBeNull();
    expect(totalCPUCoresUsed).toBe(0.5);
  });
});

describe("readContainerPanelPreferences", () => {
  it("starts collapsed, because dozens of containers must not become a wall", () => {
    const storage = { getItem: () => null } as unknown as Storage;
    expect(readContainerPanelPreferences(storage)).toEqual({ expanded: false });
  });

  it("falls back to collapsed when stored preferences are malformed", () => {
    const storage = { getItem: () => "not-json" } as unknown as Storage;
    expect(readContainerPanelPreferences(storage)).toEqual({ expanded: false });
  });

  it("restores an operator's expanded choice", () => {
    const storage = { getItem: () => JSON.stringify({ expanded: true }) } as unknown as Storage;
    expect(readContainerPanelPreferences(storage)).toEqual({ expanded: true });
  });
});
