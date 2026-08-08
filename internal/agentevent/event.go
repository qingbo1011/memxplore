// Package agentevent defines the versioned, opt-in generic agent ingestion protocol.
package agentevent

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/qingbo1011/memxplore/internal/domain"
)

const SchemaV1 = "v1"

// EventType is a protocol-level observation category, not a vendor event name.
type EventType string

const (
	EventMessage    EventType = "message"
	EventToolResult EventType = "tool_result"
	EventOutcome    EventType = "outcome"
	EventTaskState  EventType = "task_state"
)

// Event is a generic opt-in envelope that preserves the source event as evidence.
type Event struct {
	SchemaVersion string            `json:"schema_version"`
	ID            domain.ID         `json:"id"`
	Source        string            `json:"source"`
	Type          EventType         `json:"type"`
	Owner         domain.ID         `json:"owner"`
	Subject       domain.ID         `json:"subject"`
	Context       domain.ID         `json:"context,omitempty"`
	Content       domain.Content    `json:"content"`
	OccurredAt    time.Time         `json:"occurred_at"`
	Metadata      map[string]string `json:"metadata,omitempty"`
}

// Validate checks the vendor-neutral wire contract.
func (e Event) Validate() error {
	if e.SchemaVersion != SchemaV1 || e.ID == "" || e.Source == "" || e.Owner == "" || e.Subject == "" || e.OccurredAt.IsZero() {
		return fmt.Errorf("AgentEvent v1 requires schema, id, source, owner, subject, and timestamp")
	}
	switch e.Type {
	case EventMessage, EventToolResult, EventOutcome, EventTaskState:
	default:
		return fmt.Errorf("AgentEvent type %q is invalid", e.Type)
	}
	return e.Content.Validate()
}

// Observation converts an authorized event into immutable untrusted evidence.
func (e Event) Observation(namespace, actor domain.ID, visibility domain.Visibility) (domain.Observation, error) {
	if err := e.Validate(); err != nil {
		return domain.Observation{}, err
	}
	digest := sha256.Sum256([]byte(string(e.ID) + "\x00" + e.Source))
	metadata := make(map[string]string, len(e.Metadata)+2)
	for key, value := range e.Metadata {
		metadata[key] = value
	}
	metadata["agent_event_id"] = string(e.ID)
	metadata["agent_event_type"] = string(e.Type)
	observation := domain.Observation{
		ID: domain.ID("obs-event-" + hex.EncodeToString(digest[:12])),
		Scope: domain.Scope{
			Namespace: namespace, Owner: e.Owner, Subject: e.Subject, Actor: actor,
			Context: e.Context, Visibility: visibility,
		},
		SourceKind: "agent-event:" + e.Source, SourceReference: string(e.ID),
		Content: e.Content, EvidenceClass: domain.EvidenceUntrusted,
		CapturedAt: e.OccurredAt, Metadata: metadata,
	}
	if err := observation.Validate(); err != nil {
		return domain.Observation{}, err
	}
	return observation, nil
}
