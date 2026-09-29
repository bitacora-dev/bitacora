import { describe, expect, it } from "vitest";
import type { JobOutputLine } from "../api";
import { INVENTORY_STALE_AFTER_SECONDS, inventoryIsStale, linesForJob, packageActionVisibility, phaseAfterSuccessfulCacheRefresh } from "./PackageUpdatePanel";

describe("phaseAfterSuccessfulCacheRefresh", () => {
  it("records an explicit recoverable state when a configured APT source remains stale", () => {
    const staleInventory = {
      host_id: "host-a",
      kind: "package_update",
      reported_at: "2026-09-19T00:00:00Z",
      schema: 1,
      items: [{ id: "apt:bash", name: "bash", attrs: { cache_age_seconds: "7200" } }],
    };
    expect(phaseAfterSuccessfulCacheRefresh(staleInventory, 3600)).toBe("refresh_still_stale");
  });

  it("keeps the stale-cache reason actionable after an otherwise successful refresh", () => {
    const phase = phaseAfterSuccessfulCacheRefresh({
      host_id: "host-a",
      kind: "package_update",
      reported_at: "2026-09-19T00:00:00Z",
      schema: 1,
      items: [{ id: "apt:bash", name: "bash", attrs: { cache_age_seconds: "7200" } }],
    }, 3600);
    expect(packageActionVisibility(true, true, true, phase)).toEqual({
      showApply: false,
      showRefresh: true,
    });
  });

  it("keeps the review-plan state after a fresh cache is returned", () => {
    expect(phaseAfterSuccessfulCacheRefresh(null, 3600)).toBe("refreshed");
  });
});

describe("linesForJob", () => {
  const line = (jobID: string, sequence: number): JobOutputLine => ({ job_id: jobID, sequence, ts: "2026-09-19T10:00:00Z", stream: "stdout", message: `line ${sequence}` });

  it("drops output belonging to a previous run that landed after a new one started", () => {
    expect(linesForJob("job-2", [line("job-1", 1), line("job-2", 1), line("job-2", 2)]).map((entry) => entry.sequence)).toEqual([1, 2]);
    expect(linesForJob("job-2", [line("job-1", 1)])).toEqual([]);
  });

  it("keeps output from a hub that does not send the job id yet", () => {
    expect(linesForJob("job-2", [line("", 1)])).toHaveLength(1);
  });
});

describe("inventoryIsStale", () => {
  const now = Date.parse("2026-09-29T14:00:00Z");
  const reportedSecondsAgo = (seconds: number) => new Date(now - seconds * 1000).toISOString();

  it("leaves an inventory collected within the collection cadence unmarked", () => {
    // The reported case: four hours old. It is not late — pkgupdates runs
    // every six hours — so the panel states the age without raising alarm.
    expect(inventoryIsStale(reportedSecondsAgo(4 * 60 * 60), now)).toBe(false);
  });

  it("does not cry wolf at exactly one collection interval", () => {
    expect(inventoryIsStale(reportedSecondsAgo(6 * 60 * 60), now)).toBe(false);
    expect(inventoryIsStale(reportedSecondsAgo(INVENTORY_STALE_AFTER_SECONDS), now)).toBe(false);
  });

  it("marks an inventory that has missed a whole cycle", () => {
    expect(inventoryIsStale(reportedSecondsAgo(INVENTORY_STALE_AFTER_SECONDS + 1), now)).toBe(true);
    expect(inventoryIsStale(reportedSecondsAgo(4 * 24 * 60 * 60), now)).toBe(true);
  });

  it("treats an absent or unset instant as unknown rather than stale", () => {
    // Go serializes an unset time.Time as year 1; reading that as "very old"
    // would paint a panel gold over a field the producer never filled in.
    expect(inventoryIsStale(undefined, now)).toBe(false);
    expect(inventoryIsStale("", now)).toBe(false);
    expect(inventoryIsStale("0001-01-01T00:00:00Z", now)).toBe(false);
  });
});
