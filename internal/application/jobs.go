package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/qingbo1011/memxplore/internal/domain"
)

var (
	// ErrNoJob indicates that no job is currently claimable.
	ErrNoJob = errors.New("no claimable job")
	// ErrJobNotFound indicates that a referenced job does not exist.
	ErrJobNotFound = errors.New("job not found")
	// ErrLeaseLost indicates a stale or foreign worker tried to finish a job.
	ErrLeaseLost = errors.New("job lease lost")
)

// JobState is the durable state machine persisted across daemon restarts.
type JobState string

const (
	JobQueued    JobState = "queued"
	JobRunning   JobState = "running"
	JobSucceeded JobState = "succeeded"
	JobFailed    JobState = "failed"
	JobCanceled  JobState = "canceled"
)

// Job contains no provider-specific fields.
type Job struct {
	ID             domain.ID       `json:"id"`
	Namespace      domain.ID       `json:"namespace"`
	Kind           string          `json:"kind"`
	State          JobState        `json:"state"`
	IdempotencyKey string          `json:"idempotency_key"`
	Payload        json.RawMessage `json:"payload"`
	Result         json.RawMessage `json:"result,omitempty"`
	ErrorCode      string          `json:"error_code,omitempty"`
	ErrorMessage   string          `json:"error_message,omitempty"`
	Attempts       int             `json:"attempts"`
	LeaseOwner     string          `json:"lease_owner,omitempty"`
	LeaseExpiresAt *time.Time      `json:"lease_expires_at,omitempty"`
	AvailableAt    time.Time       `json:"available_at"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
}

// ValidateEnqueue checks caller-owned job data before observation capture.
func (j Job) ValidateEnqueue() error {
	if j.ID == "" || j.Namespace == "" || j.Kind == "" || j.IdempotencyKey == "" || !json.Valid(j.Payload) {
		return fmt.Errorf("job id, namespace, kind, idempotency key, and JSON payload are required")
	}
	return nil
}

// JobQueue captures observations and formation work atomically.
type JobQueue interface {
	EnqueueObservation(context.Context, domain.Observation, Job) (Job, bool, error)
	Claim(context.Context, string, time.Time, time.Duration) (Job, error)
	Complete(context.Context, domain.ID, string, json.RawMessage, time.Time) error
	Fail(context.Context, domain.ID, string, string, string, time.Time, time.Duration, int) error
	Get(context.Context, domain.ID) (Job, error)
	Wait(context.Context, domain.ID, time.Duration) (Job, error)
}
