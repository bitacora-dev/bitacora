import { useEffect, useMemo, useRef, useState } from "react";
import uPlot from "uplot";
import "uplot/dist/uPlot.min.css";
import type { SeriesPoint } from "../api";
import { useTranslation } from "../i18n";

export interface PointReadout {
  primary: string;
  secondary?: string;
}

export interface ChartSize {
  width: number;
  height: number;
}

// uPlot draws its axis labels in a 12px system font. Keep the clearance
// separate from measurement so ticks do not collide with the plot edge.
export const Y_AXIS_LABEL_MARGIN_PX = 16;

// uPlot calls an axis size hook once during init, before any tick exists, and
// passes a literal null there (uPlot.esm.js: `axis.size(self, null, i, 0)`).
// Its own typings declare `values: string[]`, so TypeScript will not catch it.
// Returning just the margin is the right initial guess: uPlot calls the hook
// again with the real ticks as soon as it has them.
export function yAxisSize(
  labels: string[] | null,
  measureText: (label: string) => number,
): number {
  const widestLabel = (labels ?? []).reduce(
    (widest, label) => Math.max(widest, measureText(label)),
    0,
  );
  return Math.ceil(widestLabel + Y_AXIS_LABEL_MARGIN_PX);
}

export function chartSizeChanged(current: ChartSize, next: ChartSize): boolean {
  return current.width !== next.width || current.height !== next.height;
}

interface Props {
  title: string;
  points: SeriesPoint[];
  color: string;
  yRange?: [number, number];
  formatAxisValue: (v: number) => string;
  describePoint: (point: SeriesPoint, index: number) => PointReadout;
}

export default function TimeSeriesChart({ title, points, color, yRange, formatAxisValue, describePoint }: Props) {
  const { t, intlTag } = useTranslation();
  const containerRef = useRef<HTMLDivElement>(null);
  const chartRef = useRef<uPlot | null>(null);
  const [cursorIndex, setCursorIndex] = useState<number | null>(null);

  const activeIndex = cursorIndex !== null && points[cursorIndex] ? cursorIndex : points.length - 1;
  const activePoint = activeIndex >= 0 ? points[activeIndex] : null;
  const readout = activePoint ? describePoint(activePoint, activeIndex) : { primary: t.noSamples };
  const label = cursorIndex !== null && points[cursorIndex] ? t.inspectedValueLabel : t.currentValueLabel;
  const timeLabel = activePoint ? new Date(activePoint.ts).toLocaleTimeString(intlTag) : "";

  const data = useMemo<uPlot.AlignedData>(
    () => [
      points.map((p) => Math.floor(new Date(p.ts).getTime() / 1000)),
      points.map((p) => p.value),
    ],
    [points],
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
      scales: { x: { time: true }, ...(yRange ? { y: { range: yRange } } : {}) },
      axes: [
        {
          stroke: "#94a3b8",
          grid: { stroke: "#1f2937", width: 1 },
        },
        {
          size: (_u, labels) => yAxisSize(labels, measureAxisLabel),
          stroke: "#94a3b8",
          grid: { stroke: "#1f2937", width: 1 },
          values: (_u, vals) => vals.map((v) => formatAxisValue(v)),
        },
      ],
      series: [
        {},
        {
          stroke: color,
          fill: color + "18",
          width: 2,
          points: { show: points.length > 0 && points.length < 60 },
        },
      ],
      hooks: {
        setCursor: [
          (u) => {
            setCursorIndex(typeof u.cursor.idx === "number" ? u.cursor.idx : null);
          },
        ],
      },
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
  }, [color, data, formatAxisValue, points.length, yRange]);

  return (
    <div className="control-panel chart-panel">
      <div className="chart-head">
        <div>
          <h2>{title}</h2>
          <p>{label}</p>
        </div>
        <div className="chart-value">
          <strong>{readout.primary}</strong>
          {readout.secondary && <span>{readout.secondary}</span>}
          {timeLabel && <time dateTime={activePoint?.ts}>{timeLabel}</time>}
        </div>
      </div>
      <div ref={containerRef} className="chart-canvas" />
    </div>
  );
}
