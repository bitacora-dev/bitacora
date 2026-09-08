import { describe, expect, it } from "vitest";
import { chartSizeChanged, Y_AXIS_LABEL_MARGIN_PX, yAxisSize } from "./TimeSeriesChart";

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
});
