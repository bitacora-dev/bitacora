import type { SeriesPoint } from "../api";

// The constants band shows each cell's window as a trace behind its value.
// It is drawn as an inline SVG rather than a uPlot canvas on purpose: five
// of them sit on the first screen, they carry no cursor, no axes and no
// readout, and the cell's own big number is the readable current value
// web/DESIGN.md requires. Spending five uPlot instances and five
// ResizeObservers on pure background would cost more than the band is worth.
//
// It is decoration layered under real text, so it is aria-hidden and never
// the only place a value appears.

export const SPARKLINE_VIEWBOX_WIDTH = 100;
export const SPARKLINE_VIEWBOX_HEIGHT = 32;

export interface SparklineGeometry {
  line: string;
  area: string;
}

// Builds the trace in viewBox units; the SVG stretches it to the cell with a
// non-uniform preserveAspectRatio, so no measurement of the DOM is needed.
//
// A single sample has no shape: it is drawn as a flat segment at its own
// height rather than as a dot, because the band's trace answers "has this
// been moving", and one sample's answer is "nothing has moved yet".
export function sparklineGeometry(points: SeriesPoint[], ceiling?: number): SparklineGeometry | null {
  const usable = points
    .filter((point) => Number.isFinite(point.value) && Number.isFinite(new Date(point.ts).getTime()))
    .sort((left, right) => new Date(left.ts).getTime() - new Date(right.ts).getTime());
  if (usable.length === 0) return null;

  const values = usable.map((point) => point.value);
  const low = Math.min(0, ...values);
  const explicit = ceiling !== undefined && Number.isFinite(ceiling) && ceiling > low ? ceiling : null;
  const high = explicit ?? Math.max(...values);
  // A flat series still deserves a line. Without the guard a zero span
  // divides by zero and the trace disappears exactly when the host is calm.
  const span = high - low === 0 ? 1 : high - low;

  const x = (index: number) => (usable.length === 1 ? SPARKLINE_VIEWBOX_WIDTH : (index / (usable.length - 1)) * SPARKLINE_VIEWBOX_WIDTH);
  const y = (value: number) => SPARKLINE_VIEWBOX_HEIGHT - ((Math.max(low, Math.min(high, value)) - low) / span) * SPARKLINE_VIEWBOX_HEIGHT;

  const coordinates = usable.map((point, index) => `${x(index).toFixed(2)},${y(point.value).toFixed(2)}`);
  const line = usable.length === 1
    ? `M0,${y(values[0]).toFixed(2)} L${SPARKLINE_VIEWBOX_WIDTH},${y(values[0]).toFixed(2)}`
    : `M${coordinates[0]} ${coordinates.slice(1).map((pair) => `L${pair}`).join(" ")}`;
  const area = `${line} L${SPARKLINE_VIEWBOX_WIDTH},${SPARKLINE_VIEWBOX_HEIGHT} L0,${SPARKLINE_VIEWBOX_HEIGHT} Z`;
  return { line, area };
}

interface Props {
  points: SeriesPoint[];
  // Fixed upper bound for a bounded signal (a ratio), so a calm host does not
  // get its noise stretched to full height. Omitted for unbounded ones.
  ceiling?: number;
  tone: "cyan" | "gold" | "green";
}

export default function Sparkline({ points, ceiling, tone }: Props) {
  const geometry = sparklineGeometry(points, ceiling);
  if (geometry === null) return null;

  return (
    <svg
      className={`sparkline sparkline--${tone}`}
      viewBox={`0 0 ${SPARKLINE_VIEWBOX_WIDTH} ${SPARKLINE_VIEWBOX_HEIGHT}`}
      preserveAspectRatio="none"
      aria-hidden="true"
      focusable="false"
    >
      <path className="sparkline-area" d={geometry.area} />
      <path className="sparkline-line" d={geometry.line} />
    </svg>
  );
}
