package domain

import (
	"fmt"
	"time"
)

// OperationPhase separates model observation/proposal from policy-controlled application.
type OperationPhase string

const (
	PhaseObserve OperationPhase = "observe"
	PhasePropose OperationPhase = "propose"
	PhaseApply   OperationPhase = "apply"
)

// Operation records a lifecycle mutation without embedding deleted content.
type Operation struct {
	ID         ID             `json:"id"`
	Phase      OperationPhase `json:"phase"`
	Kind       string         `json:"kind"`
	Actor      ID             `json:"actor"`
	TargetID   ID             `json:"target_id"`
	ProposalID ID             `json:"proposal_id,omitempty"`
	StrategyID string         `json:"strategy_id,omitempty"`
	OccurredAt time.Time      `json:"occurred_at"`
	Result     string         `json:"result"`
}

// ScoreExplanation preserves independent retrieval signals.
type ScoreExplanation struct {
	Lexical  *float64 `json:"lexical,omitempty"`
	Semantic *float64 `json:"semantic,omitempty"`
	RRF      *float64 `json:"rrf,omitempty"`
	Trust    float64  `json:"trust"`
	Total    float64  `json:"total"`
}

// RetrievalCandidate is one auditable candidate in a retrieval trace.
type RetrievalCandidate struct {
	MemoryID        ID               `json:"memory_id"`
	VersionID       ID               `json:"version_id"`
	ConflictGroup   ID               `json:"conflict_group,omitempty"`
	Selected        bool             `json:"selected"`
	DuplicateOf     ID               `json:"duplicate_of,omitempty"`
	EstimatedTokens int              `json:"estimated_tokens"`
	Score           ScoreExplanation `json:"score"`
}

// RetrievalTrace records strategy inputs, candidates, budget, and fallback behavior.
type RetrievalTrace struct {
	ID             ID                   `json:"id"`
	Scope          Scope                `json:"scope"`
	Query          string               `json:"query"`
	StrategyID     string               `json:"strategy_id"`
	FallbackReason string               `json:"fallback_reason,omitempty"`
	ValidAt        time.Time            `json:"valid_at"`
	SystemAt       time.Time            `json:"system_at"`
	TokenBudget    int                  `json:"token_budget"`
	TokensUsed     int                  `json:"tokens_used"`
	Candidates     []RetrievalCandidate `json:"candidates"`
	StartedAt      time.Time            `json:"started_at"`
	CompletedAt    time.Time            `json:"completed_at"`
}

// Validate checks the trace without interpreting adapter-specific scores.
func (t RetrievalTrace) Validate() error {
	if err := validateID("retrieval_trace.id", t.ID, true); err != nil {
		return err
	}
	if err := t.Scope.Validate(); err != nil {
		return err
	}
	if err := validateRequiredText("retrieval_trace.query", t.Query, 1<<20); err != nil {
		return err
	}
	if err := validateRequiredText("retrieval_trace.strategy_id", t.StrategyID, 255); err != nil {
		return err
	}
	if t.ValidAt.IsZero() || t.SystemAt.IsZero() {
		return fmt.Errorf("retrieval trace requires valid_at and system_at")
	}
	if t.TokenBudget < 0 || t.TokensUsed < 0 || t.TokensUsed > t.TokenBudget {
		return fmt.Errorf("retrieval trace token budget is invalid")
	}
	if t.StartedAt.IsZero() || t.CompletedAt.Before(t.StartedAt) {
		return fmt.Errorf("retrieval trace timestamps are invalid")
	}
	return nil
}
