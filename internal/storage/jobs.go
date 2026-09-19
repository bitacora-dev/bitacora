package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/bitacora-dev/bitacora/internal/schema"
)

const jobSchemaVersion = 1

var sqliteJobSchema = []string{
	`CREATE TABLE IF NOT EXISTS jobs (id TEXT PRIMARY KEY, job_name TEXT NOT NULL, host_id TEXT NOT NULL, started_at INTEGER NOT NULL, finished_at INTEGER, duration_seconds REAL, status TEXT NOT NULL, exit_code INTEGER, signal TEXT, stats_json TEXT, peer_host_id TEXT, trigger TEXT, next_expected INTEGER, log_refs_json TEXT, schema INTEGER NOT NULL)`,
	`CREATE INDEX IF NOT EXISTS idx_jobs_host_finished ON jobs(host_id, finished_at)`,
	`CREATE INDEX IF NOT EXISTS idx_jobs_host_status_started ON jobs(host_id, status, started_at DESC)`,
	`CREATE TABLE IF NOT EXISTS job_output (job_id TEXT NOT NULL, sequence INTEGER NOT NULL, ts INTEGER NOT NULL, stream TEXT NOT NULL, message TEXT NOT NULL, PRIMARY KEY (job_id, sequence), FOREIGN KEY(job_id) REFERENCES jobs(id))`,
}

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
	for _, pragma := range []string{"PRAGMA journal_mode=WAL", "PRAGMA synchronous=NORMAL", "PRAGMA busy_timeout=5000"} {
		if _, err := db.Exec(pragma); err != nil {
			db.Close()
			return nil, err
		}
	}
	if err := migrateSQLiteJobs(db); err != nil {
		db.Close()
		return nil, err
	}
	s.jobsDB = db
	return db, nil
}

func migrateSQLiteJobs(db *sql.DB) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS job_schema_migrations (version INTEGER PRIMARY KEY)`); err != nil {
		return err
	}
	var version int
	if err := db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM job_schema_migrations`).Scan(&version); err != nil {
		return err
	}
	if version >= jobSchemaVersion {
		return nil
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	fail := func(err error) error { _ = tx.Rollback(); return err }
	var exists int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='jobs'`).Scan(&exists); err != nil {
		return fail(err)
	}
	if exists != 0 {
		if _, err := tx.Exec(`ALTER TABLE jobs RENAME TO jobs_legacy_v0`); err != nil {
			return fail(err)
		}
	}
	for _, stmt := range sqliteJobSchema {
		if _, err := tx.Exec(stmt); err != nil {
			return fail(err)
		}
	}
	if exists != 0 {
		if _, err := tx.Exec(`INSERT INTO jobs (id,job_name,host_id,started_at,finished_at,duration_seconds,status,exit_code,signal,stats_json,schema) SELECT id,job_name,host_id,started_at,finished_at,duration_seconds,status,exit_code,signal,stats_json,schema FROM jobs_legacy_v0`); err != nil {
			return fail(err)
		}
		if _, err := tx.Exec(`DROP TABLE jobs_legacy_v0`); err != nil {
			return fail(err)
		}
	}
	if _, err := tx.Exec(`INSERT INTO job_schema_migrations(version) VALUES (?)`, jobSchemaVersion); err != nil {
		return fail(err)
	}
	return tx.Commit()
}

func (s *SQLiteStore) InsertJob(ctx context.Context, job schema.Job) error {
	if job.Status == schema.JobRunning {
		return s.CreateJob(ctx, job)
	}
	if err := job.Validate(); err != nil {
		return fmt.Errorf("invalid job: %w", err)
	}
	return s.enqueueWrite(ctx, func(ctx context.Context) error {
		db, err := s.jobsDatabase()
		if err != nil {
			return err
		}
		stats, refs, err := marshalJobFields(job)
		if err != nil {
			return err
		}
		_, err = db.ExecContext(ctx, `INSERT INTO jobs (id,job_name,host_id,started_at,finished_at,duration_seconds,status,exit_code,signal,stats_json,peer_host_id,trigger,next_expected,log_refs_json,schema) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO NOTHING`, job.ID, job.JobName, job.HostID, job.StartedAt.UnixMilli(), job.FinishedAt.UnixMilli(), job.DurationSecond, string(job.Status), job.ExitCode, job.Signal, stats, job.PeerHostID, job.Trigger, nullableMillis(job.NextExpected), refs, job.Schema)
		return err
	})
}

func (s *SQLiteStore) CreateJob(ctx context.Context, job schema.Job) error {
	if job.Status != schema.JobRunning {
		return fmt.Errorf("%w: create requires running", ErrJobTransition)
	}
	if err := job.Validate(); err != nil {
		return fmt.Errorf("invalid job: %w", err)
	}
	return s.enqueueWrite(ctx, func(ctx context.Context) error {
		db, err := s.jobsDatabase()
		if err != nil {
			return err
		}
		_, err = db.ExecContext(ctx, `INSERT INTO jobs (id,job_name,host_id,started_at,status,schema) VALUES (?,?,?,?,?,?) ON CONFLICT(id) DO NOTHING`, job.ID, job.JobName, job.HostID, job.StartedAt.UnixMilli(), string(job.Status), job.Schema)
		return err
	})
}

func (s *SQLiteStore) FinishJob(ctx context.Context, job schema.Job) error {
	if !job.Status.Terminal() {
		return fmt.Errorf("%w: finish requires a terminal status", ErrJobTransition)
	}
	if err := job.Validate(); err != nil {
		return fmt.Errorf("invalid job: %w", err)
	}
	return s.enqueueWrite(ctx, func(ctx context.Context) error {
		db, err := s.jobsDatabase()
		if err != nil {
			return err
		}
		stats, refs, err := marshalJobFields(job)
		if err != nil {
			return err
		}
		res, err := db.ExecContext(ctx, `UPDATE jobs SET finished_at=?,duration_seconds=?,status=?,exit_code=?,signal=?,stats_json=?,peer_host_id=?,trigger=?,next_expected=?,log_refs_json=?,schema=? WHERE id=? AND host_id=? AND status='running'`, job.FinishedAt.UnixMilli(), job.DurationSecond, string(job.Status), job.ExitCode, job.Signal, stats, job.PeerHostID, job.Trigger, nullableMillis(job.NextExpected), refs, job.Schema, job.ID, job.HostID)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n == 1 {
			return nil
		}
		existing, ok, err := getSQLiteJob(ctx, db, job.HostID, job.ID)
		if err != nil {
			return err
		}
		if ok && jobsEqual(existing, job) {
			return nil
		}
		return fmt.Errorf("%w: job %q is not running", ErrJobTransition, job.ID)
	})
}

func (s *SQLiteStore) GetJob(ctx context.Context, hostID, jobID string) (schema.Job, bool, error) {
	db, err := s.jobsDatabase()
	if err != nil {
		return schema.Job{}, false, err
	}
	return getSQLiteJob(ctx, db, hostID, jobID)
}

func (s *SQLiteStore) ListJobs(ctx context.Context, from, to time.Time, hostID string) ([]schema.Job, error) {
	db, err := s.jobsDatabase()
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `SELECT id,job_name,host_id,started_at,finished_at,duration_seconds,status,exit_code,signal,stats_json,peer_host_id,trigger,next_expected,log_refs_json,schema FROM jobs WHERE host_id=? AND finished_at BETWEEN ? AND ? ORDER BY finished_at DESC`, hostID, from.UnixMilli(), to.UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	jobs := []schema.Job{}
	for rows.Next() {
		j, _, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, j)
	}
	return jobs, rows.Err()
}

func (s *SQLiteStore) AppendJobOutput(ctx context.Context, hostID string, line schema.JobOutputLine) error {
	if err := line.Validate(); err != nil {
		return fmt.Errorf("invalid job output: %w", err)
	}
	return s.enqueueWrite(ctx, func(ctx context.Context) error {
		db, err := s.jobsDatabase()
		if err != nil {
			return err
		}
		var status string
		err = db.QueryRowContext(ctx, `SELECT status FROM jobs WHERE id=? AND host_id=?`, line.JobID, hostID).Scan(&status)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: job %q does not exist", ErrJobTransition, line.JobID)
		}
		if err != nil {
			return err
		}
		if status != string(schema.JobRunning) {
			return fmt.Errorf("%w: job %q is terminal", ErrJobTransition, line.JobID)
		}
		_, err = db.ExecContext(ctx, `INSERT INTO job_output (job_id,sequence,ts,stream,message) VALUES (?,?,?,?,?) ON CONFLICT(job_id,sequence) DO NOTHING`, line.JobID, line.Sequence, line.TS.UnixMilli(), line.Stream, line.Message)
		return err
	})
}

func (s *SQLiteStore) ListJobOutput(ctx context.Context, hostID, jobID string, afterSequence int64, limit int) ([]schema.JobOutputLine, int64, error) {
	if afterSequence < 0 || limit < 1 || limit > 500 {
		return nil, 0, fmt.Errorf("invalid output cursor or limit")
	}
	db, err := s.jobsDatabase()
	if err != nil {
		return nil, 0, err
	}
	if _, ok, err := getSQLiteJob(ctx, db, hostID, jobID); err != nil || !ok {
		return nil, afterSequence, err
	}
	rows, err := db.QueryContext(ctx, `SELECT job_id,sequence,ts,stream,message FROM job_output WHERE job_id=? AND sequence>? ORDER BY sequence ASC LIMIT ?`, jobID, afterSequence, limit)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	lines := []schema.JobOutputLine{}
	next := afterSequence
	for rows.Next() {
		var line schema.JobOutputLine
		var millis int64
		if err := rows.Scan(&line.JobID, &line.Sequence, &millis, &line.Stream, &line.Message); err != nil {
			return nil, 0, err
		}
		line.TS = time.UnixMilli(millis).UTC()
		lines = append(lines, line)
		next = line.Sequence
	}
	return lines, next, rows.Err()
}

func marshalJobFields(job schema.Job) (string, string, error) {
	stats, err := json.Marshal(job.Stats)
	if err != nil {
		return "", "", err
	}
	refs, err := json.Marshal(job.LogRefs)
	if err != nil {
		return "", "", err
	}
	return string(stats), string(refs), nil
}

func nullableMillis(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.UnixMilli()
}

func getSQLiteJob(ctx context.Context, db *sql.DB, hostID, jobID string) (schema.Job, bool, error) {
	row := db.QueryRowContext(ctx, `SELECT id,job_name,host_id,started_at,finished_at,duration_seconds,status,exit_code,signal,stats_json,peer_host_id,trigger,next_expected,log_refs_json,schema FROM jobs WHERE id=? AND host_id=?`, jobID, hostID)
	return scanJob(row)
}

type jobRow interface{ Scan(...any) error }

func scanJob(row jobRow) (schema.Job, bool, error) {
	var j schema.Job
	var started int64
	var finished, next sql.NullInt64
	var duration sql.NullFloat64
	var exit sql.NullInt64
	var signal, stats, peer, trigger, refs sql.NullString
	err := row.Scan(&j.ID, &j.JobName, &j.HostID, &started, &finished, &duration, &j.Status, &exit, &signal, &stats, &peer, &trigger, &next, &refs, &j.Schema)
	if errors.Is(err, sql.ErrNoRows) {
		return schema.Job{}, false, nil
	}
	if err != nil {
		return schema.Job{}, false, err
	}
	j.StartedAt = time.UnixMilli(started).UTC()
	if finished.Valid {
		j.FinishedAt = time.UnixMilli(finished.Int64).UTC()
	}
	if duration.Valid {
		j.DurationSecond = duration.Float64
	}
	if exit.Valid {
		j.ExitCode = int(exit.Int64)
	}
	if signal.Valid {
		j.Signal = signal.String
	}
	if peer.Valid {
		j.PeerHostID = peer.String
	}
	if trigger.Valid {
		j.Trigger = trigger.String
	}
	if next.Valid {
		j.NextExpected = time.UnixMilli(next.Int64).UTC()
	}
	if stats.Valid && stats.String != "" && stats.String != "null" {
		if err := json.Unmarshal([]byte(stats.String), &j.Stats); err != nil {
			return schema.Job{}, false, err
		}
	}
	if refs.Valid && refs.String != "" && refs.String != "null" {
		if err := json.Unmarshal([]byte(refs.String), &j.LogRefs); err != nil {
			return schema.Job{}, false, err
		}
	}
	return j, true, nil
}

func jobsEqual(a, b schema.Job) bool {
	return a.ID == b.ID && a.HostID == b.HostID && a.Status == b.Status && a.FinishedAt.Equal(b.FinishedAt) && a.DurationSecond == b.DurationSecond && a.ExitCode == b.ExitCode && a.Signal == b.Signal
}
