import { describe, expect, it } from "vitest";
import type { PublicSurface, SeriesPoint } from "../api";
import { hasPublicSurfaceData, latestValue, windowIncrease } from "./PublicSurfacePanel";

function points(...values: number[]): SeriesPoint[] {
  return values.map((value, index) => ({ ts: new Date(Date.UTC(2026, 8, 21, 10, index * 5)).toISOString(), value }));
}

const empty: PublicSurface = {
  ssh_failed_logins_total: [],
  ssh_failed_logins_per_minute: [],
  fail2ban_jails_total: [],
  fail2ban_banned_total: [],
  firewall_rules_total: [],
  ovh_traffic_used_ratio: [],
};

describe("latestValue", () => {
  it("returns the most recent sample", () => {
    expect(latestValue(points(400, 460))).toBe(460);
  });

  it("returns null rather than zero for a series nobody reported", () => {
    expect(latestValue([])).toBeNull();
  });
});

describe("windowIncrease", () => {
  it("counts the new attempts that arrived inside the window", () => {
    expect(windowIncrease(points(400, 430, 460))).toBe(60);
  });

  it("skips the drop when the auth log rotates instead of reporting a negative", () => {
    expect(windowIncrease(points(900, 4, 34))).toBe(30);
  });

  // One sample proves a total. It proves nothing about change, and a 0 there
  // would read as "no new attempts", which is the false reassurance this
  // panel exists to avoid.
  it("returns null when a single sample cannot describe change", () => {
    expect(windowIncrease(points(400))).toBeNull();
    expect(windowIncrease([])).toBeNull();
  });

  it("reports a genuinely quiet window as zero, not as missing", () => {
    expect(windowIncrease(points(400, 400, 400))).toBe(0);
  });
});

describe("hasPublicSurfaceData", () => {
  it("treats a host that reported nothing as pending, not as zeroed", () => {
    expect(hasPublicSurfaceData(empty)).toBe(false);
  });

  it("counts a host with any one reported signal as reporting", () => {
    expect(hasPublicSurfaceData({ ...empty, firewall_rules_total: points(27) })).toBe(true);
  });

  // The per-minute series is derived by the hub and needs two samples, so a
  // host one cycle into its first report would otherwise look like it had
  // reported nothing at all.
  it("does not depend on the derived per-minute series", () => {
    expect(hasPublicSurfaceData({ ...empty, ssh_failed_logins_total: points(400) })).toBe(true);
  });
});
