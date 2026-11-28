import { hasInstant, type Job } from "../api";
import { groupByDay } from "../dayGroups";
import { jobLogTarget, type LogRefTarget } from "../logrefs";
import { useTranslation } from "../i18n";
import { jobProgressRatio, splitJobs } from "../overview";
import { formatBytes } from "../bytes";
import RowInstant, { DayHeading } from "./RowInstant";

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

// The overview is a status surface, not a raw protocol dump. Unknown extractor
// keys remain reachable in the linked log block, where their provenance is
// visible, instead of being rendered as untranslated implementation details.
export function jobStats(job: Job): JobStat[] {
  return Object.entries(job.stats ?? {})
    .filter(([key]) => SUMMARY_STATS.includes(key))
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

function formatJobStat(key: string, value: string, locale: string): string {
  if (key !== "bytes_transferred") return value;
  const bytes = Number(value);
  return Number.isFinite(bytes) && bytes >= 0 ? formatBytes(bytes, locale) : value;
}

interface Props {
  jobs: Job[];
  onShowLogs?: (job: Job, target: LogRefTarget) => void;
  // Caps the finished rows on the dense overview. The running job is never
  // capped: a run in progress is the one row the operator came for.
  maxRows?: number;
}

// The instant a job last moved: when it finished, or when it started if it
// is still running. The same rule the hub orders ListLatestJobs by, so the
// dates on screen match the order the rows arrive in.
function jobInstant(job: Job): string {
  return hasInstant(job.finished_at) ? job.finished_at : job.started_at;
}

export default function JobsList({ jobs, onShowLogs, maxRows }: Props) {
  const { t } = useTranslation();
  if (jobs.length === 0) return <div className="events-empty"><h3>{t.jobsEmptyHeading}</h3><p>{t.jobsEmptyBody}</p></div>;

  const { running, finished } = splitJobs(jobs);
  const shown = maxRows === undefined ? finished : finished.slice(0, maxRows);

  return (
    <div className="jobs-list">
      {running.length > 0 && (
        <section className="jobs-running" aria-label={t.jobsRunningLabel}>
          {running.map((job) => <RunningJob key={job.id} job={job} onShowLogs={onShowLogs} />)}
        </section>
      )}

      {shown.length > 0 && (
        <div className="event-days">
          {groupByDay(shown, jobInstant).map((day) => (
            <section className="event-day" key={day.key}>
              <DayHeading dayKey={day.key} />
              <ul className="event-list event-list--rows job-rows">
                {day.items.map((job) => <JobRow key={job.id} job={job} onShowLogs={onShowLogs} />)}
              </ul>
            </section>
          ))}
        </div>
      )}
    </div>
  );
}

// The run in progress, pinned above the history with its own treatment.
//
// The provenance the hub sends beside a run: when it is next due, the signal
// that stopped it and the peer it replicated with. Shared so a running job and
// a finished one can never disagree about which of them is worth showing.
function JobContext({ job }: { job: Job }) {
  const { t, intlTag } = useTranslation();
  if (!hasInstant(job.next_expected) && !job.signal && !job.peer_host_id) return null;

  return (
    <p className="job-context">
      {hasInstant(job.next_expected) && <span>{t.jobNextExpected(new Date(job.next_expected).toLocaleString(intlTag))}</span>}
      {job.signal && <span>{t.jobSignal(job.signal)}</span>}
      {job.peer_host_id && <span>{t.jobPeer(job.peer_host_id)}</span>}
    </p>
  );
}

// The progress bar is drawn only when the extractor reported both a done and
// a total figure (overview.jobProgressRatio). Most tools report neither, and
// a bar derived from "files transferred so far" with no total would be a
// guess dressed as a measurement — the counters below say what is known.
function RunningJob({ job, onShowLogs }: { job: Job; onShowLogs?: (job: Job, target: LogRefTarget) => void }) {
  const { t, intlTag } = useTranslation();
  const progress = jobProgressRatio(job);
  const percent = new Intl.NumberFormat(intlTag, { style: "percent", maximumFractionDigits: 0 });
  const stats = jobStats(job);
  const logTarget = onShowLogs ? jobLogTarget(job) : null;

  return (
    <article className="job-running">
      <div className="job-running-head">
        <strong>{job.job_name}</strong>
        <span className="job-status job-status--running">{t.jobStatus.running}</span>
        <RowInstant instant={job.started_at} />
      </div>
      {/* What launched this run, and who it is talking to. The finished rows
          carry both; dropping them from the one row an operator actually came
          for would hide which peer a replication in progress is writing to. */}
      <p className="job-running-trigger">{job.trigger ? t.jobTrigger(job.trigger) : t.jobTriggerUnreported}</p>
      <JobContext job={job} />
      {progress === null ? (
        <p className="job-running-note">{t.jobProgressUnknown}</p>
      ) : (
        <div className="job-progress">
          <div className="usage-bar" role="img" aria-label={t.jobProgressAria(job.job_name, percent.format(progress))}>
            <span style={{ width: `${progress * 100}%` }} />
          </div>
          <strong>{percent.format(progress)}</strong>
        </div>
      )}
      {stats.length > 0 && (
        <p className="job-running-stats">{stats.map(({ key, value }) => `${t.jobStat(key)}: ${formatJobStat(key, value, intlTag)}`).join(" · ")}</p>
      )}
      {logTarget && onShowLogs && (
        <div className="event-actions">
          <button type="button" className="link-button" aria-label={t.jobLogsAria(job.job_name)} onClick={() => onShowLogs(job, logTarget)}>
            {t.jobLogsButton}
          </button>
        </div>
      )}
    </article>
  );
}

// One finished run: time, job, what launched it, the verdict and how long it
// took, with its statistics and its log lines behind the disclosure.
//
// "What launched it" is `trigger`, the only provenance the hub sends for a
// run. There is no tool field in ADR-0010's Job, so none is invented here.
function JobRow({ job, onShowLogs }: { job: Job; onShowLogs?: (job: Job, target: LogRefTarget) => void }) {
  const { t, intlTag } = useTranslation();
  const stats = jobStats(job);
  const logTarget = onShowLogs ? jobLogTarget(job) : null;
  const seconds = new Intl.NumberFormat(intlTag, { maximumFractionDigits: 1 });

  return (
    <li className="event-row job-row">
      <details>
        <summary>
          <RowInstant instant={jobInstant(job)} />
          <span className="job-row-name">{job.job_name}</span>
          <span className="job-row-trigger">{job.trigger ? t.jobTrigger(job.trigger) : t.jobTriggerUnreported}</span>
          <span className={`job-status job-status--${job.status}`}>{t.jobStatus[job.status]}</span>
          <span className="job-row-duration">{t.jobDuration(seconds.format(job.duration_seconds))}</span>
        </summary>
        <div className="event-row-body">
          <p className="event-row-source">{t.jobExitStatus(job.exit_code, job.duration_seconds)}</p>
          {/* Repeated inside the disclosure because the summary's own trigger
              span is hidden below 760 px, and ADR-0014 makes the phone the
              primary client: "what launched this run" has to stay reachable
              there, not only on a desktop. */}
          <p className="job-row-trigger-detail">{job.trigger ? t.jobTrigger(job.trigger) : t.jobTriggerUnreported}</p>
          <JobContext job={job} />
          {stats.length > 0 && (
            <dl>
              {stats.map(({ key, value }) => (
                <div key={key}>
                  <dt>{t.jobStat(key)}</dt>
                  <dd>{formatJobStat(key, value, intlTag)}</dd>
                </div>
              ))}
            </dl>
          )}
          {logTarget && onShowLogs && (
            <div className="event-actions">
              <button type="button" className="link-button" aria-label={t.jobLogsAria(job.job_name)} onClick={() => onShowLogs(job, logTarget)}>
                {t.jobLogsButton}
              </button>
            </div>
          )}
        </div>
      </details>
    </li>
  );
}
