import { describe, expect, it } from "vitest";
import { packageActionVisibility, phaseAfterSuccessfulCacheRefresh } from "./PackageUpdatePanel";

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
