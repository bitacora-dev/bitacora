import type { Summary } from "../api";
import { formatBytes } from "../bytes";
import { useTranslation } from "../i18n";
import { memorySnapshot } from "../overview";
import Sparkline from "./Sparkline";

// Memory as a meter, not a chart. The question is "how much room is left",
// which a bar answers at a glance and a line chart makes the reader estimate
// off an axis. The window still shows as a trace behind the bar, so a host
// that has been climbing all window looks different from one that is simply
// busy.
//
// Swap is a separate bar because it answers a separate question: RAM at 80 %
// is a well-used machine, swap at 80 % is a machine in trouble.
export default function MemoryPanel({ summary }: { summary: Summary }) {
  const { t, intlTag } = useTranslation();
  const memory = memorySnapshot(summary);
  const percent = new Intl.NumberFormat(intlTag, { style: "percent", maximumFractionDigits: 1 });
  const bytes = (value: number) => formatBytes(value, intlTag);

  const swapRatio =
    memory.swapTotalBytes !== null && memory.swapUsedBytes !== null && memory.swapTotalBytes > 0
      ? memory.swapUsedBytes / memory.swapTotalBytes
      : null;

  return (
    <article className="control-panel memory-panel">
      <div className="panel-title-row">
        <h2>{t.bandMemoryLabel}</h2>
        <strong className="memory-panel-value">{memory.usedRatio === null ? t.noSamples : percent.format(memory.usedRatio)}</strong>
      </div>

      <div className="memory-meter">
        <Sparkline points={summary.memory} ceiling={1} tone="gold" />
        <div
          className="usage-bar usage-bar--memory"
          role="img"
          aria-label={memory.usedRatio === null ? t.noSamples : t.memoryUsedRatio(percent.format(memory.usedRatio))}
        >
          <span style={{ width: `${(memory.usedRatio ?? 0) * 100}%` }} />
        </div>
      </div>

      <p className="memory-panel-context">
        {memory.usedBytes !== null && memory.totalBytes !== null
          ? t.memoryOfTotal(bytes(memory.usedBytes), bytes(memory.totalBytes))
          : t.noSamples}
        {memory.availableBytes !== null && <span>{t.memoryAvailable(bytes(memory.availableBytes))}</span>}
      </p>

      {/* A host with no swap configured reports a total of zero, which is a
          measurement and is said as such. A host that reported neither half
          of swap gets no swap line at all, because "0 of 0" there would be
          an answer the hub never gave. */}
      {memory.swapTotalBytes !== null && memory.swapUsedBytes !== null && (
        memory.swapTotalBytes === 0 ? (
          <p className="memory-panel-swap">{t.memorySwapNone}</p>
        ) : (
          <div className="memory-panel-swap">
            <div className="memory-swap-head">
              <span>{t.memorySwapLabel}</span>
              <strong>{t.memoryOfTotal(bytes(memory.swapUsedBytes), bytes(memory.swapTotalBytes))}</strong>
            </div>
            <div
              className="usage-bar usage-bar--swap"
              role="img"
              aria-label={t.memorySwapUsage(percent.format(swapRatio ?? 0))}
            >
              <span style={{ width: `${(swapRatio ?? 0) * 100}%` }} />
            </div>
          </div>
        )
      )}
    </article>
  );
}
