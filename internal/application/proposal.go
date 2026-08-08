package application

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/qingbo1011/memxplore/internal/domain"
)

// ProposalKind enumerates mutations a model may suggest but never apply itself.
type ProposalKind string

const (
	ProposalCreate      ProposalKind = "create"
	ProposalUpdate      ProposalKind = "update"
	ProposalForget      ProposalKind = "forget"
	ProposalConsolidate ProposalKind = "consolidate"
)

// Proposal is immutable strategy output awaiting policy evaluation.
type Proposal struct {
	ID               domain.ID       `json:"id"`
	Namespace        domain.ID       `json:"namespace"`
	ObservationIDs   []domain.ID     `json:"observation_ids"`
	Kind             ProposalKind    `json:"kind"`
	TargetID         domain.ID       `json:"target_id,omitempty"`
	Payload          json.RawMessage `json:"payload"`
	StrategyID       string          `json:"strategy_id"`
	StrategyHash     string          `json:"strategy_hash"`
	Provider         string          `json:"provider,omitempty"`
	Model            string          `json:"model,omitempty"`
	CreatedAt        time.Time       `json:"created_at"`
	UntrustedContent bool            `json:"untrusted_content"`
}

// Validate rejects unauditable proposals at the application boundary.
func (p Proposal) Validate() error {
	if p.ID == "" || p.Namespace == "" || len(p.ObservationIDs) == 0 || p.StrategyID == "" || len(p.StrategyHash) != 64 || p.CreatedAt.IsZero() {
		return fmt.Errorf("proposal identity, evidence, strategy, hash, and timestamp are required")
	}
	switch p.Kind {
	case ProposalCreate:
		if p.TargetID != "" {
			return fmt.Errorf("create proposal cannot name an existing target")
		}
	case ProposalUpdate, ProposalForget, ProposalConsolidate:
		if p.TargetID == "" {
			return fmt.Errorf("%s proposal requires a target", p.Kind)
		}
	default:
		return fmt.Errorf("proposal kind %q is invalid", p.Kind)
	}
	if !json.Valid(p.Payload) {
		return fmt.Errorf("proposal payload must be valid JSON")
	}
	return nil
}

// PolicyDecision is an auditable authorization result.
type PolicyDecision struct {
	Allow      bool     `json:"allow"`
	ReasonCode string   `json:"reason_code"`
	Warnings   []string `json:"warnings,omitempty"`
}

// ApplyPolicy owns authorization and safety decisions for proposal application.
type ApplyPolicy interface {
	Evaluate(context.Context, domain.Scope, Proposal) (PolicyDecision, error)
}
