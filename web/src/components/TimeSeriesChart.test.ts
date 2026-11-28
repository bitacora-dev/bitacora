import { describe, expect, it } from "vitest";
import { chartSizeChanged, mirroredRange, Y_AXIS_LABEL_MARGIN_PX, yAxisSize } from "./TimeSeriesChart";

describe("chartSizeChanged", () => {
  it("does not resize a chart when its dimensions are unchanged", () => {
    expect(chartSizeChanged({ width: 640, height: 220 }, { width: 640, height: 220 })).toBe(false);
  });

  it("resizes a chart when either dimension changes", () => {
    expect(chartSizeChanged({ width: 640, height: 220 }, { width: 641, height: 220 })).toBe(true);
    expect(chartSizeChanged({ width: 640, height: 220 }, { width: 640, height: 221 })).toBe(true);
  });
});

describe("yAxisSize", () => {
  it("reserves the measured width of every formatted uPlot tick, including range bounds outside the samples", () => {
    const measured = new Map([["82.0%", 33], ["100.0%", 48], ["0.0%", 28]]);
    const uPlotTicks = ["82.0%", "100.0%", "0.0%"];
    const size = yAxisSize(uPlotTicks, (label) => measured.get(label) ?? 0);

    expect(size).toBeGreaterThanOrEqual(48 + Y_AXIS_LABEL_MARGIN_PX);
  });

  // uPlot calls the size hook with a literal null before the first tick exists.
  // Dereferencing it threw during render and the ErrorBoundary took down the
  // whole dashboard, not just the chart.
  it("survives the null uPlot passes on the first sizing pass", () => {
    expect(() => yAxisSize(null, () => 40)).not.toThrow();
    expect(yAxisSize(null, () => 40)).toBe(Y_AXIS_LABEL_MARGIN_PX);
  });

  it("reserves only the margin when there are no ticks yet", () => {
    expect(yAxisSize([], () => 40)).toBe(Y_AXIS_LABEL_MARGIN_PX);
  });
});

describe("mirroredRange", () => {
  const point = (ts: string, value: number) => ({ ts, value });

  it("gives both halves one shared bound taken from the larger series", () => {
    expect(mirroredRange([
      { points: [point("2026-11-27T10:00:00Z", 10)] },
      { points: [point("2026-11-27T10:00:00Z", 40)] },
    ])).toEqual([-40, 40]);
  });

  it("is symmetric, so an equal upload and download draw the same height", () => {
    const [low, high] = mirroredRange([
      { points: [point("2026-11-27T10:00:00Z", 7)] },
      { points: [point("2026-11-27T10:00:00Z", 7)] },
    ]) ?? [0, 0];
    expect(-low).toBe(high);
  });

  it("leaves uPlot its own range when nothing has been sampled", () => {
    expect(mirroredRange([{ points: [] }, { points: [] }])).toBeNull();
  });

  it("does not pin the scale to zero on an idle host", () => {
    expect(mirroredRange([{ points: [point("2026-11-27T10:00:00Z", 0)] }])).toBeNull();
  });

  it("ignores a non-finite sample instead of producing a NaN bound", () => {
    expect(mirroredRange([{ points: [point("2026-11-27T10:00:00Z", Number.NaN), point("2026-11-27T10:00:10Z", 3)] }])).toEqual([-3, 3]);
  });
});
