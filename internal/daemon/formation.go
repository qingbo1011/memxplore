// Package daemon owns durable workers and long-running application orchestration.
package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/qingbo1011/memxplore/internal/adapters/sqlite"
	"github.com/qingbo1011/memxplore/internal/application"
	"github.com/qingbo1011/memxplore/internal/domain"
	"github.com/qingbo1011/memxplore/internal/observability"
	"github.com/qingbo1011/memxplore/internal/policy"
	"github.com/qingbo1011/memxplore/internal/strategy/formation"
)

// FormationConfig binds only explicitly configured local/provider capabilities.
type FormationConfig struct {
	Store               *sqlite.Store
	Generator           application.Generator
	GeneratorProvider   string
	GeneratorModel      string
	Embedder            application.EmbeddingProvider
	EmbeddingProvider   string
	EmbeddingModel      string
	EmbeddingDimensions int
	Lease               time.Duration
	PollInterval        time.Duration
	Now                 func() time.Time
	Observability       observability.Recorder
}

// FormationWorker applies durable observation jobs and optionally embeds results.
type FormationWorker struct {
	config FormationConfig
	wake   chan struct{}
}

// SupportsAssisted reports whether generator-assisted formation was explicitly configured.
func (w *FormationWorker) SupportsAssisted() bool { return w.config.Generator != nil }

// NewFormationWorker validates durable worker dependencies.
func NewFormationWorker(config FormationConfig) (*FormationWorker, error) {
	if config.Store == nil {
		return nil, fmt.Errorf("formation worker store is required")
	}
	if config.Generator != nil && (config.GeneratorProvider == "" || config.GeneratorModel == "") {
		return nil, fmt.Errorf("generator identity is required")
	}
	if config.Embedder != nil && (config.EmbeddingProvider == "" || config.EmbeddingModel == "" || config.EmbeddingDimensions < 1) {
		return nil, fmt.Errorf("embedding identity is required")
	}
	if config.Lease <= 0 {
		config.Lease = 15 * time.Minute
	}
	if config.PollInterval <= 0 {
		config.PollInterval = 100 * time.Millisecond
	}
	if config.Now == nil {
		config.Now = func() time.Time { return time.Now().UTC() }
	}
	config.Observability = observability.OrNop(config.Observability)
	return &FormationWorker{config: config, wake: make(chan struct{}, 1)}, nil
}

// Notify wakes a polling worker after enqueue.
func (w *FormationWorker) Notify() {
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

// Run processes jobs until context cancellation.
func (w *FormationWorker) Run(ctx context.Context) error {
	for {
		for {
			err := w.ProcessOne(ctx, "formation-worker")
			if errors.Is(err, application.ErrNoJob) {
				break
			}
			if err != nil && ctx.Err() != nil {
				return ctx.Err()
			}
		}
		timer := time.NewTimer(w.config.PollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-w.wake:
			timer.Stop()
		case <-timer.C:
		}
	}
}

// ProcessOne claims and resolves one job. Failures are durably retried up to three attempts.
func (w *FormationWorker) ProcessOne(ctx context.Context, workerID string) (finalErr error) {
	now := w.config.Now().UTC()
	job, err := w.config.Store.Claim(ctx, workerID, now, w.config.Lease)
	if err != nil {
		return err
	}
	ctx, endOperation := w.config.Observability.Start(ctx, "memory.formation", observability.String("job_kind", job.Kind))
	defer func() { endOperation(finalErr) }()
	if err := w.processClaimed(ctx, job); err != nil {
		message := err.Error()
		if len(message) > 2048 {
			message = message[:2048]
		}
		failErr := w.config.Store.Fail(ctx, job.ID, workerID, "formation_failed", message,
			w.config.Now().UTC(), time.Second, 3)
		if failErr != nil {
			return fmt.Errorf("formation failed: %v; persist failure: %w", err, failErr)
		}
		return fmt.Errorf("formation job %s: %w", job.ID, err)
	}
	return nil
}

func (w *FormationWorker) processClaimed(ctx context.Context, job application.Job) error {
	var payload application.FormationJobPayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return fmt.Errorf("decode formation job: %w", err)
	}
	observation, err := w.config.Store.GetObservation(ctx, payload.ObservationID)
	if err != nil {
		return err
	}
	if observation.Scope != payload.ApplyScope || observation.Scope.Namespace != job.Namespace {
		return fmt.Errorf("formation job scope does not match observation")
	}
	var strategy *formation.Strategy
	if payload.Mode == "generator-free" {
		strategy, err = formation.NewGeneratorFree(payload.Function)
	} else if payload.Mode == "assisted" {
		strategy, err = formation.NewAssisted(payload.Function, w.config.Generator, w.config.GeneratorProvider, w.config.GeneratorModel)
	} else {
		err = application.ErrInvalidFormationJob
	}
	if err != nil {
		return err
	}
	proposal, err := strategy.Propose(ctx, observation)
	if err != nil {
		return err
	}
	if err := w.ensureFunctionalReferences(ctx, proposal, payload, observation); err != nil {
		return err
	}
	service, _ := application.NewLifecycleService(policy.OwnerPolicy{}, w.config.Store)
	applied, err := service.Apply(ctx, payload.ApplyScope, proposal, w.config.Now().UTC())
	if err != nil {
		return err
	}
	if w.config.Embedder != nil {
		text := application.MemoryText(applied.Version.Payload)
		embedding, err := w.config.Embedder.Embed(ctx, application.EmbeddingRequest{
			Model: w.config.EmbeddingModel, Input: []string{text}, Dimensions: w.config.EmbeddingDimensions,
		})
		if err != nil {
			return err
		}
		if len(embedding.Vectors) != 1 {
			return fmt.Errorf("embedding provider returned %d vectors", len(embedding.Vectors))
		}
		if err := w.config.Store.PutEmbedding(ctx, applied.Version.ID, w.config.EmbeddingProvider,
			w.config.EmbeddingModel, text, embedding.Vectors[0], w.config.Now().UTC()); err != nil {
			return err
		}
	}
	result, _ := json.Marshal(application.FormationJobResult{
		MemoryID: applied.Memory.ID, VersionID: applied.Version.ID, OperationID: applied.Operation.ID, ProposalID: proposal.ID,
	})
	return w.config.Store.Complete(ctx, job.ID, job.LeaseOwner, result, w.config.Now().UTC())
}

func (w *FormationWorker) ensureFunctionalReferences(ctx context.Context, proposal application.Proposal, job application.FormationJobPayload, observation domain.Observation) error {
	var create application.MemoryCreate
	if err := json.Unmarshal(proposal.Payload, &create); err != nil {
		return err
	}
	if create.Payload.Experiential != nil {
		for _, evidence := range create.Payload.Experiential.Evidence {
			exists, err := w.config.Store.EpisodeExists(ctx, evidence.EpisodeID)
			if err != nil {
				return err
			}
			if exists {
				continue
			}
			episode := domain.Episode{
				ID: evidence.EpisodeID, Scope: observation.Scope, Task: observation.Content,
				ObservationIDs: []domain.ID{observation.ID},
				StartedAt:      observation.CapturedAt.Add(-time.Nanosecond), EndedAt: observation.CapturedAt,
			}
			outcomes := make([]domain.Outcome, len(evidence.OutcomeIDs))
			for index, outcomeID := range evidence.OutcomeIDs {
				outcomes[index] = domain.Outcome{
					ID: outcomeID, EpisodeID: episode.ID, Source: observation.Scope.Actor,
					Kind: "observation", Value: 0, Evidence: observation.Content, ObservedAt: observation.CapturedAt,
				}
			}
			if err := w.config.Store.PutEpisode(ctx, episode, outcomes); err != nil {
				return err
			}
		}
	}
	if create.Payload.Working != nil {
		working := create.Payload.Working
		exists, err := w.config.Store.WorkingSetExists(ctx, working.WorkingSetID)
		if err != nil {
			return err
		}
		if !exists {
			set := domain.WorkingSet{
				ID: working.WorkingSetID, Scope: observation.Scope, TaskID: working.TaskID,
				Goal: working.Goal, GlobalRecall: job.WorkingGlobalRecall, ExpiresAt: job.WorkingExpiresAt,
				CreatedAt: claimedAtOr(observation.CapturedAt, w.config.Now()), UpdatedAt: w.config.Now().UTC(),
			}
			if err := w.config.Store.PutWorkingSet(ctx, set); err != nil {
				return err
			}
		}
	}
	return nil
}

func claimedAtOr(captured, fallback time.Time) time.Time {
	if !captured.IsZero() {
		return captured.UTC()
	}
	return fallback.UTC()
}
