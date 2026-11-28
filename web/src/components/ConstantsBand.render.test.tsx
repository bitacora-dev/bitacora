import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import type { Inventory, Summary } from "../api";
import { LocaleProvider } from "../i18n";
import ConstantsBand from "./ConstantsBand";

const point = (ts: string, value: number) => ({ ts, value });

const summary = (overrides: Partial<Summary> = {}): Summary => ({
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
  ...overrides,
});

const disks = (items: Inventory["items"]): Inventory => ({
  host_id: "host-a",
  kind: "disk",
  reported_at: "2026-11-27T10:00:00Z",
  schema: 1,
  items,
});

function render(node: Parameters<typeof renderToStaticMarkup>[0]): string {
  return renderToStaticMarkup(<LocaleProvider>{node}</LocaleProvider>);
}

describe("ConstantsBand", () => {
  it("says an unreported signal in words and never draws it as zero", () => {
    const html = render(<ConstantsBand summary={summary()} disks={null} temperature={null} powerWatts={null} />);
    expect(html).toContain("Sin muestras todavía");
    expect(html).toContain("El agente todavía no ha reportado discos");
    // No trace at all when there is nothing to trace.
    expect(html).not.toContain("sparkline");
    // A "0 %" here would assert an idle processor the hub never measured.
    expect(html).not.toContain(">0 %<");
  });

  it("draws the five cells with their window traces once data arrives", () => {
    const html = render(
      <ConstantsBand
        summary={summary({
          cpu: [point("2026-11-27T10:00:00Z", 0.2), point("2026-11-27T10:00:10Z", 0.25)],
          memory: [point("2026-11-27T10:00:10Z", 0.5)],
          memory_used_bytes: [point("2026-11-27T10:00:10Z", 500)],
          memory_total_bytes: [point("2026-11-27T10:00:10Z", 1000)],
          network_rx_bytes_per_second: [point("2026-11-27T10:00:10Z", 2048)],
          network_tx_bytes_per_second: [point("2026-11-27T10:00:10Z", 1024)],
        })}
        disks={disks([{ id: "/", name: "/", attrs: { device: "/dev/sda1", smart_status: "passed", capacity_bytes: "1000", used_bytes: "600" } }])}
        temperature={{ chip: "coretemp-isa-0000", sensor: "package_id_0", value: 46.5, package: true }}
        powerWatts={31.2}
      />,
    );
    expect(html.match(/class="band-cell"/g)).toHaveLength(5);
    expect(html).toContain("46,5 °C");
    expect(html).toContain("31,2 W");
    expect(html).toContain("1 disco");
    expect(html).toContain("1 con SMART correcto");
    expect(html).toContain("sparkline--cyan");
    expect(html).toContain("sparkline--green");
  });

  it("counts a disk with no SMART verdict as unreported, not as healthy", () => {
    const html = render(
      <ConstantsBand
        summary={summary()}
        disks={disks([{ id: "/", name: "/", attrs: { device: "/dev/sda1", capacity_bytes: "1000", used_bytes: "100" } }])}
        temperature={null}
        powerWatts={null}
      />,
    );
    expect(html).toContain("1 sin veredicto SMART");
    expect(html).not.toContain("con SMART correcto");
  });

  it("never passes a hottest-core reading off as the package temperature", () => {
    // web/DESIGN.md: a host with no package sensor shows its hottest core and
    // says so. The band sits right above the processor panel, which tags the
    // same number; a bare "46,5 °C" here would contradict it.
    const html = render(
      <ConstantsBand
        summary={summary()}
        disks={null}
        temperature={{ chip: "coretemp-isa-0000", sensor: "core_3", value: 46.5, package: false }}
        powerWatts={null}
      />,
    );
    expect(html).toContain("46,5 °C en el núcleo más caliente");
  });
});
