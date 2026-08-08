// Package api implements the versioned REST and MCP transport adapters.
package api

import (
	"encoding/json"
	"time"

	"github.com/qingbo1011/memxplore/internal/agentevent"
	"github.com/qingbo1011/memxplore/internal/application"
	"github.com/qingbo1011/memxplore/internal/auth"
	"github.com/qingbo1011/memxplore/internal/domain"
)

// RememberRequest captures evidence and schedules durable formation.
type RememberRequest struct {
	ObservationID       domain.ID             `json:"observation_id,omitempty"`
	IdempotencyKey      string                `json:"idempotency_key"`
	Owner               domain.ID             `json:"owner"`
	Subject             domain.ID             `json:"subject"`
	Context             domain.ID             `json:"context,omitempty"`
	Visibility          domain.Visibility     `json:"visibility,omitempty"`
	SourceKind          string                `json:"source_kind"`
	SourceReference     string                `json:"source_reference,omitempty"`
	Content             domain.Content        `json:"content"`
	EvidenceClass       domain.EvidenceClass  `json:"evidence_class,omitempty"`
	PolicyAuthority     string                `json:"policy_authority,omitempty"`
	CapturedAt          *time.Time            `json:"captured_at,omitempty"`
	Metadata            map[string]string     `json:"metadata,omitempty"`
	Function            domain.MemoryFunction `json:"function"`
	Strategy            string                `json:"strategy,omitempty"`
	WorkingTTLSeconds   int64                 `json:"working_ttl_seconds,omitempty"`
	WorkingGlobalRecall bool                  `json:"working_global_recall,omitempty"`
	WaitMilliseconds    int                   `json:"wait_milliseconds,omitempty"`
}

// RememberResponse reports durable job state and optional terminal result.
type RememberResponse struct {
	Job application.Job `json:"job"`
}

// RecallRequest is the REST wire request for structured evidence recall.
type RecallRequest struct {
	Owner                domain.ID                 `json:"owner"`
	Subject              domain.ID                 `json:"subject"`
	Context              domain.ID                 `json:"context,omitempty"`
	Query                string                    `json:"query"`
	Functions            []domain.MemoryFunction   `json:"functions,omitempty"`
	Mode                 application.RetrievalMode `json:"mode,omitempty"`
	ValidAt              *time.Time                `json:"valid_at,omitempty"`
	SystemAt             *time.Time                `json:"system_at,omitempty"`
	TokenBudget          int                       `json:"token_budget,omitempty"`
	CandidateLimit       int                       `json:"candidate_limit,omitempty"`
	IncludeGlobalWorking bool                      `json:"include_global_working,omitempty"`
}

// TokenCreateRequest creates a scoped credential in the caller's namespace.
type TokenCreateRequest struct {
	ID            domain.ID    `json:"id,omitempty"`
	PrincipalID   domain.ID    `json:"principal_id"`
	PrivateOwners []domain.ID  `json:"private_owners"`
	Scopes        []auth.Scope `json:"scopes"`
	AllowShared   bool         `json:"allow_shared,omitempty"`
	AllowPublic   bool         `json:"allow_public,omitempty"`
	ExpiresAt     *time.Time   `json:"expires_at,omitempty"`
}

// TokenCreateResponse contains one-time raw token material.
type TokenCreateResponse struct {
	ID    domain.ID `json:"id"`
	Token string    `json:"token"`
}

// AgentEventRequest opts one generic event into the formation pipeline.
type AgentEventRequest struct {
	Event            agentevent.Event      `json:"event"`
	Function         domain.MemoryFunction `json:"function"`
	Strategy         string                `json:"strategy,omitempty"`
	WaitMilliseconds int                   `json:"wait_milliseconds,omitempty"`
}

// ErrorResponse is the stable REST error envelope.
type ErrorResponse struct {
	Error APIError `json:"error"`
}

// APIError contains a machine code and safe message.
type APIError struct {
	Code    string          `json:"code"`
	Message string          `json:"message"`
	Details json.RawMessage `json:"details,omitempty"`
}

// TextContent is a convenience constructor for the most common wire content shape.
func TextContent(text string) domain.Content {
	return domain.Content{Parts: []domain.ContentPart{{Kind: domain.PartText, Text: text}}}
}
