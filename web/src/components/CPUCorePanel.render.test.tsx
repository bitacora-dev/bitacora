import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import type { CPUSeries, Inventory } from "../api";
import { LocaleProvider } from "../i18n";
import CPUCorePanel from "./CPUCorePanel";

const topology = (attrs: Record<string, Record<string, string>>): Inventory => ({
  host_id: "host-a", kind: "cpu_topology", reported_at: "2026-10-08T10:00:00Z", schema: 1,
  items: Object.entries(attrs).map(([cpu, values]) => ({ id: `cpu${cpu}`, name: `cpu${cpu}`, attrs: values })),
});
const sampled = (values: Record<string, number>): CPUSeries[] => Object.entries(values).map(([cpu, value]) => ({ cpu, points: [{ ts: "2026-10-08T10:00:00Z", value }] }));

function render(cores: CPUSeries[], inventory: Inventory): string {
  return renderToStaticMarkup(<LocaleProvider><CPUCorePanel cores={cores} topology={inventory} identity={null} total={[]} temperatures={[]} generatedAt="2026-10-08T10:00:00Z" /></LocaleProvider>);
}

// One chunk per core row, starting right after `<li class="cpu-core-row`, so
// each chunk begins with its own modifier classes (or the closing quote).
const rows = (html: string) => html.split('<li class="cpu-core-row').slice(1);

describe("CPUCorePanel", () => {
  it("dims a switched-off core and says each of its threads is off, even without samples", () => {
    const html = render(sampled({ 0: 0.25, 1: 0.1 }), topology({
      0: { core_id: "0", core_type: "p-core", online: "true" },
      1: { core_id: "0", core_type: "p-core", online: "true" },
      8: { core_id: "16", core_type: "p-core", online: "false", core_id_inferred: "true" },
      9: { core_id: "16", core_type: "p-core", online: "false", core_id_inferred: "true" },
    }));

    const [running, offline] = rows(html);
    expect(running.startsWith('"')).toBe(true);
    expect(offline.startsWith(' cpu-core-row--offline"')).toBe(true);
    // Labelled by its lowest CPU, never by the reconstructed core id 16.
    expect(offline).toContain("<strong>CPU 8</strong>");
    expect(offline).not.toContain("CPU 16");
    expect(offline).toContain("Apagada");
    expect(offline.match(/cpu-thread-row--offline/g)).toHaveLength(2);
    expect(offline).toContain("La CPU 8 está apagada");
    expect(offline).toContain("La CPU 9 está apagada");
    expect(offline).not.toContain("sin muestras");
    expect(html).toContain("2 CPU apagadas");
  });

  it("shows hyperthread siblings on the core's row and a downed sibling only on its thread", () => {
    const html = render(sampled({ 4: 0.156, 5: 0.06 }), topology({
      4: { core_id: "8", core_type: "p-core", online: "true" },
      5: { core_id: "8", core_type: "p-core", online: "false" },
    }));

    const [row] = rows(html);
    expect(row.startsWith('"')).toBe(true);
    expect(row).toContain("<strong>CPU 4</strong>");
    expect(row).toContain("HT 5");
    expect(row.match(/cpu-thread-row--offline/g)).toHaveLength(1);
    expect(row).toContain("La CPU 5 está apagada");
    expect(row).toContain("15,6");
  });

  it("still says there is no per-core load when no CPU reports any", () => {
    const html = render([], topology({ 0: { core_id: "0", online: "false" } }));
    expect(rows(html)).toHaveLength(0);
    expect(html).toContain("El agente todavía no ha reportado carga por núcleo.");
  });
});
