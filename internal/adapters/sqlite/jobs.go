package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/qingbo1011/memxplore/internal/application"
	"github.com/qingbo1011/memxplore/internal/domain"
)

// EnqueueObservation captures an immutable observation and its formation job in one transaction.
// The durable job identity is inserted first so idempotent replays never duplicate observations.
func (s *Store) EnqueueObservation(ctx context.Context, observation domain.Observation, job application.Job) (application.Job, bool, error) {
	if err := observation.Validate(); err != nil {
		return application.Job{}, false, fmt.Errorf("validate observation: %w", err)
	}
	if err := job.ValidateEnqueue(); err != nil {
		return application.Job{}, false, fmt.Errorf("validate job: %w", err)
	}
	if job.Namespace != observation.Scope.Namespace {
		return application.Job{}, false, fmt.Errorf("observation and job namespaces differ")
	}
	now := time.Now().UTC()
	if !job.AvailableAt.IsZero() {
		now = job.AvailableAt.UTC()
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return application.Job{}, false, fmt.Errorf("begin observation enqueue: %w", err)
	}
	defer tx.Rollback()
	var insertedID string
	err = tx.QueryRowContext(ctx, `
        INSERT INTO durable_jobs(
            id, namespace_id, kind, state, idempotency_key, payload_json, attempts,
            available_at, created_at, updated_at
        ) VALUES(?, ?, ?, 'queued', ?, ?, 0, ?, ?, ?)
        ON CONFLICT(namespace_id, kind, idempotency_key) DO NOTHING
        RETURNING id`, job.ID, job.Namespace, job.Kind, job.IdempotencyKey, string(job.Payload),
		formatTime(now), formatTime(now), formatTime(now)).Scan(&insertedID)
	if errors.Is(err, sql.ErrNoRows) {
		_ = tx.Rollback()
		existing, getErr := s.getByIdempotency(ctx, job.Namespace, job.Kind, job.IdempotencyKey)
		if getErr != nil {
			return application.Job{}, false, fmt.Errorf("load idempotent job: %w", getErr)
		}
		return existing, false, nil
	}
	if err != nil {
		return application.Job{}, false, fmt.Errorf("insert durable job: %w", err)
	}
	if err := insertObservation(ctx, tx, observation, now); err != nil {
		return application.Job{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return application.Job{}, false, fmt.Errorf("commit observation enqueue: %w", err)
	}
	created, err := s.Get(ctx, domain.ID(insertedID))
	return created, true, err
}

// Claim leases the oldest ready job, including a job abandoned by a crashed worker.
func (s *Store) Claim(ctx context.Context, worker string, now time.Time, lease time.Duration) (application.Job, error) {
	if worker == "" || now.IsZero() || lease <= 0 {
		return application.Job{}, fmt.Errorf("worker, current time, and positive lease are required")
	}
	row := s.db.QueryRowContext(ctx, `
        UPDATE durable_jobs
        SET state = 'running', attempts = attempts + 1, lease_owner = ?, lease_expires_at = ?, updated_at = ?
        WHERE id = (
            SELECT id FROM durable_jobs
            WHERE (state = 'queued' AND available_at <= ?)
               OR (state = 'running' AND lease_expires_at IS NOT NULL AND lease_expires_at <= ?)
            ORDER BY available_at, created_at, id
            LIMIT 1
        )
        RETURNING id, namespace_id, kind, state, idempotency_key, payload_json, result_json,
                  error_code, error_message, attempts, lease_owner, lease_expires_at,
                  available_at, created_at, updated_at`,
		worker, formatTime(now.Add(lease)), formatTime(now), formatTime(now), formatTime(now))
	job, err := scanJob(row)
	if errors.Is(err, sql.ErrNoRows) {
		return application.Job{}, application.ErrNoJob
	}
	if err != nil {
		return application.Job{}, fmt.Errorf("claim durable job: %w", err)
	}
	return job, nil
}

// Complete commits a result only for the current unexpired lease owner.
func (s *Store) Complete(ctx context.Context, id domain.ID, worker string, result json.RawMessage, now time.Time) error {
	if id == "" || worker == "" || now.IsZero() || !json.Valid(result) {
		return fmt.Errorf("job id, worker, current time, and JSON result are required")
	}
	update, err := s.db.ExecContext(ctx, `
        UPDATE durable_jobs
        SET state = 'succeeded', result_json = ?, error_code = NULL, error_message = NULL,
            lease_owner = NULL, lease_expires_at = NULL, updated_at = ?
        WHERE id = ? AND state = 'running' AND lease_owner = ? AND lease_expires_at > ?`,
		string(result), formatTime(now), id, worker, formatTime(now))
	if err != nil {
		return fmt.Errorf("complete durable job: %w", err)
	}
	return requireLease(update)
}

// Fail releases a retryable job with backoff, or terminally fails it at maxAttempts.
func (s *Store) Fail(ctx context.Context, id domain.ID, worker, code, message string, now time.Time, backoff time.Duration, maxAttempts int) error {
	if id == "" || worker == "" || code == "" || now.IsZero() || backoff < 0 || maxAttempts < 1 {
		return fmt.Errorf("job id, worker, error code, current time, backoff, and max attempts are required")
	}
	update, err := s.db.ExecContext(ctx, `
        UPDATE durable_jobs
        SET state = CASE WHEN attempts >= ? THEN 'failed' ELSE 'queued' END,
            error_code = ?, error_message = ?, available_at = ?,
            lease_owner = NULL, lease_expires_at = NULL, updated_at = ?
        WHERE id = ? AND state = 'running' AND lease_owner = ? AND lease_expires_at > ?`,
		maxAttempts, code, message, formatTime(now.Add(backoff)), formatTime(now), id, worker, formatTime(now))
	if err != nil {
		return fmt.Errorf("fail durable job: %w", err)
	}
	return requireLease(update)
}

func requireLease(result sql.Result) error {
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read affected job rows: %w", err)
	}
	if count != 1 {
		return application.ErrLeaseLost
	}
	return nil
}

// Get reads a durable job by stable ID.
func (s *Store) Get(ctx context.Context, id domain.ID) (application.Job, error) {
	row := s.db.QueryRowContext(ctx, `
        SELECT id, namespace_id, kind, state, idempotency_key, payload_json, result_json,
               error_code, error_message, attempts, lease_owner, lease_expires_at,
               available_at, created_at, updated_at
        FROM durable_jobs WHERE id = ?`, id)
	job, err := scanJob(row)
	if errors.Is(err, sql.ErrNoRows) {
		return application.Job{}, application.ErrJobNotFound
	}
	if err != nil {
		return application.Job{}, fmt.Errorf("get durable job: %w", err)
	}
	return job, nil
}

func (s *Store) getByIdempotency(ctx context.Context, namespace domain.ID, kind, key string) (application.Job, error) {
	row := s.db.QueryRowContext(ctx, `
        SELECT id, namespace_id, kind, state, idempotency_key, payload_json, result_json,
               error_code, error_message, attempts, lease_owner, lease_expires_at,
               available_at, created_at, updated_at
        FROM durable_jobs WHERE namespace_id = ? AND kind = ? AND idempotency_key = ?`, namespace, kind, key)
	return scanJob(row)
}

// Wait polls durable state until terminal or context cancellation.
func (s *Store) Wait(ctx context.Context, id domain.ID, interval time.Duration) (application.Job, error) {
	if interval <= 0 {
		interval = 25 * time.Millisecond
	}
	for {
		job, err := s.Get(ctx, id)
		if err != nil {
			return application.Job{}, err
		}
		switch job.State {
		case application.JobSucceeded, application.JobFailed, application.JobCanceled:
			return job, nil
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return application.Job{}, ctx.Err()
		case <-timer.C:
		}
	}
}

type rowScanner interface {
	Scan(...any) error
}

func scanJob(row rowScanner) (application.Job, error) {
	var job application.Job
	var payload string
	var result, errorCode, errorMessage, leaseOwner, leaseExpires sql.NullString
	var availableAt, createdAt, updatedAt string
	if err := row.Scan(&job.ID, &job.Namespace, &job.Kind, &job.State, &job.IdempotencyKey,
		&payload, &result, &errorCode, &errorMessage, &job.Attempts, &leaseOwner, &leaseExpires,
		&availableAt, &createdAt, &updatedAt); err != nil {
		return application.Job{}, err
	}
	job.Payload = json.RawMessage(payload)
	if result.Valid {
		job.Result = json.RawMessage(result.String)
	}
	job.ErrorCode, job.ErrorMessage, job.LeaseOwner = errorCode.String, errorMessage.String, leaseOwner.String
	var err error
	if job.AvailableAt, err = parseStoredTime(availableAt); err != nil {
		return application.Job{}, err
	}
	if job.CreatedAt, err = parseStoredTime(createdAt); err != nil {
		return application.Job{}, err
	}
	if job.UpdatedAt, err = parseStoredTime(updatedAt); err != nil {
		return application.Job{}, err
	}
	if leaseExpires.Valid {
		parsed, parseErr := parseStoredTime(leaseExpires.String)
		if parseErr != nil {
			return application.Job{}, parseErr
		}
		job.LeaseExpiresAt = &parsed
	}
	return job, nil
}

func parseStoredTime(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse stored time: %w", err)
	}
	return parsed, nil
}
