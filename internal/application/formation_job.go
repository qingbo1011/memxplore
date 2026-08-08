package application

import (
	"encoding/json"
	"time"

	"github.com/qingbo1011/memxplore/internal/domain"
)

// FormationJobPayload is the durable, provider-neutral formation work contract.
type FormationJobPayload struct {
	ObservationID       domain.ID             `json:"observation_id"`
	Function            domain.MemoryFunction `json:"function"`
	Mode                string                `json:"mode"`
	ApplyScope          domain.Scope          `json:"apply_scope"`
	WorkingExpiresAt    *time.Time            `json:"working_expires_at,omitempty"`
	WorkingGlobalRecall bool                  `json:"working_global_recall,omitempty"`
}

// FormationJobResult is persisted only after proposal apply and optional embedding succeed.
type FormationJobResult struct {
	MemoryID    domain.ID `json:"memory_id"`
	VersionID   domain.ID `json:"version_id"`
	OperationID domain.ID `json:"operation_id"`
	ProposalID  domain.ID `json:"proposal_id"`
}

// MemoryText is the canonical text embedded and indexed for one typed payload.
func MemoryText(payload domain.MemoryPayload) string {
	switch {
	case payload.Factual != nil:
		return payload.Factual.Predicate + "\n" + payload.Factual.Object.PlainText()
	case payload.Experiential != nil:
		return payload.Experiential.Lesson.PlainText()
	case payload.Working != nil:
		return payload.Working.Goal.PlainText() + "\n" + payload.Working.State.PlainText()
	default:
		return ""
	}
}

// EncodeFormationJob validates and encodes one durable job payload.
func EncodeFormationJob(payload FormationJobPayload) (json.RawMessage, error) {
	if payload.ObservationID == "" || payload.ApplyScope.Validate() != nil {
		return nil, ErrInvalidFormationJob
	}
	switch payload.Function {
	case domain.FunctionFactual, domain.FunctionExperiential, domain.FunctionWorking:
	default:
		return nil, ErrInvalidFormationJob
	}
	if payload.Mode != "generator-free" && payload.Mode != "assisted" {
		return nil, ErrInvalidFormationJob
	}
	return json.Marshal(payload)
}
