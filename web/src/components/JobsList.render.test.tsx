import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import type { Job } from "../api";
import { LocaleProvider } from "../i18n";
import JobsList from "./JobsList";

const job = (overrides: Partial<Job>): Job => ({
  id: "job-1",
  job_name: "backup",
  host_id: "host-a",
  started_at: "2026-11-27T09:00:00Z",
  finished_at: "2026-11-27T09:05:00Z",
  duration_seconds: 300,
  status: "success",
  exit_code: 0,
  ...overrides,
});

const render = (jobs: Job[], maxRows?: number) =>
  renderToStaticMarkup(<LocaleProvider><JobsList jobs={jobs} maxRows={maxRows} /></LocaleProvider>);

describe("JobsList", () => {
  it("pins the run in progress above the finished history", () => {
    const html = render([
      job({ id: "done", job_name: "nightly" }),
      job({ id: "live", job_name: "rsync-now", status: "running", finished_at: "0001-01-01T00:00:00Z" }),
    ]);
    expect(html.indexOf("rsync-now")).toBeLessThan(html.indexOf("nightly"));
    expect(html).toContain("job-running");
  });

  it("refuses to draw a progress bar the tool never reported", () => {
    const html = render([job({ id: "live", status: "running", stats: { files_transferred: 12 }, finished_at: "0001-01-01T00:00:00Z" })]);
    expect(html).not.toContain("job-progress");
    expect(html).toContain("no se dibuja ninguna barra de progreso");
  });

  it("draws the bar when the extractor published a total" , () => {
    const html = render([job({ id: "live", status: "running", stats: { bytes_transferred: 250, bytes_total: 1000 }, finished_at: "0001-01-01T00:00:00Z" })]);
    expect(html).toContain("job-progress");
    // Spanish percent formatting puts a non-breaking space before the sign.
    expect(html).toContain("completado");
    expect(html).toContain('style="width:25%"');
  });

  it("makes every finished run a disclosure row with its columns", () => {
    const html = render([job({ trigger: "systemd-timer", stats: { files_transferred: 4 } })]);
    expect(html).toContain('<li class="event-row job-row"><details>');
    expect(html).toContain("Temporizador de systemd");
    expect(html).toContain('class="job-row-duration">300 s<');
    expect(html).toContain("Archivos transferidos");
  });

  it("says the trigger is unreported rather than leaving the column blank", () => {
    expect(render([job({})])).toContain("Disparador sin reportar");
  });

  it("caps the finished rows without ever capping a running one", () => {
    const jobs = [
      job({ id: "live", status: "running", finished_at: "0001-01-01T00:00:00Z" }),
      ...[1, 2, 3].map((n) => job({ id: `done-${n}`, job_name: `run-${n}` })),
    ];
    const html = render(jobs, 2);
    expect(html).toContain("run-1");
    expect(html).toContain("run-2");
    expect(html).not.toContain("run-3");
    expect(html).toContain("job-running");
  });

  it("keeps the trigger and the peer on the run in progress", () => {
    // The pinned card is the row an operator came for. Dropping the peer would
    // hide which host a replication in progress is writing to.
    const html = render([job({
      id: "live",
      job_name: "borg-remote",
      status: "running",
      finished_at: "0001-01-01T00:00:00Z",
      trigger: "systemd-timer",
      peer_host_id: "host-b",
    })]);
    expect(html).toContain("Temporizador de systemd");
    expect(html).toContain("host-b");
  });

  it("repeats the trigger inside the disclosure, where a phone can still reach it", () => {
    // The summary's own trigger span is display:none below 760 px.
    const html = render([job({ trigger: "systemd-timer" })]);
    expect(html).toContain('class="job-row-trigger-detail">Temporizador de systemd<');
  });
});
