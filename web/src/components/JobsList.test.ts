import { describe, expect, it } from "vitest";
import type { Job } from "../api";
import { jobStats } from "./JobsList";

const job: Job = {
  id: "job-1",
  job_name: "rclone-backup",
  host_id: "host-1",
  started_at: "2026-09-19T03:00:00Z",
  finished_at: "2026-09-19T03:04:00Z",
  duration_seconds: 240,
  status: "success",
  exit_code: 0,
};

describe("jobStats", () => {
  it("renders only canonical statistics, in ADR-0010 order", () => {
    expect(jobStats({ ...job, stats: { errors: 0, bucket: "offsite", files_transferred: 12, bytes_transferred: 4096 } })).toEqual([
      { key: "files_transferred", value: "12" },
      { key: "bytes_transferred", value: "4096" },
      { key: "errors", value: "0" },
    ]);
  });

  it("drops unknown extractor keys instead of exposing raw protocol names", () => {
    const stats = jobStats({
      ...job,
      stats: { ["a\n".repeat(100)]: "bounded key", note: "x".repeat(8_000) },
    });
    expect(stats).toEqual([]);
  });

  it("keeps known keys while dropping an unknown one", () => {
    const stats = jobStats({ ...job, stats: { files_transferred: 20, bytes_total: 1024 } });
    expect(stats).toEqual([{ key: "files_transferred", value: "20" }]);
  });

  it("drops empty values rather than rendering a blank row", () => {
    expect(jobStats({ ...job, stats: { files_transferred: 3, note: "", missing: null } })).toEqual([
      { key: "files_transferred", value: "3" },
    ]);
  });

  it("returns nothing for a job the extractor never populated", () => {
    expect(jobStats(job)).toEqual([]);
  });
});
