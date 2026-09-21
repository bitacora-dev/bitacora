import { describe, expect, it } from "vitest";
import type { BitacoraEvent, Job } from "./api";
import { LOG_PAGE_SIZE, LOG_REF_WINDOW_MS, MAX_REFERENCED_LINES, eventLogTarget, jobLogTarget, logEntryID, logRefPageOffset, logRefRange } from "./logrefs";

const event: BitacoraEvent = {
  id: "event-1",
  ts: "2026-09-19T11:31:18Z",
  host_id: "host-1",
  source: "journald",
  type: "kernel.segfault",
  severity: "error",
  title: "Segfault in api",
};

const job: Job = {
  id: "job-1",
  job_name: "rclone-backup",
  host_id: "host-1",
  started_at: "2026-09-19T03:00:00Z",
  finished_at: "2026-09-19T03:04:00Z",
  duration_seconds: 240,
  status: "success",
  exit_code: 0,
};

describe("logEntryID", () => {
  it("builds the durable entry id logstore assigns to a referenced line", () => {
    expect(logEntryID("block-a", 0)).toBe("block-a:0");
  });
});

describe("eventLogTarget", () => {
  it("resolves every ref inside the first referenced block", () => {
    expect(eventLogTarget({ ...event, log_refs: [{ block_id: "block-a", line: 7 }, { block_id: "block-a", line: 5 }] })).toEqual({
      blockID: "block-a",
      firstLine: 5,
      entryIDs: ["block-a:5", "block-a:7"],
      anchorTS: event.ts,
    });
  });

  it("keeps a single block when refs name more than one, because a page shows one", () => {
    const target = eventLogTarget({ ...event, log_refs: [{ block_id: "block-a", line: 1 }, { block_id: "block-b", line: 2 }] });
    expect(target?.blockID).toBe("block-a");
    expect(target?.entryIDs).toEqual(["block-a:1"]);
  });

  it("returns nothing for an event without usable refs", () => {
    expect(eventLogTarget(event)).toBeNull();
    expect(eventLogTarget({ ...event, log_refs: [] })).toBeNull();
    expect(eventLogTarget({ ...event, log_refs: [{ block_id: "", line: 1 }] })).toBeNull();
    expect(eventLogTarget({ ...event, log_refs: [{ block_id: "block-a", line: -1 }] })).toBeNull();
  });
});

describe("jobLogTarget", () => {
  it("expands an inclusive line range and anchors on when the run finished", () => {
    expect(jobLogTarget({ ...job, log_refs: [{ block_id: "block-a", from: 2, to: 4 }] })).toEqual({
      blockID: "block-a",
      firstLine: 2,
      entryIDs: ["block-a:2", "block-a:3", "block-a:4"],
      anchorTS: job.finished_at,
    });
  });

  it("anchors a running job on its start, since Go sends the zero instant for an unset finish", () => {
    const target = jobLogTarget({ ...job, status: "running", finished_at: "0001-01-01T00:00:00Z", log_refs: [{ block_id: "block-a", from: 0, to: 0 }] });
    expect(target?.anchorTS).toBe(job.started_at);
  });

  it("bounds a very long run so one click never marks an unbounded number of rows", () => {
    const target = jobLogTarget({ ...job, log_refs: [{ block_id: "block-a", from: 0, to: 10_000 }] });
    expect(target?.entryIDs).toHaveLength(MAX_REFERENCED_LINES);
  });

  it("returns nothing for an inverted or absent range", () => {
    expect(jobLogTarget(job)).toBeNull();
    expect(jobLogTarget({ ...job, log_refs: [{ block_id: "block-a", from: 9, to: 2 }] })).toBeNull();
  });
});

describe("logRefPageOffset", () => {
  it("lands on the page that actually contains the referenced line", () => {
    expect(logRefPageOffset(0)).toBe(0);
    expect(logRefPageOffset(LOG_PAGE_SIZE - 1)).toBe(0);
    expect(logRefPageOffset(LOG_PAGE_SIZE)).toBe(LOG_PAGE_SIZE);
    expect(logRefPageOffset(137, 50)).toBe(100);
  });

  it("falls back to the first page for an unusable line or page size", () => {
    expect(logRefPageOffset(-1)).toBe(0);
    expect(logRefPageOffset(10, 0)).toBe(0);
  });
});

describe("logRefRange", () => {
  it("brackets the anchor so a block flushed before the event stays in range", () => {
    const range = logRefRange("2026-09-19T11:31:18Z");
    expect(range).toEqual({ from: "2026-09-18T11:31", to: "2026-09-20T11:31" });
    expect(new Date(`${range!.to}Z`).getTime() - new Date(`${range!.from}Z`).getTime()).toBe(2 * LOG_REF_WINDOW_MS);
  });

  it("returns nothing for an unparseable anchor instead of querying a NaN range", () => {
    expect(logRefRange("not a timestamp")).toBeNull();
  });
});
