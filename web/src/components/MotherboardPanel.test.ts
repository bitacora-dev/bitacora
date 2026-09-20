import { describe, expect, it } from "vitest";
import { isStaleBIOS, parseBIOSDate } from "./MotherboardPanel";

describe("parseBIOSDate", () => {
  it("parses DMI's month/day/year BIOS date without timezone drift", () => {
    const date = parseBIOSDate("11/28/2025");
    expect([date?.getFullYear(), date?.getMonth(), date?.getDate()]).toEqual([2025, 10, 28]);
  });

  it("rejects malformed and impossible DMI dates", () => {
    expect(parseBIOSDate("2025-11-28")).toBeNull();
    expect(parseBIOSDate("02/30/2025")).toBeNull();
  });
});

describe("isStaleBIOS", () => {
  it("marks BIOS dates older than five years as operationally old", () => {
    expect(isStaleBIOS(new Date(2019, 0, 1), new Date(2026, 0, 1))).toBe(true);
    expect(isStaleBIOS(new Date(2022, 0, 1), new Date(2026, 0, 1))).toBe(false);
  });
});
