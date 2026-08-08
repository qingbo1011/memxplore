package application

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/qingbo1011/memxplore/internal/domain"
)

var (
	// ErrPolicyDenied indicates a valid proposal was rejected by apply policy.
	ErrPolicyDenied = errors.New("proposal denied by apply policy")
	// ErrNoChange indicates that deterministic evolution found no new state.
	ErrNoChange = errors.New("proposal would not change memory")
)

// MemoryCreate is the typed create payload emitted by formation strategies.
type MemoryCreate struct {
	Scope         domain.Scope          `json:"scope"`
	Function      domain.MemoryFunction `json:"function"`
	Taxonomy      domain.Taxonomy       `json:"taxonomy"`
	Payload       domain.MemoryPayload  `json:"payload"`
	Provenance    []domain.EvidenceRef  `json:"provenance"`
	ValidTime     *domain.TimeRange     `json:"valid_time,omitempty"`
	ConflictGroup domain.ID             `json:"conflict_group,omitempty"`
	DerivedFrom   []domain.ID           `json:"derived_from,omitempty"`
}

// EvolutionMode distinguishes replacement, a coexisting conflict, and derived rebuild.
type EvolutionMode string

const (
	EvolutionSupersede EvolutionMode = "supersede"
	EvolutionConflict  EvolutionMode = "conflict"
	EvolutionRebuild   EvolutionMode = "rebuild"
)

// MemoryEvolution is a complete candidate for the next immutable version.
type MemoryEvolution struct {
	Mode          EvolutionMode        `json:"mode"`
	Taxonomy      domain.Taxonomy      `json:"taxonomy"`
	Payload       domain.MemoryPayload `json:"payload"`
	Provenance    []domain.EvidenceRef `json:"provenance"`
	ValidTime     domain.TimeRange     `json:"valid_time"`
	ConflictGroup domain.ID            `json:"conflict_group,omitempty"`
	DerivedFrom   []domain.ID          `json:"derived_from,omitempty"`
}

// AppliedMutation returns stable identities and an immutable operation record.
type AppliedMutation struct {
	Memory    domain.Memory        `json:"memory"`
	Version   domain.MemoryVersion `json:"version"`
	Operation domain.Operation     `json:"operation"`
	Decision  PolicyDecision       `json:"decision"`
}

// LifecycleRepository is the policy-gated persistence port.
type LifecycleRepository interface {
	ApplyProposal(context.Context, Proposal, domain.ID, time.Time) (domain.Memory, domain.MemoryVersion, domain.Operation, error)
}

// LifecycleService is the sole application boundary that applies model or rule proposals.
type LifecycleService struct {
	policy     ApplyPolicy
	repository LifecycleRepository
}

// NewLifecycleService requires an explicit policy and repository.
func NewLifecycleService(policy ApplyPolicy, repository LifecycleRepository) (*LifecycleService, error) {
	if policy == nil || repository == nil {
		return nil, fmt.Errorf("apply policy and lifecycle repository are required")
	}
	return &LifecycleService{policy: policy, repository: repository}, nil
}

// Apply validates, authorizes, and then atomically records a proposal.
func (s *LifecycleService) Apply(ctx context.Context, scope domain.Scope, proposal Proposal, at time.Time) (AppliedMutation, error) {
	if err := scope.Validate(); err != nil {
		return AppliedMutation{}, err
	}
	if err := proposal.Validate(); err != nil {
		return AppliedMutation{}, err
	}
	if proposal.Namespace != scope.Namespace || at.IsZero() {
		return AppliedMutation{}, fmt.Errorf("proposal namespace and apply timestamp must match the authorized scope")
	}
	decision, err := s.policy.Evaluate(ctx, scope, proposal)
	if err != nil {
		return AppliedMutation{}, fmt.Errorf("evaluate apply policy: %w", err)
	}
	if decision.ReasonCode == "" {
		return AppliedMutation{}, fmt.Errorf("apply policy returned no reason code")
	}
	if !decision.Allow {
		return AppliedMutation{Decision: decision}, fmt.Errorf("%w: %s", ErrPolicyDenied, decision.ReasonCode)
	}
	memory, version, operation, err := s.repository.ApplyProposal(ctx, proposal, scope.Actor, at.UTC())
	if err != nil {
		return AppliedMutation{Decision: decision}, fmt.Errorf("apply lifecycle proposal: %w", err)
	}
	if operation.Phase != domain.PhaseApply || operation.ProposalID != proposal.ID || operation.Actor != scope.Actor {
		return AppliedMutation{Decision: decision}, fmt.Errorf("repository returned an invalid apply operation")
	}
	return AppliedMutation{Memory: memory, Version: version, Operation: operation, Decision: decision}, nil
}

// Validate checks a create payload without assigning persistence identities.
func (p MemoryCreate) Validate() error {
	if err := p.Scope.Validate(); err != nil {
		return err
	}
	if err := p.Taxonomy.Validate(); err != nil {
		return err
	}
	if err := p.Payload.Validate(p.Function); err != nil {
		return err
	}
	if p.Function == domain.FunctionWorking && p.Scope.Context != p.Payload.Working.TaskID {
		return fmt.Errorf("working memory scope context must equal task id")
	}
	if len(p.Provenance) == 0 {
		return fmt.Errorf("memory create provenance is required")
	}
	if p.ValidTime != nil {
		if err := p.ValidTime.Validate("memory_create.valid_time"); err != nil {
			return err
		}
	}
	return nil
}

// Validate checks a complete next-version candidate.
func (p MemoryEvolution) Validate(function domain.MemoryFunction) error {
	switch p.Mode {
	case EvolutionSupersede, EvolutionConflict:
	case EvolutionRebuild:
		if len(p.DerivedFrom) == 0 {
			return fmt.Errorf("rebuild evolution requires dependencies")
		}
	default:
		return fmt.Errorf("evolution mode %q is invalid", p.Mode)
	}
	if err := p.Taxonomy.Validate(); err != nil {
		return err
	}
	if err := p.Payload.Validate(function); err != nil {
		return err
	}
	if len(p.Provenance) == 0 {
		return fmt.Errorf("memory evolution provenance is required")
	}
	return p.ValidTime.Validate("memory_evolution.valid_time")
}
