import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import type { BitacoraEvent } from "../api";
import { LocaleProvider } from "../i18n";
import EventsList from "./EventsList";

const event = (overrides: Partial<BitacoraEvent>): BitacoraEvent => ({
  id: "event-1",
  ts: "2026-11-27T09:00:00Z",
  host_id: "host-a",
  source: "journald",
  type: "service.failed",
  severity: "error",
  title: "nginx.service failed",
  ...overrides,
});

const render = (events: BitacoraEvent[], maxRows?: number) =>
  renderToStaticMarkup(<LocaleProvider><EventsList events={events} maxRows={maxRows} /></LocaleProvider>);

describe("EventsList", () => {
  it("makes each row a disclosure that states the event's origin", () => {
    const html = render([event({})]);
    expect(html).toContain('<li class="event-row"><details>');
    expect(html).toContain("Origen: journald");
    expect(html).toContain("nginx.service failed");
  });

  it("keeps the day heading above the run of rows", () => {
    const html = render([event({})]);
    expect(html).toContain("event-day-heading");
  });

  it("caps the rows on the overview without touching the empty state", () => {
    const events = [1, 2, 3].map((n) => event({ id: `event-${n}`, title: `titulo-${n}`, ts: `2026-11-27T09:0${n}:00Z` }));
    const html = render(events, 2);
    expect(html).toContain("titulo-3");
    expect(html).toContain("titulo-2");
    expect(html).not.toContain("titulo-1");
  });

  it("explains an empty stream instead of leaving a blank panel", () => {
    expect(render([])).toContain("Sin eventos registrados");
  });

  it("never highlights a row on the first paint", () => {
    // Every row would qualify, and a page that flashes its whole contents on
    // load has said nothing.
    expect(render([event({})])).not.toContain("event-row--new");
  });
});
