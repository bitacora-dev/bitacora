import { describe, expect, it } from "vitest";
import { formatDayLabel, formatRowTime, groupByDay } from "./dayGroups";

const names = { today: "Today", yesterday: "Yesterday", unknownDay: "Undated" };

// Local-time constructor on purpose: the grouping is about the calendar day
// the person reading the screen is in, not the one UTC happens to be in.
function localISO(year: number, month: number, day: number, hour: number, minute = 0): string {
  return new Date(year, month - 1, day, hour, minute).toISOString();
}

describe("groupByDay", () => {
  it("collects consecutive rows from the same local day under one key", () => {
    const rows = [
      { ts: localISO(2026, 9, 29, 14, 14) },
      { ts: localISO(2026, 9, 29, 10, 14) },
      { ts: localISO(2026, 9, 27, 23, 50) },
    ];

    expect(groupByDay(rows, (r) => r.ts).map((g) => [g.key, g.items.length])).toEqual([
      ["2026-09-29", 2],
      ["2026-09-27", 1],
    ]);
  });

  it("keeps a row whose instant cannot be parsed instead of dropping it", () => {
    const rows = [{ ts: "not a date" }, { ts: localISO(2026, 9, 29, 9) }];
    const groups = groupByDay(rows, (r) => r.ts);

    expect(groups.map((g) => g.key)).toEqual(["", "2026-09-29"]);
    expect(groups[0].items).toHaveLength(1);
  });

  it("returns nothing for no rows", () => {
    expect(groupByDay([], (r: { ts: string }) => r.ts)).toEqual([]);
  });
});

describe("formatDayLabel", () => {
  const now = new Date(2026, 8, 29, 16, 0);

  it("names today and yesterday rather than dating them", () => {
    expect(formatDayLabel("2026-09-29", "en-GB", names, now)).toBe("Today");
    expect(formatDayLabel("2026-09-28", "en-GB", names, now)).toBe("Yesterday");
  });

  it("omits the year inside the current year and states it outside", () => {
    // The exact wording is Intl's, not ours; what this pins is that a date
    // in the current year does not carry a redundant "2026" on every
    // heading, and a date outside it is not silently ambiguous.
    expect(formatDayLabel("2026-03-02", "en-GB", names, now)).not.toContain("2026");
    expect(formatDayLabel("2025-12-31", "en-GB", names, now)).toContain("2025");
  });

  it("says a row is undated rather than inventing a day for it", () => {
    expect(formatDayLabel("", "en-GB", names, now)).toBe("Undated");
  });
});

describe("formatRowTime", () => {
  it("keeps seconds, because two events in the same incident are a sequence", () => {
    expect(formatRowTime(localISO(2026, 9, 29, 10, 14), "en-GB")).toMatch(/^10:14:00$/);
  });
});
