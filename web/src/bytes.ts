const BYTE_UNITS = ["byte", "kilobyte", "megabyte", "gigabyte", "terabyte"] as const;

const BYTE_UNIT_SIZES = [1, 1_000, 1_000_000, 1_000_000_000, 1_000_000_000_000] as const;

export function formatBytes(value: unknown, locale: string): string {
  const bytes = typeof value === "number" ? value : Number(value);
  if (!Number.isFinite(bytes) || bytes < 0) return "—";

  let unitIndex = 0;
  while (unitIndex < BYTE_UNITS.length - 1 && bytes >= BYTE_UNIT_SIZES[unitIndex + 1]) {
    unitIndex += 1;
  }

  return new Intl.NumberFormat(locale, {
    style: "unit",
    unit: BYTE_UNITS[unitIndex],
    unitDisplay: "short",
    maximumFractionDigits: 1,
  }).format(bytes / BYTE_UNIT_SIZES[unitIndex]);
}
