import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import type { Inventory } from "../api";
import { LocaleProvider } from "../i18n";
import DiskArrayPanel from "./DiskArrayPanel";

const inventory = (items: Inventory["items"]): Inventory => ({ host_id: "host-a", kind: "disk", reported_at: "2026-10-08T10:00:00Z", schema: 1, items });

describe("DiskArrayPanel", () => {
  it("flags a nearly full disk on its usage while the health light stays on SMART", () => {
    const html = renderToStaticMarkup(<LocaleProvider><DiskArrayPanel inventory={inventory([
      { id: "/", name: "/", attrs: { device: "/dev/sda1", smart_status: "passed", capacity_bytes: "1000", used_bytes: "950", available_bytes: "50" } },
    ])} /></LocaleProvider>);

    expect(html).toContain("disk-status-light--ok");
    expect(html).toContain("Correcto");
    expect(html).toContain('class="disk-usage-warning">Casi lleno<');
    expect(html).toContain("usage-bar--nearly-full");
  });

  it("does not flag usage below the threshold", () => {
    const html = renderToStaticMarkup(<LocaleProvider><DiskArrayPanel inventory={inventory([
      { id: "/", name: "/", attrs: { device: "/dev/sda1", smart_status: "passed", capacity_bytes: "1000", used_bytes: "400", available_bytes: "600" } },
    ])} /></LocaleProvider>);

    expect(html).not.toContain("Casi lleno");
  });
});
