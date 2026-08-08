package domain

import (
	"encoding/hex"
	"fmt"
	"math"
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

// RetrievalAuthorization records the pre-content access decision used for one trace.
type RetrievalAuthorization struct {
	PrincipalID   ID   `json:"principal_id"`
	PrivateOwners []ID `json:"private_owners"`
	AllowShared   bool `json:"allow_shared"`
	AllowPublic   bool `json:"allow_public"`
}

// RetrievalTrace records strategy inputs, candidates, budget, and fallback behavior.
type RetrievalTrace struct {
	ID                   ID                     `json:"id"`
	Scope                Scope                  `json:"scope"`
	Query                string                 `json:"query"`
	StrategyID           string                 `json:"strategy_id"`
	StrategyHash         string                 `json:"strategy_hash"`
	FallbackReason       string                 `json:"fallback_reason,omitempty"`
	Authorization        RetrievalAuthorization `json:"authorization"`
	Functions            []MemoryFunction       `json:"functions,omitempty"`
	IncludeGlobalWorking bool                   `json:"include_global_working"`
	ValidAt              time.Time              `json:"valid_at"`
	SystemAt             time.Time              `json:"system_at"`
	TokenBudget          int                    `json:"token_budget"`
	TokensUsed           int                    `json:"tokens_used"`
	Candidates           []RetrievalCandidate   `json:"candidates"`
	StartedAt            time.Time              `json:"started_at"`
	CompletedAt          time.Time              `json:"completed_at"`
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
	strategyDigest, err := hex.DecodeString(t.StrategyHash)
	if err != nil || len(strategyDigest) != 32 {
		return fmt.Errorf("retrieval trace strategy_hash must be a SHA-256 hex digest")
	}
	if err := validateID("retrieval_trace.authorization.principal_id", t.Authorization.PrincipalID, true); err != nil {
		return err
	}
	if len(t.Authorization.PrivateOwners) == 0 {
		return fmt.Errorf("retrieval trace authorization requires private owners")
	}
	seenOwners := make(map[ID]struct{}, len(t.Authorization.PrivateOwners))
	for _, owner := range t.Authorization.PrivateOwners {
		if err := validateID("retrieval_trace.authorization.private_owner", owner, true); err != nil {
			return err
		}
		if _, duplicate := seenOwners[owner]; duplicate {
			return fmt.Errorf("retrieval trace authorization contains duplicate owner %s", owner)
		}
		seenOwners[owner] = struct{}{}
	}
	seenFunctions := make(map[MemoryFunction]struct{}, len(t.Functions))
	for _, function := range t.Functions {
		switch function {
		case FunctionFactual, FunctionExperiential, FunctionWorking:
		default:
			return fmt.Errorf("retrieval trace function %q is invalid", function)
		}
		if _, duplicate := seenFunctions[function]; duplicate {
			return fmt.Errorf("retrieval trace contains duplicate function %q", function)
		}
		seenFunctions[function] = struct{}{}
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
	selectedTokens := 0
	for index, candidate := range t.Candidates {
		if err := validateID("retrieval_candidate.memory_id", candidate.MemoryID, true); err != nil {
			return fmt.Errorf("candidate %d: %w", index, err)
		}
		if err := validateID("retrieval_candidate.version_id", candidate.VersionID, true); err != nil {
			return fmt.Errorf("candidate %d: %w", index, err)
		}
		if err := validateID("retrieval_candidate.conflict_group", candidate.ConflictGroup, false); err != nil {
			return fmt.Errorf("candidate %d: %w", index, err)
		}
		if err := validateID("retrieval_candidate.duplicate_of", candidate.DuplicateOf, false); err != nil {
			return fmt.Errorf("candidate %d: %w", index, err)
		}
		if candidate.EstimatedTokens < 0 || (candidate.Selected && candidate.DuplicateOf != "") {
			return fmt.Errorf("candidate %d has invalid selection or token estimate", index)
		}
		if err := candidate.Score.validate(); err != nil {
			return fmt.Errorf("candidate %d score: %w", index, err)
		}
		if candidate.Selected {
			selectedTokens += candidate.EstimatedTokens
		}
	}
	if selectedTokens != t.TokensUsed {
		return fmt.Errorf("retrieval trace selected token sum %d does not equal tokens_used %d", selectedTokens, t.TokensUsed)
	}
	return nil
}

func (s ScoreExplanation) validate() error {
	for name, value := range map[string]*float64{"lexical": s.Lexical, "semantic": s.Semantic, "rrf": s.RRF} {
		if value != nil && (math.IsNaN(*value) || math.IsInf(*value, 0)) {
			return fmt.Errorf("%s score is not finite", name)
		}
	}
	if math.IsNaN(s.Trust) || math.IsInf(s.Trust, 0) || s.Trust < 0 || s.Trust > 1 {
		return fmt.Errorf("trust score must be finite and within [0,1]")
	}
	if math.IsNaN(s.Total) || math.IsInf(s.Total, 0) {
		return fmt.Errorf("total score is not finite")
	}
	return nil
}
