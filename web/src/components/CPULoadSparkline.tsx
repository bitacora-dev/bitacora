import { useEffect, useMemo, useRef } from "react";
import uPlot from "uplot";
import "uplot/dist/uPlot.min.css";
import type { SeriesPoint } from "../api";
import { chartSizeChanged, type ChartSize } from "./TimeSeriesChart";

// Total CPU load is cyan everywhere on this dashboard. web/DESIGN.md records
// the hard-coded series colors as a transitional exception until the chart API
// takes tokens directly; this chart follows that exception rather than
// inventing a second CPU color.
const CPU_SERIES_COLOR = "#38bdf8";
const Y_RANGE: [number, number] = [0, 1];

export function sparklineData(points: SeriesPoint[]): uPlot.AlignedData {
  const usable = points
    .filter((point) => Number.isFinite(point.value) && Number.isFinite(new Date(point.ts).getTime()))
    .sort((left, right) => new Date(left.ts).getTime() - new Date(right.ts).getTime());
  return [
    usable.map((point) => Math.floor(new Date(point.ts).getTime() / 1000)),
    usable.map((point) => point.value),
  ];
}

interface Props {
  points: SeriesPoint[];
  label: string;
  current: string;
}

// A compact history strip under the core rows: it answers "was this spike
// already happening a minute ago?" without the axes, cursor, and readout of
// TimeSeriesChart, which owns the full inspectable CPU history above it. The
// readable current value DESIGN.md requires lives in the header of this
// component, not inside the canvas.
export default function CPULoadSparkline({ points, label, current }: Props) {
  const containerRef = useRef<HTMLDivElement>(null);
  const chartRef = useRef<uPlot | null>(null);
  const data = useMemo(() => sparklineData(points), [points]);
  const dataRef = useRef(data);
  dataRef.current = data;

  // The chart is built once per mount and fed new samples with setData on
  // every poll. Rebuilding the uPlot instance for each new data array tore the
  // canvas down and redrew it every ten seconds, which reads as a flicker.
  useEffect(() => {
    const container = containerRef.current;
    if (!container) return;
    let size: ChartSize = { width: container.clientWidth, height: container.clientHeight };
    const chart = new uPlot({
      width: size.width,
      height: size.height,
      cursor: { show: false },
      legend: { show: false },
      scales: { x: { time: true }, y: { range: Y_RANGE } },
      axes: [{ show: false }, { show: false }],
      padding: [2, 0, 0, 0],
      series: [{}, { stroke: CPU_SERIES_COLOR, fill: `${CPU_SERIES_COLOR}24`, width: 2, points: { show: false } }],
    }, dataRef.current, container);
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

    return () => {
      disposed = true;
      if (animationFrame !== null) cancelAnimationFrame(animationFrame);
      resize.disconnect();
      chartRef.current = null;
      chart.destroy();
    };
  }, []);

  useEffect(() => {
    chartRef.current?.setData(data);
  }, [data]);

  return <div className="cpu-load-history">
    <div className="cpu-load-history-head"><span>{label}</span><strong>{current}</strong></div>
    <div ref={containerRef} className="cpu-load-history-canvas" aria-hidden="true" />
  </div>;
}
