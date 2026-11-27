import { describe, expect, it } from "vitest";
import { sparklineGeometry, SPARKLINE_VIEWBOX_HEIGHT, SPARKLINE_VIEWBOX_WIDTH } from "./Sparkline";

const point = (ts: string, value: number) => ({ ts, value });

describe("sparklineGeometry", () => {
  it("draws nothing at all for an empty series", () => {
    expect(sparklineGeometry([])).toBeNull();
  });

  it("draws nothing when every sample is unusable", () => {
    expect(sparklineGeometry([point("not-a-date", 1), point("2026-11-27T10:00:00Z", Number.NaN)])).toBeNull();
  });

  it("spans the full viewBox and ends at the newest sample", () => {
    const geometry = sparklineGeometry([
      point("2026-11-27T10:00:00Z", 0),
      point("2026-11-27T10:00:10Z", 1),
    ], 1);
    expect(geometry?.line).toBe(`M0.00,${SPARKLINE_VIEWBOX_HEIGHT.toFixed(2)} L${SPARKLINE_VIEWBOX_WIDTH.toFixed(2)},0.00`);
  });

  it("sorts samples by instant instead of trusting array order", () => {
    const ordered = sparklineGeometry([point("2026-11-27T10:00:00Z", 0), point("2026-11-27T10:00:10Z", 1)], 1);
    const shuffled = sparklineGeometry([point("2026-11-27T10:00:10Z", 1), point("2026-11-27T10:00:00Z", 0)], 1);
    expect(shuffled).toEqual(ordered);
  });

  it("still draws a line for a perfectly flat series", () => {
    // A zero span would divide by zero and erase the trace exactly when the
    // host is calm, which reads as "no data" rather than "nothing moved".
    const geometry = sparklineGeometry([
      point("2026-11-27T10:00:00Z", 5),
      point("2026-11-27T10:00:10Z", 5),
    ]);
    expect(geometry?.line).toContain("L");
    expect(geometry?.line).not.toContain("NaN");
  });

  it("gives one sample a flat segment rather than an invisible dot", () => {
    const geometry = sparklineGeometry([point("2026-11-27T10:00:00Z", 0.5)], 1);
    expect(geometry?.line).toBe(`M0,${(SPARKLINE_VIEWBOX_HEIGHT / 2).toFixed(2)} L${SPARKLINE_VIEWBOX_WIDTH},${(SPARKLINE_VIEWBOX_HEIGHT / 2).toFixed(2)}`);
  });

  it("holds a bounded signal against its ceiling, not against its own peak", () => {
    // A host idling between 1 % and 2 % must not have that noise stretched
    // to the full height of the cell.
    const bounded = sparklineGeometry([point("2026-11-27T10:00:00Z", 0.01), point("2026-11-27T10:00:10Z", 0.02)], 1);
    expect(bounded?.line.endsWith("0.00")).toBe(false);
  });

  it("closes the area path back along the baseline", () => {
    const geometry = sparklineGeometry([point("2026-11-27T10:00:00Z", 1), point("2026-11-27T10:00:10Z", 2)]);
    expect(geometry?.area.endsWith(`L0,${SPARKLINE_VIEWBOX_HEIGHT} Z`)).toBe(true);
  });
});
