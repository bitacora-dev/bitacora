import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { LocaleProvider } from "../i18n";
import { CPUCoreCard, groupCPUCores } from "./CPUCorePanel";

const topology = (attrs: Record<string, Record<string, string>>) => ({
  host_id: "host-a", kind: "cpu_topology", reported_at: "2026-10-08T10:00:00Z", schema: 1,
  items: Object.entries(attrs).map(([cpu, values]) => ({ id: `cpu${cpu}`, name: `cpu${cpu}`, attrs: values })),
});

function renderCard(groupIndex: number, series: { cpu: string; points: { ts: string; value: number }[] }[], inventory: ReturnType<typeof topology>): string {
  const group = groupCPUCores(series, inventory)[groupIndex];
  return renderToStaticMarkup(<LocaleProvider><CPUCoreCard group={group} averagingWindowSeconds={60} /></LocaleProvider>);
}

describe("CPUCoreCard", () => {
  const sampled = [{ cpu: "0", points: [{ ts: "2026-10-08T10:00:00Z", value: 0.25 }] }];

  it("says every thread of a switched-off core is off, not waiting for samples", () => {
    const html = renderCard(1, sampled, topology({
      0: { core_id: "0", core_type: "p-core", online: "true" },
      8: { core_id: "16", core_type: "p-core", online: "false", core_id_inferred: "true" },
      9: { core_id: "16", core_type: "p-core", online: "false", core_id_inferred: "true" },
    }));

    expect(html).toContain("cpu-core--offline");
    expect(html).toContain("CPU 8, 9 (apagada)");
    // The reconstructed core id is never shown as a core number.
    expect(html).not.toContain("CPU 16");
    expect(html.match(/>Apagada</g)).toHaveLength(2);
    expect(html).not.toContain("Sin muestras todavía");
  });

  it("labels a CPU the agent could not place by its own number", () => {
    const html = renderCard(1, sampled, topology({
      0: { core_id: "0", core_type: "p-core", online: "true" },
      30: { core_type: "unknown", online: "false" },
    }));

    expect(html).toContain("CPU 30 (apagada)");
    expect(html).not.toContain("CPU 48");
  });

  it("keeps the kernel's core number on a switched-off core it reported", () => {
    const html = renderCard(1, sampled, topology({
      0: { core_id: "0", core_type: "p-core", online: "true" },
      2: { core_id: "4", core_type: "p-core", online: "false" },
    }));

    expect(html).toContain("CPU 4");
    expect(html).toContain("cpu-core-offline");
  });

  it("still says a running thread with no samples yet is waiting for them", () => {
    const html = renderCard(0, sampled, topology({
      0: { core_id: "0", core_type: "p-core", online: "true" },
      1: { core_id: "0", core_type: "p-core", online: "true" },
    }));

    expect(html).toContain("Sin muestras todavía");
    expect(html).not.toContain("cpu-core--offline");
  });
});
