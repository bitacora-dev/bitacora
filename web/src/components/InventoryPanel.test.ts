import { describe, expect, it } from "vitest";
import { diskUsage } from "./InventoryPanel";

describe("diskUsage", () => {
  it("keeps usable disk values and clamps an over-reported usage ratio", () => {
    expect(diskUsage({ capacity_bytes: "100", used_bytes: "120", available_bytes: "5" })).toEqual({
      capacity: 100,
      used: 120,
      available: 5,
      ratio: 1,
    });
  });

  it("does not invent a zero usage when statfs attributes are absent or invalid", () => {
    expect(diskUsage({})).toBeNull();
    expect(diskUsage({ capacity_bytes: "100", used_bytes: "unknown" })).toBeNull();
    expect(diskUsage({ capacity_bytes: "0", used_bytes: "0" })).toBeNull();
  });
});
