import { useEffect, useMemo, useRef, useState } from "react";
import uPlot from "uplot";
import "uplot/dist/uPlot.min.css";
import type { SeriesPoint } from "../api";
import { useTranslation } from "../i18n";

export interface PointReadout { primary: string; secondary?: string; }
export interface ChartSeries {
  name: string;
  points: SeriesPoint[];
  color: string;
  describePoint: (point: SeriesPoint, index: number) => PointReadout;
}
export interface ChartSize { width: number; height: number; }

// uPlot draws its axis labels in a 12px system font. Keep the clearance
// separate from measurement so ticks do not collide with the plot edge.
export const Y_AXIS_LABEL_MARGIN_PX = 16;

// uPlot calls the size hook with null before the first tick exists, despite
// declaring the value as string[] in its TypeScript definitions.
export function yAxisSize(labels: string[] | null, measureText: (label: string) => number): number {
  const widestLabel = (labels ?? []).reduce((widest, label) => Math.max(widest, measureText(label)), 0);
  return Math.ceil(widestLabel + Y_AXIS_LABEL_MARGIN_PX);
}

export function chartSizeChanged(current: ChartSize, next: ChartSize): boolean {
  return current.width !== next.width || current.height !== next.height;
}

// mirroredRange is the symmetric y range a mirror chart plots on: both
// halves share one bound, so a 40 MB/s download and a 40 MB/s upload draw
// the same height in opposite directions. Scaling each half to its own peak
// would make a trickle of uploads look like a flood whenever the download
// was idle, which is the exact comparison this shape exists to make.
//
// Returns null when nothing has been sampled yet: uPlot then keeps its own
// default range instead of being pinned to a bogus ±0.
export function mirroredRange(series: { points: SeriesPoint[] }[]): [number, number] | null {
  let bound = 0;
  for (const item of series) {
    for (const point of item.points) {
      if (!Number.isFinite(point.value)) continue;
      bound = Math.max(bound, Math.abs(point.value));
    }
  }
  return bound === 0 ? null : [-bound, bound];
}

interface Props {
  title: string;
  // Legacy single-series props remain supported for existing callers.
  points?: SeriesPoint[];
  color?: string;
  describePoint?: (point: SeriesPoint, index: number) => PointReadout;
  // New charts can provide any number of independently named series.
  series?: ChartSeries[];
  yRange?: [number, number];
  // Plots the first series upwards and every later one downwards against one
  // shared scale. The sign is a drawing decision only: `describePoint` still
  // receives the measured value, so the readout never shows a negative byte
  // rate and the axis labels are drawn through Math.abs.
  mirror?: boolean;
  formatAxisValue: (v: number) => string;
}

export default function TimeSeriesChart({ title, points, color, describePoint, series, yRange, mirror = false, formatAxisValue }: Props) {
  const { t, intlTag } = useTranslation();
  const containerRef = useRef<HTMLDivElement>(null);
  const chartRef = useRef<uPlot | null>(null);
  const [cursorIndex, setCursorIndex] = useState<number | null>(null);

  const chartSeries = useMemo<ChartSeries[]>(() => {
    if (series && series.length > 0) return series;
    if (points && color && describePoint) return [{ name: "", points, color, describePoint }];
    return [];
  }, [color, describePoint, points, series]);

  const { data, timestampKeys } = useMemo(() => {
    const keys = new Set<string>();
    for (const item of chartSeries) for (const point of item.points) keys.add(point.ts);
    const sortedKeys = [...keys].sort((left, right) => new Date(left).getTime() - new Date(right).getTime());
    const aligned: uPlot.AlignedData = [
      sortedKeys.map((key) => Math.floor(new Date(key).getTime() / 1000)),
      ...chartSeries.map((item, index) => {
        const sign = mirror && index > 0 ? -1 : 1;
        const values = new Map(item.points.map((point) => [point.ts, point.value * sign]));
        return sortedKeys.map((key) => values.get(key) ?? null);
      }),
    ];
    return { data: aligned, timestampKeys: sortedKeys };
  }, [chartSeries, mirror]);

  const activeIndex = cursorIndex !== null && timestampKeys[cursorIndex] ? cursorIndex : timestampKeys.length - 1;
  const activeTimestamp = activeIndex >= 0 ? timestampKeys[activeIndex] : null;
  const readouts = activeTimestamp === null ? [] : chartSeries.flatMap((item) => {
    const pointIndex = item.points.findIndex((point) => point.ts === activeTimestamp);
    return pointIndex < 0 ? [] : [{ name: item.name, readout: item.describePoint(item.points[pointIndex], pointIndex) }];
  });
  const label = cursorIndex !== null && timestampKeys[cursorIndex] ? t.inspectedValueLabel : t.currentValueLabel;
  const timeLabel = activeTimestamp ? new Date(activeTimestamp).toLocaleTimeString(intlTag) : "";

  // Recomputed from the data rather than from a prop so the two halves stay
  // comparable as the window slides; `yRange` still wins when a caller pins
  // a fixed scale (a ratio chart).
  const effectiveRange = useMemo(
    () => (yRange ? yRange : mirror ? mirroredRange(chartSeries) : null),
    [chartSeries, mirror, yRange],
  );

  useEffect(() => {
    const container = containerRef.current;
    if (!container) return;
    let size: ChartSize = { width: container.clientWidth, height: container.clientHeight };
    const measurementCanvas = document.createElement("canvas");
    const context = measurementCanvas.getContext("2d");
    const measureAxisLabel = (label: string) => {
      if (!context) return label.length * 7;
      context.font = "12px system-ui";
      return context.measureText(label).width;
    };
    const opts: uPlot.Options = {
      width: size.width,
      height: size.height,
      cursor: { drag: { x: true, y: false }, points: { show: false } },
      legend: { show: false },
      scales: { x: { time: true }, ...(effectiveRange ? { y: { range: effectiveRange } } : {}) },
      axes: [
        { stroke: "#94a3b8", grid: { stroke: "#1f2937", width: 1 } },
        { size: (_u, labels) => yAxisSize(labels, measureAxisLabel), stroke: "#94a3b8", grid: { stroke: "#1f2937", width: 1 }, values: (_u, vals) => vals.map((v) => formatAxisValue(mirror ? Math.abs(v) : v)) },
      ],
      series: [{}, ...chartSeries.map((item) => ({
        stroke: item.color,
        fill: item.color + "18",
        width: 2,
        points: { show: item.points.length > 0 && item.points.length < 60 },
      }))],
      hooks: { setCursor: [(u) => setCursorIndex(typeof u.cursor.idx === "number" ? u.cursor.idx : null)] },
    };
    const chart = new uPlot(opts, data, container);
    chartRef.current = chart;
    let animationFrame: number | null = null;
    let disposed = false;
    const resize = new ResizeObserver(() => {
      if (animationFrame !== null) return;
      animationFrame = requestAnimationFrame(() => {
        animationFrame = null;
        if (disposed) return;
        const nextSize = { width: container.clientWidth, height: container.clientHeight };
        if (!chartSizeChanged(size, nextSize)) return;
        size = nextSize;
        chart.setSize(nextSize);
      });
    });
    resize.observe(container);
    const clearCursor = () => setCursorIndex(null);
    container.addEventListener("mouseleave", clearCursor);
    container.addEventListener("touchend", clearCursor);
    container.addEventListener("touchcancel", clearCursor);
    return () => {
      container.removeEventListener("mouseleave", clearCursor);
      container.removeEventListener("touchend", clearCursor);
      container.removeEventListener("touchcancel", clearCursor);
      disposed = true;
      if (animationFrame !== null) cancelAnimationFrame(animationFrame);
      resize.disconnect();
      chart.destroy();
      chartRef.current = null;
    };
  }, [chartSeries, data, effectiveRange, formatAxisValue, mirror]);

  return <div className={mirror ? "control-panel chart-panel chart-panel--mirror" : "control-panel chart-panel"}>
    <div className="chart-head">
      <div><h2>{title}</h2><p>{label}</p></div>
      <div className="chart-value">
        {readouts.length === 0 ? <strong>{t.noSamples}</strong> : readouts.map(({ name, readout }) => <div className="chart-series-value" key={name || "primary"}>
          {name && <span>{name}</span>}<strong>{readout.primary}</strong>{readout.secondary && <span>{readout.secondary}</span>}
        </div>)}
        {timeLabel && <time dateTime={activeTimestamp ?? undefined}>{timeLabel}</time>}
      </div>
    </div>
    <div ref={containerRef} className="chart-canvas" />
  </div>;
}
