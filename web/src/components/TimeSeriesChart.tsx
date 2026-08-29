import { useEffect, useRef } from "react";
import uPlot from "uplot";
import "uplot/dist/uPlot.min.css";
import type { SeriesPoint } from "../api";

interface Props {
  title: string;
  points: SeriesPoint[];
  color: string;
  /** Format a value for the y-axis and legend, e.g. "42%" or "1.2 GB". */
  formatValue?: (v: number) => string;
}

// A single uPlot line chart, sized to its container and touch-usable out
// of the box (ADR-0014: "gráficas manejables con el dedo").
export default function TimeSeriesChart({ title, points, color, formatValue }: Props) {
  const containerRef = useRef<HTMLDivElement>(null);
  const chartRef = useRef<uPlot | null>(null);

  useEffect(() => {
    const container = containerRef.current;
    if (!container) return;

    const data: uPlot.AlignedData = [
      points.map((p) => Math.floor(new Date(p.ts).getTime() / 1000)),
      points.map((p) => p.value),
    ];

    const opts: uPlot.Options = {
      width: container.clientWidth,
      height: 180,
      title,
      cursor: { drag: { x: true, y: false } },
      scales: { x: { time: true } },
      axes: [
        {},
        {
          values: formatValue ? (_u, vals) => vals.map((v) => formatValue(v)) : undefined,
        },
      ],
      series: [
        {},
        {
          label: title,
          stroke: color,
          fill: color + "22",
          width: 2,
          points: { show: points.length < 60 },
        },
      ],
    };

    const chart = new uPlot(opts, data, container);
    chartRef.current = chart;

    const resize = new ResizeObserver(() => {
      chart.setSize({ width: container.clientWidth, height: 180 });
    });
    resize.observe(container);

    return () => {
      resize.disconnect();
      chart.destroy();
      chartRef.current = null;
    };
  }, [points, title, color, formatValue]);

  return <div ref={containerRef} className="w-full" />;
}
