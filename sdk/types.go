// Package sdk is the stable Go client for MemXplore protocol v1.
package sdk

import (
	"encoding/json"
	"time"
)

// ID is an opaque protocol identifier.
type ID string

// ContentPart is one text or content-addressed artifact part.
type ContentPart struct {
	Kind     string       `json:"kind"`
	Text     string       `json:"text,omitempty"`
	Artifact *ArtifactRef `json:"artifact,omitempty"`
}

// ArtifactRef describes immutable bytes held outside memory rows.
type ArtifactRef struct {
	Digest    string `json:"digest"`
	MediaType string `json:"media_type"`
	Size      int64  `json:"size"`
}

// Content is ordered evidence content.
type Content struct {
	Parts []ContentPart `json:"parts"`
}

// TextContent constructs text evidence.
func TextContent(text string) Content {
	return Content{Parts: []ContentPart{{Kind: "text", Text: text}}}
}

// RememberRequest captures evidence and schedules durable formation.
type RememberRequest struct {
	ObservationID       ID                `json:"observation_id,omitempty"`
	IdempotencyKey      string            `json:"idempotency_key"`
	Owner               ID                `json:"owner"`
	Subject             ID                `json:"subject"`
	Context             ID                `json:"context,omitempty"`
	Visibility          string            `json:"visibility,omitempty"`
	SourceKind          string            `json:"source_kind"`
	SourceReference     string            `json:"source_reference,omitempty"`
	Content             Content           `json:"content"`
	EvidenceClass       string            `json:"evidence_class,omitempty"`
	PolicyAuthority     string            `json:"policy_authority,omitempty"`
	CapturedAt          *time.Time        `json:"captured_at,omitempty"`
	Metadata            map[string]string `json:"metadata,omitempty"`
	Function            string            `json:"function"`
	Strategy            string            `json:"strategy,omitempty"`
	WorkingTTLSeconds   int64             `json:"working_ttl_seconds,omitempty"`
	WorkingGlobalRecall bool              `json:"working_global_recall,omitempty"`
	WaitMilliseconds    int               `json:"wait_milliseconds,omitempty"`
}

// Job is the durable formation state machine returned by remember.
type Job struct {
	ID             ID              `json:"id"`
	Namespace      ID              `json:"namespace"`
	Kind           string          `json:"kind"`
	State          string          `json:"state"`
	IdempotencyKey string          `json:"idempotency_key"`
	Payload        json.RawMessage `json:"payload"`
	Result         json.RawMessage `json:"result,omitempty"`
	ErrorCode      string          `json:"error_code,omitempty"`
	ErrorMessage   string          `json:"error_message,omitempty"`
	Attempts       int             `json:"attempts"`
	AvailableAt    time.Time       `json:"available_at"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
}

// RememberResponse reports durable job state.
type RememberResponse struct {
	Job Job `json:"job"`
}

// RecallRequest asks for evidence, never a generated answer.
type RecallRequest struct {
	Owner                ID         `json:"owner"`
	Subject              ID         `json:"subject"`
	Context              ID         `json:"context,omitempty"`
	Query                string     `json:"query"`
	Functions            []string   `json:"functions,omitempty"`
	Mode                 string     `json:"mode,omitempty"`
	ValidAt              *time.Time `json:"valid_at,omitempty"`
	SystemAt             *time.Time `json:"system_at,omitempty"`
	TokenBudget          int        `json:"token_budget,omitempty"`
	CandidateLimit       int        `json:"candidate_limit,omitempty"`
	IncludeGlobalWorking bool       `json:"include_global_working,omitempty"`
}

// RecallItem is one provenance-bearing memory version.
type RecallItem struct {
	MemoryID        ID              `json:"memory_id"`
	VersionID       ID              `json:"version_id"`
	Function        string          `json:"function"`
	ConflictGroup   ID              `json:"conflict_group,omitempty"`
	Payload         json.RawMessage `json:"payload"`
	Provenance      json.RawMessage `json:"provenance"`
	EstimatedTokens int             `json:"estimated_tokens"`
	Score           json.RawMessage `json:"score"`
}

// RecallGroup keeps conflicting alternatives together.
type RecallGroup struct {
	ID       string       `json:"id"`
	Conflict bool         `json:"conflict"`
	Items    []RecallItem `json:"items"`
}

// RecallBundle is structured retrieval evidence, deliberately not an answer.
type RecallBundle struct {
	Query          string          `json:"query"`
	Mode           string          `json:"mode"`
	FallbackReason string          `json:"fallback_reason,omitempty"`
	Items          []RecallItem    `json:"items"`
	Groups         []RecallGroup   `json:"groups"`
	Trace          json.RawMessage `json:"trace"`
}

// Version describes protocol and persisted schema compatibility.
type Version struct {
	Program              string `json:"program"`
	Version              string `json:"version"`
	ProtocolVersion      string `json:"protocol_version"`
	StorageSchemaVersion int    `json:"storage_schema_version"`
	ExportSchemaVersion  int    `json:"export_schema_version"`
}

// PurgeReceipt contains no memory content.
type PurgeReceipt struct {
	ID                ID        `json:"id"`
	Namespace         ID        `json:"namespace"`
	TargetID          ID        `json:"target_id"`
	Actor             ID        `json:"actor"`
	VersionsDeleted   int       `json:"versions_deleted"`
	ArtifactsDetached int       `json:"artifacts_detached"`
	PurgedAt          time.Time `json:"purged_at"`
}

// AgentEvent is the vendor-neutral opt-in agent ingestion envelope.
type AgentEvent struct {
	SchemaVersion string            `json:"schema_version"`
	ID            ID                `json:"id"`
	Source        string            `json:"source"`
	Type          string            `json:"type"`
	Owner         ID                `json:"owner"`
	Subject       ID                `json:"subject"`
	Context       ID                `json:"context,omitempty"`
	Content       Content           `json:"content"`
	OccurredAt    time.Time         `json:"occurred_at"`
	Metadata      map[string]string `json:"metadata,omitempty"`
}

// AgentEventRequest selects formation for an AgentEvent.
type AgentEventRequest struct {
	Event            AgentEvent `json:"event"`
	Function         string     `json:"function"`
	Strategy         string     `json:"strategy,omitempty"`
	WaitMilliseconds int        `json:"wait_milliseconds,omitempty"`
}
