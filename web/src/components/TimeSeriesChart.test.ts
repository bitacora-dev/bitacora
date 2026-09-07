import { describe, expect, it } from "vitest";
import { chartSizeChanged } from "./TimeSeriesChart";

describe("chartSizeChanged", () => {
  it("does not resize a chart when its dimensions are unchanged", () => {
    expect(chartSizeChanged({ width: 640, height: 220 }, { width: 640, height: 220 })).toBe(false);
  });

  it("resizes a chart when either dimension changes", () => {
    expect(chartSizeChanged({ width: 640, height: 220 }, { width: 641, height: 220 })).toBe(true);
    expect(chartSizeChanged({ width: 640, height: 220 }, { width: 640, height: 221 })).toBe(true);
  });
});
