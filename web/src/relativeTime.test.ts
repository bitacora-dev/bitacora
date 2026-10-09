import { describe, expect, it } from "vitest";
import { ageSeconds, formatAge, formatRuntime, instantMillis } from "./relativeTime";

const now = Date.UTC(2026, 8, 21, 12, 0, 0);

describe("instantMillis", () => {
  it("reads a real instant", () => {
    expect(instantMillis("2026-09-21T11:00:00Z")).toBe(Date.UTC(2026, 8, 21, 11, 0, 0));
  });

  // Go serializes an unset time.Time as year 1 instead of omitting it, so the
  // zero instant has to be read as "never", not as a very old event.
  it("treats the Go zero instant as absent", () => {
    expect(instantMillis("0001-01-01T00:00:00Z")).toBeNull();
  });

  it("treats an empty or missing value as absent", () => {
    expect(instantMillis("")).toBeNull();
    expect(instantMillis(undefined)).toBeNull();
  });

  it("treats an unparseable value as absent rather than as NaN", () => {
    expect(instantMillis("not-a-date")).toBeNull();
  });
});

describe("ageSeconds", () => {
  it("measures elapsed seconds", () => {
    expect(ageSeconds("2026-09-21T11:30:00Z", now)).toBe(1800);
  });

  // Agent and hub clocks drift. A handshake stamped a few seconds into the
  // future is still "just now", never a negative age.
  it("clamps a future instant to zero instead of reporting negative age", () => {
    expect(ageSeconds("2026-09-21T12:00:30Z", now)).toBe(0);
  });

  it("returns null for an absent instant", () => {
    expect(ageSeconds(undefined, now)).toBeNull();
  });
});

describe("formatAge", () => {
  it("scales the unit to the elapsed time", () => {
    expect(formatAge("2026-09-21T11:59:30Z", "en", now)).toBe("30 seconds ago");
    expect(formatAge("2026-09-21T11:30:00Z", "en", now)).toBe("30 minutes ago");
    expect(formatAge("2026-09-21T03:00:00Z", "en", now)).toBe("9 hours ago");
    expect(formatAge("2026-09-18T12:00:00Z", "en", now)).toBe("3 days ago");
  });

  it("follows the active locale", () => {
    expect(formatAge("2026-09-21T03:00:00Z", "es", now)).toBe("hace 9 horas");
  });

  it("returns null rather than a placeholder when the instant is absent", () => {
    expect(formatAge("0001-01-01T00:00:00Z", "en", now)).toBeNull();
  });
});

describe("formatRuntime", () => {
  it("reports whole minutes for a short battery estimate", () => {
    expect(formatRuntime(1_320, "en")).toBe("22 min");
  });

  it("splits hours and minutes once the estimate is long", () => {
    expect(formatRuntime(5_400, "en")).toBe("1 hr 30 min");
  });

  it("drops an empty minute part", () => {
    expect(formatRuntime(7_200, "en")).toBe("2 hr");
  });

  // A UPS that reports a handful of seconds left is still reporting; showing
  // "0 min" is the honest rounding, and the caller pairs it with the charge.
  it("rounds a sub-minute estimate down without inventing precision", () => {
    expect(formatRuntime(30, "en")).toBe("0 min");
  });
});
