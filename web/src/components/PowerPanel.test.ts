import { describe, expect, it } from "vitest";
import type { Inventory } from "../api";
import { anyOnBattery, chargeRatio, readUPS, readUPSInventory, runtimeSeconds } from "./PowerPanel";

function inventory(items: Inventory["items"]): Inventory {
  return { host_id: "host-a", kind: "ups", reported_at: "2026-09-21T12:00:00Z", schema: 1, items };
}

describe("chargeRatio", () => {
  // NUT prints battery.charge verbatim and the agent forwards the string
  // untouched, so both spellings arrive from real hardware.
  it("accepts the integer and decimal spellings NUT emits", () => {
    expect(chargeRatio("100")).toBe(1);
    expect(chargeRatio("84.5")).toBeCloseTo(0.845);
  });

  it("rejects a reading outside 0-100 instead of drawing an impossible bar", () => {
    expect(chargeRatio("-1")).toBeNull();
    expect(chargeRatio("140")).toBeNull();
  });

  it("returns null for an absent or unparseable value", () => {
    expect(chargeRatio(undefined)).toBeNull();
    expect(chargeRatio("unknown")).toBeNull();
  });
});

describe("runtimeSeconds", () => {
  it("reads the seconds NUT reports", () => {
    expect(runtimeSeconds("1800")).toBe(1800);
  });

  it("returns null rather than zero for an absent estimate", () => {
    expect(runtimeSeconds(undefined)).toBeNull();
  });
});

describe("readUPS", () => {
  it("reads every attribute the collector sets", () => {
    expect(
      readUPS({ id: "eaton", name: "eaton", attrs: { status: "OB DISCHRG", on_battery: "true", battery_charge_pct: "72", runtime_seconds: "900", model: "Eaton 5E" } }),
    ).toEqual({ id: "eaton", name: "eaton", status: "OB DISCHRG", onBattery: true, chargeRatio: 0.72, runtimeSeconds: 900, model: "Eaton 5E" });
  });

  // The collector omits every key whose NUT variable is missing, and omits
  // `attrs` entirely when none of them are. "Unknown" must not become "fine".
  it("does not read a missing on_battery attribute as mains power", () => {
    expect(readUPS({ id: "ups", name: "ups", attrs: {} }).onBattery).toBeNull();
  });
});

describe("readUPSInventory", () => {
  // The ups collector builds its slice with `var items []InventoryItem`, so
  // an unreachable NUT arrives as `"items": null`, not as an empty array.
  it("survives the null item list an unreachable NUT produces", () => {
    expect(readUPSInventory({ ...inventory([]), items: null as unknown as Inventory["items"] })).toEqual([]);
  });

  it("returns nothing for a host that never reported this inventory", () => {
    expect(readUPSInventory(null)).toEqual([]);
  });
});

describe("anyOnBattery", () => {
  it("raises the alarm when any unit reports battery power", () => {
    expect(anyOnBattery(readUPSInventory(inventory([
      { id: "a", name: "a", attrs: { on_battery: "false" } },
      { id: "b", name: "b", attrs: { on_battery: "true" } },
    ])))).toBe(true);
  });

  // An unreported unit is not evidence of mains power, so it can neither
  // raise the alarm on its own nor be counted as "on battery".
  it("does not raise the alarm on an unreported unit", () => {
    expect(anyOnBattery(readUPSInventory(inventory([{ id: "a", name: "a", attrs: {} }])))).toBe(false);
  });

  it("stays quiet while every unit reports mains power", () => {
    expect(anyOnBattery(readUPSInventory(inventory([{ id: "a", name: "a", attrs: { on_battery: "false" } }])))).toBe(false);
  });
});

describe("empty attribute strings", () => {
  // Number("") is 0. A UPS that reported the key with no value must read as
  // unknown, never as a flat battery with no runtime left.
  it("reads an empty charge or runtime as unknown, not as zero", () => {
    expect(chargeRatio("")).toBeNull();
    expect(runtimeSeconds("")).toBeNull();
  });
});
