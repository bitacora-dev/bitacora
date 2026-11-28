import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import type { Host, Inventory } from "../api";
import { LocaleProvider } from "../i18n";
import HostBar from "./HostBar";

const host: Host = {
  id: "01J0000000000000000000HOST",
  name: "garza",
  hostname: "garza.lan",
  last_seen_at: "2026-11-27T10:00:00Z",
  agent_version: "0.4.1",
};

const updates = (items: Inventory["items"]): Inventory => ({
  host_id: host.id,
  kind: "pkgupdates",
  reported_at: "2026-11-27T10:00:00Z",
  schema: 1,
  items,
});

function render(overrides: Partial<Parameters<typeof HostBar>[0]> = {}) {
  return renderToStaticMarkup(
    <LocaleProvider>
      <HostBar
        hostID={host.id}
        host={host}
        hosts={[host]}
        sampleAt="2026-11-27T10:00:00Z"
        windowMinutes={15}
        lastSeenAt={host.last_seen_at ?? null}
        hostStale={false}
        ups={null}
        updates={null}
        hostIDCopyStatus="idle"
        onCopyHostID={() => {}}
        onAddServer={() => {}}
        onAddDevice={() => {}}
        onShowEvents={() => {}}
        onShowLogs={() => {}}
        onSelectHost={() => {}}
        {...overrides}
      />
    </LocaleProvider>,
  );
}

describe("HostBar", () => {
  it("states the averaging window the hub reported", () => {
    expect(render({ windowMinutes: 15 })).toContain("Ventana de 15 min");
  });

  // The regression this guards: windowMinutes used to be coerced to 0 before
  // the first summary arrived, so the one bar that is always on screen opened
  // by stating "Ventana de 0 min" — a window no collector ever reported.
  // Absent is absent, never zero.
  it("omits the window entirely before the first summary instead of saying zero", () => {
    const markup = render({ windowMinutes: null, sampleAt: null });
    expect(markup).not.toContain("Ventana de 0 min");
    expect(markup).not.toContain("host-bar-window");
  });

  // The beat asserts "a sample just landed". With no sample there is nothing
  // to assert, so the dot keeps its place without animating.
  it("does not beat when no sample has ever arrived", () => {
    expect(render({ sampleAt: null })).toContain("host-bar-pulse--idle");
    expect(render({ sampleAt: "2026-11-27T10:00:00Z" })).not.toContain("host-bar-pulse--idle");
  });

  it("says in words that there are no samples yet", () => {
    expect(render({ sampleAt: null })).toContain("Sin muestras todavía");
  });

  // The freshness line names what is being aged: the sample, not the page.
  it("marks an old real sample stale even if the host bar is freshly rendered", () => {
    const markup = render({ sampleAt: new Date(Date.now() - 5 * 60_000).toISOString() });
    expect(markup).toContain("host-bar-freshness--stale");
    expect(markup).toContain("sin señal reciente");
  });

  // An absent inventory is "the collector has not reported", which is not
  // "nothing is pending": no count may appear at all.
  it("shows no update count until an inventory exists", () => {
    expect(render({ updates: null })).not.toContain("host-bar-updates");
    expect(render({ updates: updates([]) })).toContain("Sin actualizaciones pendientes");
  });

  it("keeps the brand out of the component and in the dictionary", () => {
    expect(render()).toContain("Bitácora");
  });
});
