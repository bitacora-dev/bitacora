import type { Inventory, InventoryItem } from "../api";
import { useTranslation } from "../i18n";
import { formatRuntime } from "../relativeTime";

// A UPS that has switched to battery is the most urgent thing this dashboard
// can say: the wall power is gone and the runtime estimate is a countdown to
// an unplanned shutdown. So this panel keeps one fixed slot at the top of the
// page and changes intensity in place — a single quiet line while mains power
// is present, an alarm when it is not. It never moves, so the eye already
// knows where to look on the day it matters.
//
// A host without a UPS renders nothing at all. The `ups` collector only
// registers when the PowerUPS capability is detected, so a missing inventory
// means "no UPS here", not "0 % battery".

export interface UPSReading {
  id: string;
  name: string;
  // Raw NUT status word ("OL", "OB DISCHRG"). Shown as supporting evidence,
  // never parsed for meaning beyond what the collector already derived.
  status: string | null;
  // null means the host never reported it. It must not collapse into false:
  // "on mains" is a claim, and absence does not support it.
  onBattery: boolean | null;
  chargeRatio: number | null;
  runtimeSeconds: number | null;
  model: string | null;
}

function text(attrs: Record<string, string> | undefined, key: string): string | null {
  const value = attrs?.[key];
  return typeof value === "string" && value.trim() !== "" ? value.trim() : null;
}

function bool(attrs: Record<string, string> | undefined, key: string): boolean | null {
  const value = attrs?.[key];
  if (value === "true") return true;
  if (value === "false") return false;
  return null;
}

// NUT prints battery.charge verbatim, so it arrives as "100" on one UPS and
// "100.0" on another. Anything outside 0–100 is a reading this panel cannot
// describe, and a bar drawn from it would be a guess.
export function chargeRatio(value: string | undefined): number | null {
  // Number("") is 0, so an empty reading has to be rejected before the parse
  // rather than drawn as a flat battery.
  if (value === undefined || value.trim() === "") return null;
  const percentage = Number(value);
  if (!Number.isFinite(percentage) || percentage < 0 || percentage > 100) return null;
  return percentage / 100;
}

export function runtimeSeconds(value: string | undefined): number | null {
  if (value === undefined || value.trim() === "") return null;
  const seconds = Number(value);
  return Number.isFinite(seconds) && seconds >= 0 ? seconds : null;
}

export function readUPS(item: InventoryItem): UPSReading {
  return {
    id: item.id,
    name: item.name || item.id,
    status: text(item.attrs, "status"),
    onBattery: bool(item.attrs, "on_battery"),
    chargeRatio: chargeRatio(item.attrs?.battery_charge_pct),
    runtimeSeconds: runtimeSeconds(item.attrs?.runtime_seconds),
    model: text(item.attrs, "model"),
  };
}

// The agent builds this inventory with a nil slice when NUT is unreachable,
// which reaches the browser as `"items": null` rather than an empty array.
export function readUPSInventory(inventory: Inventory | null): UPSReading[] {
  return (inventory?.items ?? []).map(readUPS);
}

// True only when a unit says so. An unreported unit is not evidence of mains
// power, so it can never quiet the alarm raised by another unit.
export function anyOnBattery(readings: UPSReading[]): boolean {
  return readings.some((reading) => reading.onBattery === true);
}

interface Props {
  inventory: Inventory | null;
}

export default function PowerPanel({ inventory }: Props) {
  const { t, intlTag } = useTranslation();
  const readings = readUPSInventory(inventory);
  if (readings.length === 0) return null;

  const onBattery = anyOnBattery(readings);
  const percent = new Intl.NumberFormat(intlTag, { style: "percent", maximumFractionDigits: 0 });

  return (
    <section className={onBattery ? "power-strip power-strip--alarm" : "power-strip"} aria-label={t.powerHeading}>
      {readings.map((reading) => (
        <article key={reading.id} className={reading.onBattery === true ? "power-panel power-panel--alarm" : "power-panel"}>
          {reading.onBattery === true ? (
            <>
              <div className="power-alarm-heading">
                <h2>{t.powerOnBatteryHeading}</h2>
                {/* Only the state phrase is live. The runtime estimate ticks
                    every poll, and announcing that would bury the one change
                    a screen reader user needs to hear. */}
                <p aria-live="polite">{t.powerOnBatteryBody}</p>
              </div>
              <div className="power-alarm-readout">
                {reading.runtimeSeconds !== null ? (
                  <p className="power-runtime">
                    <strong>{formatRuntime(reading.runtimeSeconds, intlTag)}</strong>
                    <span>{t.powerRuntimeRemaining}</span>
                  </p>
                ) : (
                  <p className="power-runtime power-runtime--unknown">{t.powerRuntimeUnknown}</p>
                )}
                {reading.chargeRatio !== null && (
                  <div className="power-charge">
                    <span>{t.powerBatteryCharge(percent.format(reading.chargeRatio))}</span>
                    <div className="usage-bar usage-bar--battery" aria-hidden="true">
                      <span style={{ width: `${reading.chargeRatio * 100}%` }} />
                    </div>
                  </div>
                )}
              </div>
              <p className="power-context">{describeUnit(reading, t)}</p>
            </>
          ) : (
            <p className="power-line">
              <strong>{t.powerHeading}</strong>
              <span aria-live="polite">{reading.onBattery === false ? t.powerOnMains : t.powerStateUnknown}</span>
              {reading.chargeRatio !== null && <span>{t.powerBatteryCharge(percent.format(reading.chargeRatio))}</span>}
              {reading.runtimeSeconds !== null && <span>{t.powerRuntimeEstimate(formatRuntime(reading.runtimeSeconds, intlTag))}</span>}
              <span className="power-unit">{describeUnit(reading, t)}</span>
            </p>
          )}
        </article>
      ))}
    </section>
  );
}

function describeUnit(reading: UPSReading, t: ReturnType<typeof useTranslation>["t"]): string {
  return [reading.model ?? reading.name, reading.status ? t.powerStatusWord(reading.status) : null].filter(Boolean).join(" · ");
}
