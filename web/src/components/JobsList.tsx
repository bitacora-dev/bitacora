import { hasInstant, type Job } from "../api";
import { jobLogTarget, type LogRefTarget } from "../logrefs";
import { useTranslation } from "../i18n";

// The same bounds the event detail view uses: a job's stats map has canonical
// keys when an extractor recognizes them and free-form keys when it does not.
const MAX_JOB_STATS = 6;
const MAX_JOB_STAT_KEY_LENGTH = 64;
const MAX_JOB_STAT_VALUE_LENGTH = 160;
const SUMMARY_STATS = ["files_transferred", "bytes_transferred", "files_deleted", "files_checked", "errors"];

export interface JobStat {
  key: string;
  value: string;
}

function bounded(value: string, max: number): string {
  const normalized = value.replace(/\s+/g, " ").trim();
  return normalized.length > max ? `${normalized.slice(0, max - 1)}…` : normalized;
}

// Canonical keys first, in the order ADR-0010 lists them, then anything the
// extractor invented, alphabetically.
export function jobStats(job: Job): JobStat[] {
  return Object.entries(job.stats ?? {})
    .filter(([, value]) => value !== null && value !== undefined && value !== "")
    .sort(([left], [right]) => {
      const leftPriority = SUMMARY_STATS.indexOf(left);
      const rightPriority = SUMMARY_STATS.indexOf(right);
      if (leftPriority !== -1 || rightPriority !== -1) {
        return (leftPriority === -1 ? SUMMARY_STATS.length : leftPriority) - (rightPriority === -1 ? SUMMARY_STATS.length : rightPriority);
      }
      return left.localeCompare(right);
    })
    .slice(0, MAX_JOB_STATS)
    .map(([key, value]) => ({ key: bounded(key, MAX_JOB_STAT_KEY_LENGTH), value: bounded(String(value), MAX_JOB_STAT_VALUE_LENGTH) }));
}

interface Props {
  jobs: Job[];
  onShowLogs?: (job: Job, target: LogRefTarget) => void;
}

export default function JobsList({ jobs, onShowLogs }: Props) {
  const { t, intlTag } = useTranslation();
  if (jobs.length === 0) return <div className="events-empty"><h3>{t.jobsEmptyHeading}</h3><p>{t.jobsEmptyBody}</p></div>;

  return (
    <ul className="event-list">
      {jobs.map((job) => {
        const stats = jobStats(job);
        const logTarget = onShowLogs ? jobLogTarget(job) : null;
        const ranAt = hasInstant(job.finished_at) ? job.finished_at : job.started_at;
        return (
          <li key={job.id}>
            <div>
              <span>{new Date(ranAt).toLocaleTimeString(intlTag)}</span>
              <span>{t.jobStatus[job.status]}</span>
              <span>{job.job_name}</span>
              {job.trigger && <span>{t.jobTrigger(job.trigger)}</span>}
            </div>
            <p>{t.jobExitStatus(job.exit_code, job.duration_seconds)}</p>
            {(hasInstant(job.next_expected) || job.signal || job.peer_host_id) && (
              <p className="job-context">
                {hasInstant(job.next_expected) && <span>{t.jobNextExpected(new Date(job.next_expected).toLocaleString(intlTag))}</span>}
                {job.signal && <span>{t.jobSignal(job.signal)}</span>}
                {job.peer_host_id && <span>{t.jobPeer(job.peer_host_id)}</span>}
              </p>
            )}
            {stats.length > 0 && (
              <details className="event-details">
                <summary>
                  {t.jobStatsLabel}
                  <span>{stats.slice(0, 2).map(({ key, value }) => `${t.jobStat(key)}: ${value}`).join(" · ")}</span>
                </summary>
                <dl>
                  {stats.map(({ key, value }) => (
                    <div key={key}>
                      <dt>{t.jobStat(key)}</dt>
                      <dd>{value}</dd>
                    </div>
                  ))}
                </dl>
              </details>
            )}
            {logTarget && onShowLogs && (
              <div className="event-actions">
                <button type="button" className="link-button" aria-label={t.jobLogsAria(job.job_name)} onClick={() => onShowLogs(job, logTarget)}>
                  {t.jobLogsButton}
                </button>
              </div>
            )}
          </li>
        );
      })}
    </ul>
  );
}
