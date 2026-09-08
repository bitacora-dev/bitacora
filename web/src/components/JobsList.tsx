import type { Job } from "../api";
import { useTranslation } from "../i18n";

export default function JobsList({ jobs }: { jobs: Job[] }) {
  const { t, intlTag } = useTranslation();
  if (jobs.length === 0) return <div className="events-empty"><h3>{t.jobsEmptyHeading}</h3><p>{t.jobsEmptyBody}</p></div>;
  return <ul className="event-list">{jobs.map((job) => <li key={job.id}><div><span>{new Date(job.finished_at).toLocaleTimeString(intlTag)}</span><span>{t.jobStatus[job.status]}</span><span>{job.job_name}</span></div><p>{t.jobExitStatus(job.exit_code, job.duration_seconds)}</p></li>)}</ul>;
}
