import type { Inventory, TemperatureSeries } from "../api";
import { useTranslation } from "../i18n";

const STALE_BIOS_YEARS = 5;

function latestTemperature(series: TemperatureSeries): number | null {
  const point = series.points[series.points.length - 1];
  return point && Number.isFinite(point.value) ? point.value : null;
}

export function parseBIOSDate(value: string | undefined): Date | null {
  if (!value) return null;
  const match = value.trim().match(/^(\d{1,2})\/(\d{1,2})\/(\d{4})$/);
  if (!match) return null;
  const [, month, day, year] = match;
  const date = new Date(Number(year), Number(month) - 1, Number(day));
  return date.getFullYear() === Number(year) && date.getMonth() === Number(month) - 1 && date.getDate() === Number(day) ? date : null;
}

export function isStaleBIOS(date: Date, now = new Date()): boolean {
  const threshold = new Date(now);
  threshold.setFullYear(threshold.getFullYear() - STALE_BIOS_YEARS);
  return date < threshold;
}

interface Props {
  identity: Inventory | null;
  temperatures: TemperatureSeries[];
}

export default function MotherboardPanel({ identity, temperatures }: Props) {
  const { t, intlTag } = useTranslation();
  const system = identity?.items.find((item) => item.id === "system");
  const attrs = system?.attrs;
  const board = [attrs?.board_vendor, attrs?.board_name].filter(Boolean).join(" ");
  const biosDate = parseBIOSDate(attrs?.bios_date);
  const readings = temperatures.map((series) => ({ series, value: latestTemperature(series) })).filter((reading): reading is { series: TemperatureSeries; value: number } => reading.value !== null);
  const hasHardware = Boolean(board || attrs?.board_version || attrs?.bios_vendor || attrs?.bios_version || biosDate);

  if (!hasHardware && readings.length === 0) return null;

  const dateFormat = new Intl.DateTimeFormat(intlTag, { dateStyle: "long" });
  const temperatureFormat = new Intl.NumberFormat(intlTag, { maximumFractionDigits: 1 });

  return <article className="control-panel motherboard-panel">
    <div className="panel-title-row"><h2>{t.motherboardTitle}</h2></div>
    {hasHardware && <dl className="motherboard-details">
      {board && <div><dt>{t.motherboardLabel}</dt><dd>{board}</dd></div>}
      {attrs?.board_version && <div><dt>{t.motherboardVersionLabel}</dt><dd>{attrs.board_version}</dd></div>}
      {attrs?.bios_vendor && <div><dt>{t.biosVendorLabel}</dt><dd>{attrs.bios_vendor}</dd></div>}
      {attrs?.bios_version && <div><dt>{t.biosVersionLabel}</dt><dd>{attrs.bios_version}</dd></div>}
      {biosDate && <div><dt>{t.biosDateLabel}</dt><dd>{dateFormat.format(biosDate)}{isStaleBIOS(biosDate) && <span className="bios-stale">{t.biosStale}</span>}</dd></div>}
    </dl>}
    {readings.length > 0 && <div className="motherboard-temperatures" aria-label={t.cpuTemperaturesLabel}>
      <h3>{t.cpuTemperaturesLabel}</h3>
      <ul>{readings.map(({ series, value }) => <li key={`${series.chip}:${series.sensor}`}><span>{t.cpuTemperatureLabel(series.chip, series.sensor)}</span><strong>{t.temperatureCelsius(temperatureFormat.format(value))}</strong></li>)}</ul>
    </div>}
  </article>;
}
