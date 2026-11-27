import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import type { Inventory } from "../api";
import { LocaleProvider } from "../i18n";
import PackageUpdatePanel from "./PackageUpdatePanel";

const inventory = (items: Inventory["items"]): Inventory => ({
  host_id: "host-a",
  kind: "package_update",
  reported_at: new Date().toISOString(),
  schema: 1,
  items,
});

const render = (inv: Inventory | null, secondFactorAvailable = true) =>
  renderToStaticMarkup(
    <LocaleProvider>
      <PackageUpdatePanel hostID="host-a" inventory={inv} secondFactorAvailable={secondFactorAvailable} onRefreshInventory={async () => null} />
    </LocaleProvider>,
  );

describe("PackageUpdatePanel", () => {
  it("shows one row per package with both versions", () => {
    const html = render(inventory([
      { id: "curl", name: "curl", attrs: { installed_version: "8.5.0", candidate_version: "8.6.0", source: "apt" } },
    ]));
    expect(html.match(/class="package-row"/g)).toHaveLength(1);
    expect(html).toContain("8.5.0");
    expect(html).toContain("8.6.0");
    expect(html).toContain("apt");
  });

  it("says an unreported version in words rather than leaving the cell empty", () => {
    const html = render(inventory([{ id: "curl", name: "curl", attrs: { candidate_version: "8.6.0" } }]));
    expect(html).toContain("sin reportar");
  });

  it("offers one control for the whole plan and no per-package button", () => {
    // ADR-0022 authorises two fixed, argument-free operations: a package name
    // has no path from this screen to the agent, so a per-row "update" button
    // could only ever apply everything while pretending to be specific.
    const html = render(inventory([
      { id: "package-actions", name: "package-actions", attrs: { apply_pending_package_updates: "true", refresh_package_cache: "true", cache_age_seconds: "600" } },
      { id: "curl", name: "curl", attrs: { installed_version: "8.5.0", candidate_version: "8.6.0" } },
      { id: "nginx", name: "nginx", attrs: { installed_version: "1.26", candidate_version: "1.27" } },
    ]));
    expect(html.match(/class="primary-button package-action-button"/g)).toHaveLength(1);
    expect(html).toContain("Aplicar actualizaciones pendientes");
    expect(html).toContain("No hay botón por paquete a propósito");
  });

  it("keeps a backports candidate out of the pending list and its count", () => {
    const html = render(inventory([
      { id: "curl", name: "curl", attrs: { installed_version: "8.5.0", candidate_version: "8.6.0" } },
      { id: "nginx", name: "nginx", attrs: { candidate_automatic: "false", candidate_version: "1.27" } },
    ]));
    expect(html).toContain("Disponible en backports");
    expect(html).toContain('<span class="inventory-count">1</span>');
  });
});
