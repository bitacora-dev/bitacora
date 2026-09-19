package storage

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/bitacora-dev/bitacora/internal/schema"
)

func (s *PostgresStore) InsertJob(ctx context.Context, job schema.Job) error {
	if job.Status == schema.JobRunning {
		return s.CreateJob(ctx, job)
	}
	if err := job.Validate(); err != nil {
		return fmt.Errorf("invalid job: %w", err)
	}
	stats, refs, err := marshalJobFields(job)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO jobs (id,job_name,host_id,started_at,finished_at,duration_seconds,status,exit_code,signal,stats_json,peer_host_id,trigger,next_expected,log_refs_json,schema) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15) ON CONFLICT (id) DO NOTHING`, job.ID, job.JobName, job.HostID, job.StartedAt.UnixMilli(), job.FinishedAt.UnixMilli(), job.DurationSecond, string(job.Status), job.ExitCode, job.Signal, stats, job.PeerHostID, job.Trigger, nullableMillis(job.NextExpected), refs, job.Schema)
	return err
}

func (s *PostgresStore) CreateJob(ctx context.Context, job schema.Job) error {
	if job.Status != schema.JobRunning {
		return fmt.Errorf("%w: create requires running", ErrJobTransition)
	}
	if err := job.Validate(); err != nil {
		return fmt.Errorf("invalid job: %w", err)
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO jobs (id,job_name,host_id,started_at,status,schema) VALUES ($1,$2,$3,$4,$5,$6) ON CONFLICT(id) DO NOTHING`, job.ID, job.JobName, job.HostID, job.StartedAt.UnixMilli(), string(job.Status), job.Schema)
	return err
}

func (s *PostgresStore) FinishJob(ctx context.Context, job schema.Job) error {
	if !job.Status.Terminal() {
		return fmt.Errorf("%w: finish requires terminal status", ErrJobTransition)
	}
	if err := job.Validate(); err != nil {
		return fmt.Errorf("invalid job: %w", err)
	}
	stats, refs, err := marshalJobFields(job)
	if err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, `UPDATE jobs SET finished_at=$1,duration_seconds=$2,status=$3,exit_code=$4,signal=$5,stats_json=$6,peer_host_id=$7,trigger=$8,next_expected=$9,log_refs_json=$10,schema=$11 WHERE id=$12 AND host_id=$13 AND status='running'`, job.FinishedAt.UnixMilli(), job.DurationSecond, string(job.Status), job.ExitCode, job.Signal, stats, job.PeerHostID, job.Trigger, nullableMillis(job.NextExpected), refs, job.Schema, job.ID, job.HostID)
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
	existing, ok, err := s.GetJob(ctx, job.HostID, job.ID)
	if err != nil {
		return err
	}
	if ok && jobsEqual(existing, job) {
		return nil
	}
	return fmt.Errorf("%w: job %q is not running", ErrJobTransition, job.ID)
}

func (s *PostgresStore) GetJob(ctx context.Context, hostID, jobID string) (schema.Job, bool, error) {
	return scanJob(s.db.QueryRowContext(ctx, `SELECT id,job_name,host_id,started_at,finished_at,duration_seconds,status,exit_code,signal,stats_json::text,peer_host_id,trigger,next_expected,log_refs_json::text,schema FROM jobs WHERE id=$1 AND host_id=$2`, jobID, hostID))
}

func (s *PostgresStore) AppendJobOutput(ctx context.Context, hostID string, line schema.JobOutputLine) error {
	if err := line.Validate(); err != nil {
		return fmt.Errorf("invalid job output: %w", err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var status string
	err = tx.QueryRowContext(ctx, `SELECT status FROM jobs WHERE id=$1 AND host_id=$2 FOR UPDATE`, line.JobID, hostID).Scan(&status)
	if err == sql.ErrNoRows {
		return fmt.Errorf("%w: job %q does not exist", ErrJobTransition, line.JobID)
	}
	if err != nil {
		return err
	}
	if status != string(schema.JobRunning) {
		return fmt.Errorf("%w: job %q is terminal", ErrJobTransition, line.JobID)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO job_output (job_id,sequence,ts,stream,message) VALUES ($1,$2,$3,$4,$5) ON CONFLICT(job_id,sequence) DO NOTHING`, line.JobID, line.Sequence, line.TS.UnixMilli(), line.Stream, line.Message); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *PostgresStore) ListJobOutput(ctx context.Context, hostID, jobID string, afterSequence int64, limit int) ([]schema.JobOutputLine, int64, error) {
	if afterSequence < 0 || limit < 1 || limit > 500 {
		return nil, 0, fmt.Errorf("invalid output cursor or limit")
	}
	if _, ok, err := s.GetJob(ctx, hostID, jobID); err != nil || !ok {
		return nil, afterSequence, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT job_id,sequence,ts,stream,message FROM job_output WHERE job_id=$1 AND sequence>$2 ORDER BY sequence ASC LIMIT $3`, jobID, afterSequence, limit)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	lines := []schema.JobOutputLine{}
	next := afterSequence
	for rows.Next() {
		var line schema.JobOutputLine
		var ms int64
		if err := rows.Scan(&line.JobID, &line.Sequence, &ms, &line.Stream, &line.Message); err != nil {
			return nil, 0, err
		}
		line.TS = time.UnixMilli(ms).UTC()
		lines = append(lines, line)
		next = line.Sequence
	}
	return lines, next, rows.Err()
}

func (s *PostgresStore) ListJobs(ctx context.Context, from, to time.Time, hostID string) ([]schema.Job, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,job_name,host_id,started_at,finished_at,duration_seconds,status,exit_code,signal,stats_json::text,peer_host_id,trigger,next_expected,log_refs_json::text,schema FROM jobs WHERE host_id=$1 AND finished_at BETWEEN $2 AND $3 ORDER BY finished_at DESC`, hostID, from.UnixMilli(), to.UnixMilli())
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
