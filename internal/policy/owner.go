// Package policy implements explicit application safety policies.
package policy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/qingbo1011/memxplore/internal/application"
	"github.com/qingbo1011/memxplore/internal/domain"
)

// OwnerPolicy authorizes namespace/owner-bound lifecycle changes and blocks model deletion.
type OwnerPolicy struct{}

// Evaluate returns a stable reason code for every decision.
func (OwnerPolicy) Evaluate(_ context.Context, scope domain.Scope, proposal application.Proposal) (application.PolicyDecision, error) {
	if proposal.Namespace != scope.Namespace {
		return application.PolicyDecision{ReasonCode: "namespace_mismatch"}, nil
	}
	switch proposal.Kind {
	case application.ProposalCreate:
		var create application.MemoryCreate
		decoder := json.NewDecoder(bytes.NewReader(proposal.Payload))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&create); err != nil {
			return application.PolicyDecision{}, fmt.Errorf("decode create policy payload: %w", err)
		}
		if err := decoder.Decode(&struct{}{}); err != io.EOF {
			return application.PolicyDecision{}, fmt.Errorf("create policy payload contains trailing JSON")
		}
		if create.Scope.Namespace != scope.Namespace || create.Scope.Owner != scope.Owner || create.Scope.Subject != scope.Subject || create.Scope.Actor != scope.Actor {
			return application.PolicyDecision{ReasonCode: "scope_mismatch"}, nil
		}
		return application.PolicyDecision{Allow: true, ReasonCode: "owner_create"}, nil
	case application.ProposalUpdate, application.ProposalConsolidate:
		return application.PolicyDecision{Allow: true, ReasonCode: "owner_evolve"}, nil
	case application.ProposalArchive, application.ProposalForget:
		if proposal.Provider != "" || proposal.UntrustedContent {
			return application.PolicyDecision{ReasonCode: "model_destructive_change_denied"}, nil
		}
		return application.PolicyDecision{Allow: true, ReasonCode: "owner_state_change"}, nil
	default:
		return application.PolicyDecision{ReasonCode: "unsupported_operation"}, nil
	}
}

var _ application.ApplyPolicy = OwnerPolicy{}
