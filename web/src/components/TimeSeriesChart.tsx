import { useEffect, useMemo, useRef, useState } from "react";
import uPlot from "uplot";
import "uplot/dist/uPlot.min.css";
import type { SeriesPoint } from "../api";
import { useTranslation } from "../i18n";

export interface PointReadout {
  primary: string;
  secondary?: string;
}

interface Props {
  title: string;
  points: SeriesPoint[];
  color: string;
  formatAxisValue: (v: number) => string;
  describePoint: (point: SeriesPoint, index: number) => PointReadout;
}

export default function TimeSeriesChart({ title, points, color, formatAxisValue, describePoint }: Props) {
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

    const opts: uPlot.Options = {
      width: container.clientWidth,
      height: 220,
      cursor: { drag: { x: true, y: false }, points: { show: false } },
      legend: { show: false },
      scales: { x: { time: true } },
      axes: [
        {
          stroke: "#94a3b8",
          grid: { stroke: "#1f2937", width: 1 },
        },
        {
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

    const resize = new ResizeObserver(() => {
      chart.setSize({ width: container.clientWidth, height: 220 });
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
      resize.disconnect();
      chart.destroy();
      chartRef.current = null;
    };
  }, [color, data, formatAxisValue, points.length]);

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
