import { hasInstant, type BitacoraEvent, type Job } from "./api";

// The log viewer asks the hub for one bounded page at a time.
export const LOG_PAGE_SIZE = 50;

// A durable block is flushed by size or age, so the lines an event references
// can sit noticeably before the event itself. A day on each side keeps the
// block inside the requested range whatever the viewer's offset from UTC; the
// block filter is what makes the resulting page exact, not the range.
export const LOG_REF_WINDOW_MS = 24 * 60 * 60 * 1000;

// A job can reference a long run of lines. Marking rows is a reading aid, not
// a payload, so the set of highlighted ids stays bounded.
export const MAX_REFERENCED_LINES = 500;

// Where a set of log_refs points: one durable block, the first line inside it,
// and every referenced line's entry id.
export interface LogRefTarget {
  blockID: string;
  firstLine: number;
  entryIDs: string[];
  anchorTS: string;
}

// logstore names every durable line "<block_id>:<line>" with a zero-based
// line number — the same coordinate a log ref carries.
export function logEntryID(blockID: string, line: number): string {
  return `${blockID}:${line}`;
}

function validLine(line: number): boolean {
  return Number.isInteger(line) && line >= 0;
}

function target(blockID: string, lines: number[], anchorTS: string): LogRefTarget | null {
  const ordered = [...new Set(lines)].sort((left, right) => left - right).slice(0, MAX_REFERENCED_LINES);
  if (blockID === "" || ordered.length === 0) return null;
  return { blockID, firstLine: ordered[0], entryIDs: ordered.map((line) => logEntryID(blockID, line)), anchorTS };
}

// An event's log_refs may name several blocks. Only the first block is
// reachable in one page, so the target keeps that block and drops the rest
// rather than mixing coordinates that cannot be shown together.
export function eventLogTarget(event: BitacoraEvent): LogRefTarget | null {
  const refs = (event.log_refs ?? []).filter((ref) => ref.block_id !== "" && validLine(ref.line));
  if (refs.length === 0) return null;
  const blockID = refs[0].block_id;
  return target(blockID, refs.filter((ref) => ref.block_id === blockID).map((ref) => ref.line), event.ts);
}

// A job's refs carry an inclusive line range per block. The anchor is when the
// run ended, falling back to when it started for a job still running.
export function jobLogTarget(job: Job): LogRefTarget | null {
  const refs = (job.log_refs ?? []).filter((ref) => ref.block_id !== "" && validLine(ref.from) && validLine(ref.to) && ref.to >= ref.from);
  if (refs.length === 0) return null;
  const blockID = refs[0].block_id;
  const lines: number[] = [];
  for (const ref of refs.filter((candidate) => candidate.block_id === blockID)) {
    for (let line = ref.from; line <= ref.to && lines.length < MAX_REFERENCED_LINES; line += 1) lines.push(line);
  }
  return target(blockID, lines, hasInstant(job.finished_at) ? job.finished_at : job.started_at);
}

// Entries come back in block order, so the page holding a given line is
// determined by the line number alone.
export function logRefPageOffset(line: number, pageSize: number = LOG_PAGE_SIZE): number {
  if (!validLine(line) || pageSize < 1) return 0;
  return Math.floor(line / pageSize) * pageSize;
}

// The log filters hold `datetime-local` values, which is the same
// ISO-without-seconds shape the rest of the view already uses.
export function logRefRange(anchorTS: string): { from: string; to: string } | null {
  const anchor = new Date(anchorTS).getTime();
  if (!Number.isFinite(anchor)) return null;
  return {
    from: new Date(anchor - LOG_REF_WINDOW_MS).toISOString().slice(0, 16),
    to: new Date(anchor + LOG_REF_WINDOW_MS).toISOString().slice(0, 16),
  };
}
