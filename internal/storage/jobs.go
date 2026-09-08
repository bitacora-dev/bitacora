package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/bitacora-dev/bitacora/internal/schema"
	"path/filepath"
	"time"
)

func (s *SQLiteStore) jobsDatabase() (*sql.DB, error) {
	s.jobMu.Lock()
	defer s.jobMu.Unlock()
	if s.jobsDB != nil {
		return s.jobsDB, nil
	}
	db, err := sql.Open("sqlite", filepath.Join(s.dir, "jobs.db"))
	if err != nil {
		return nil, err
	}
	for _, stmt := range []string{"PRAGMA journal_mode=WAL", `CREATE TABLE IF NOT EXISTS jobs (id TEXT PRIMARY KEY, job_name TEXT NOT NULL, host_id TEXT NOT NULL, started_at INTEGER NOT NULL, finished_at INTEGER NOT NULL, duration_seconds REAL NOT NULL, status TEXT NOT NULL, exit_code INTEGER NOT NULL, signal TEXT, stats_json TEXT, schema INTEGER NOT NULL)`, `CREATE INDEX IF NOT EXISTS idx_jobs_host_finished ON jobs(host_id, finished_at)`} {
		if _, err := db.Exec(stmt); err != nil {
			db.Close()
			return nil, err
		}
	}
	s.jobsDB = db
	return db, nil
}
func (s *SQLiteStore) InsertJob(ctx context.Context, job schema.Job) error {
	if err := job.Validate(); err != nil {
		return fmt.Errorf("invalid job: %w", err)
	}
	stats, err := json.Marshal(job.Stats)
	if err != nil {
		return err
	}
	return s.enqueueWrite(ctx, func(ctx context.Context) error {
		db, err := s.jobsDatabase()
		if err != nil {
			return err
		}
		_, err = db.ExecContext(ctx, `INSERT INTO jobs (id,job_name,host_id,started_at,finished_at,duration_seconds,status,exit_code,signal,stats_json,schema) VALUES (?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO NOTHING`, job.ID, job.JobName, job.HostID, job.StartedAt.UnixMilli(), job.FinishedAt.UnixMilli(), job.DurationSecond, string(job.Status), job.ExitCode, job.Signal, string(stats), job.Schema)
		return err
	})
}
func (s *SQLiteStore) ListJobs(ctx context.Context, from, to time.Time, hostID string) ([]schema.Job, error) {
	db, err := s.jobsDatabase()
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `SELECT id,job_name,host_id,started_at,finished_at,duration_seconds,status,exit_code,signal,stats_json,schema FROM jobs WHERE host_id=? AND finished_at BETWEEN ? AND ? ORDER BY finished_at DESC`, hostID, from.UnixMilli(), to.UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []schema.Job
	for rows.Next() {
		var j schema.Job
		var started, finished int64
		var stats string
		if err := rows.Scan(&j.ID, &j.JobName, &j.HostID, &started, &finished, &j.DurationSecond, &j.Status, &j.ExitCode, &j.Signal, &stats, &j.Schema); err != nil {
			return nil, err
		}
		j.StartedAt = time.UnixMilli(started).UTC()
		j.FinishedAt = time.UnixMilli(finished).UTC()
		if stats != "" && stats != "null" {
			if err := json.Unmarshal([]byte(stats), &j.Stats); err != nil {
				return nil, err
			}
		}
		result = append(result, j)
	}
	return result, rows.Err()
}
