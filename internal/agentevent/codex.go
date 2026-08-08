package agentevent

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/qingbo1011/memxplore/internal/domain"
)

// CodexEnvelope is the supported v1 JSONL adapter input.
type CodexEnvelope struct {
	ID        string            `json:"id"`
	Type      string            `json:"type"`
	Timestamp time.Time         `json:"timestamp"`
	ThreadID  string            `json:"thread_id,omitempty"`
	TurnID    string            `json:"turn_id,omitempty"`
	Role      string            `json:"role,omitempty"`
	Text      string            `json:"text"`
	Metadata  map[string]string `json:"metadata,omitempty"`
}

// ParseCodexJSON maps an explicit Codex JSONL envelope into AgentEvent v1.
func ParseCodexJSON(data []byte, owner, subject domain.ID) (Event, error) {
	var envelope CodexEnvelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		return Event{}, fmt.Errorf("decode Codex event: %w", err)
	}
	if envelope.ID == "" || strings.TrimSpace(envelope.Text) == "" || envelope.Timestamp.IsZero() {
		return Event{}, fmt.Errorf("codex event requires id, text, and timestamp")
	}
	eventType := EventMessage
	switch envelope.Type {
	case "message", "assistant_message", "user_message":
		eventType = EventMessage
	case "tool_result", "command_result":
		eventType = EventToolResult
	case "outcome":
		eventType = EventOutcome
	case "task_state", "turn_state":
		eventType = EventTaskState
	default:
		return Event{}, fmt.Errorf("unsupported Codex event type %q", envelope.Type)
	}
	metadata := make(map[string]string, len(envelope.Metadata)+3)
	for key, value := range envelope.Metadata {
		metadata[key] = value
	}
	metadata["thread_id"] = envelope.ThreadID
	metadata["turn_id"] = envelope.TurnID
	metadata["role"] = envelope.Role
	return Event{
		SchemaVersion: SchemaV1, ID: domain.ID(envelope.ID), Source: "codex", Type: eventType,
		Owner: owner, Subject: subject, Context: domain.ID(envelope.ThreadID),
		Content:    domain.Content{Parts: []domain.ContentPart{{Kind: domain.PartText, Text: envelope.Text}}},
		OccurredAt: envelope.Timestamp, Metadata: metadata,
	}, nil
}
