import { useCallback, useMemo } from "react";
import type { PublicSurface, SeriesPoint } from "../api";
import { useTranslation } from "../i18n";
import TimeSeriesChart from "./TimeSeriesChart";

export function latestValue(points: SeriesPoint[]): number | null {
  const point = points[points.length - 1];
  return point && Number.isFinite(point.value) ? point.value : null;
}

// windowIncrease answers "how many new ones arrived inside this window",
// which is a different question from the counter's current value.
//
// It sums only the positive steps. The public-surface collector re-counts
// lines in the *current* auth log, so logrotate drops the counter back down;
// a plain last-minus-first would then report a negative number of new
// attempts, and clamping that to zero would read as "nothing happened".
// Fewer than two samples yields null, not 0: one sample proves a total, it
// proves nothing about change.
export function windowIncrease(points: SeriesPoint[]): number | null {
  if (points.length < 2) return null;
  let increase = 0;
  for (let i = 1; i < points.length; i += 1) {
    const step = points[i].value - points[i - 1].value;
    if (step > 0) increase += step;
  }
  return increase;
}

// hasPublicSurfaceData reads the five raw collector series, not the hub's
// derived per-minute one: a host that has reported exactly one sample so far
// has real data even though nothing can be differentiated from it yet.
export function hasPublicSurfaceData(surface: PublicSurface): boolean {
  return [
    surface.ssh_failed_logins_total,
    surface.fail2ban_jails_total,
    surface.fail2ban_banned_total,
    surface.firewall_rules_total,
    surface.ovh_traffic_used_ratio,
  ].some((series) => series.length > 0);
}

interface Props {
  surface: PublicSurface;
  windowMinutes: number;
}

// PublicSurfacePanel renders the internet-facing security signals.
//
// The shape is deliberate. The question this panel answers is not "how many
// failed logins exist" but "am I being attacked right now", and a standing
// total cannot answer it: 400 failed attempts reads the same for a quiet
// month and for the last twenty minutes. The rate over the window can, so
// the chart carries the rate and the totals sit beside it as context.
//
// Absence is never drawn as zero. A signal with no samples says so in words.
export default function PublicSurfacePanel({ surface, windowMinutes }: Props) {
  const { t, intlTag } = useTranslation();

  const countFormat = useMemo(() => new Intl.NumberFormat(intlTag, { maximumFractionDigits: 0 }), [intlTag]);
  const rateFormat = useMemo(() => new Intl.NumberFormat(intlTag, { maximumFractionDigits: 1 }), [intlTag]);
  const percentFormat = useMemo(
    () => new Intl.NumberFormat(intlTag, { style: "percent", minimumFractionDigits: 1, maximumFractionDigits: 1 }),
    [intlTag],
  );
  // TimeSeriesChart rebuilds its uPlot instance when these props change
  // identity, so keep them stable across the ten-second poll.
  const formatRate = useCallback((value: number) => rateFormat.format(value), [rateFormat]);
  const describeRate = useCallback(
    (point: SeriesPoint) => ({ primary: t.sshFailedLoginsPerMinute(rateFormat.format(point.value)) }),
    [rateFormat, t],
  );

  if (!hasPublicSurfaceData(surface)) {
    return (
      <article className="control-panel public-surface-panel public-surface-panel--pending">
        <div className="panel-title-row">
          <h2>{t.publicSurfaceHeading}</h2>
        </div>
        <div className="events-empty">
          <h3>{t.publicSurfacePendingHeading}</h3>
          <p>{t.publicSurfacePendingBody}</p>
          <p>{t.publicSurfacePendingHowTo}</p>
        </div>
      </article>
    );
  }

  const sshNew = windowIncrease(surface.ssh_failed_logins_total);
  const sshCumulative = latestValue(surface.ssh_failed_logins_total);
  const jails = latestValue(surface.fail2ban_jails_total);
  const banned = latestValue(surface.fail2ban_banned_total);
  const newBans = windowIncrease(surface.fail2ban_banned_total);
  const firewallRules = latestValue(surface.firewall_rules_total);
  const trafficRatio = latestValue(surface.ovh_traffic_used_ratio);

  const unreported = <span className="public-surface-unreported">{t.publicSurfaceNotReported}</span>;

  return (
    <>
      <TimeSeriesChart
        title={t.sshFailedLoginsTitle}
        points={surface.ssh_failed_logins_per_minute}
        color="#f8d66d"
        formatAxisValue={formatRate}
        describePoint={describeRate}
      />
      <article className="control-panel public-surface-panel">
        <div className="panel-title-row">
          <h2>{t.publicSurfaceHeading}</h2>
        </div>
        <p className="public-surface-note">{t.publicSurfaceIntro}</p>
        <dl className="public-surface-details">
          <div>
            <dt>{t.sshFailedLoginsWindowLabel}</dt>
            <dd>
              {sshNew === null ? (
                unreported
              ) : (
                <>
                  <strong>{t.sshFailedLoginsWindowValue(countFormat.format(sshNew), windowMinutes)}</strong>
                  {sshCumulative !== null && <span>{t.sshFailedLoginsCumulative(countFormat.format(sshCumulative))}</span>}
                </>
              )}
            </dd>
          </div>
          <div>
            <dt>{t.fail2banBannedLabel}</dt>
            <dd>
              {banned === null ? (
                unreported
              ) : (
                <>
                  <strong>{countFormat.format(banned)}</strong>
                  {newBans !== null && newBans > 0 && <span>{t.fail2banBannedNew(countFormat.format(newBans))}</span>}
                </>
              )}
            </dd>
          </div>
          <div>
            <dt>{t.fail2banJailsLabel}</dt>
            <dd>{jails === null ? unreported : <strong>{countFormat.format(jails)}</strong>}</dd>
          </div>
          <div>
            <dt>{t.firewallRulesLabel}</dt>
            <dd>{firewallRules === null ? unreported : <strong>{countFormat.format(firewallRules)}</strong>}</dd>
          </div>
          <div>
            <dt>{t.ovhTrafficLabel}</dt>
            <dd>{trafficRatio === null ? unreported : <strong>{percentFormat.format(trafficRatio)}</strong>}</dd>
          </div>
        </dl>
        <p className="public-surface-note">{t.publicSurfaceTotalsNote}</p>
      </article>
    </>
  );
}
