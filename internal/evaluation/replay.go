package evaluation

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/qingbo1011/memxplore/internal/domain"
)

// ReplayResult is a deterministic validation summary reconstructed from a trace artifact.
type ReplayResult struct {
	ID             string `json:"id"`
	Kind           string `json:"kind"`
	CandidateCount int    `json:"candidate_count,omitempty"`
	SelectedCount  int    `json:"selected_count,omitempty"`
	TokensUsed     int    `json:"tokens_used,omitempty"`
}

// AdapterTrace records a deterministic dataset-adapter materialization decision.
type AdapterTrace struct {
	ID            string   `json:"id"`
	Adapter       string   `json:"adapter"`
	CaseID        string   `json:"case_id"`
	InputIDs      []string `json:"input_ids"`
	ResolvedIDs   []string `json:"resolved_ids"`
	DatasetSHA256 string   `json:"dataset_sha256"`
}

// NewTraceReference embeds and hashes one self-contained trace payload.
func NewTraceReference(caseID, variant, kind string, value any) (TraceReference, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return TraceReference{}, err
	}
	var identity struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(payload, &identity); err != nil || identity.ID == "" {
		return TraceReference{}, fmt.Errorf("trace payload requires an id")
	}
	digest := sha256.Sum256(payload)
	return TraceReference{
		ID: identity.ID, CaseID: caseID, Variant: variant, Kind: kind,
		Location: "artifact:traces.jsonl#" + identity.ID, SHA256: hex.EncodeToString(digest[:]), Payload: payload,
	}, nil
}

// ReplayTrace verifies the payload digest and reconstructs lifecycle or retrieval decisions.
func ReplayTrace(reference TraceReference) (ReplayResult, error) {
	if reference.ID == "" || reference.Kind == "" || len(reference.Payload) == 0 {
		return ReplayResult{}, fmt.Errorf("trace reference identity, kind, and payload are required")
	}
	digest := sha256.Sum256(reference.Payload)
	if reference.SHA256 != "" && hex.EncodeToString(digest[:]) != reference.SHA256 {
		return ReplayResult{}, fmt.Errorf("trace %s payload digest mismatch", reference.ID)
	}
	switch reference.Kind {
	case "retrieval":
		var trace domain.RetrievalTrace
		if err := json.Unmarshal(reference.Payload, &trace); err != nil {
			return ReplayResult{}, fmt.Errorf("decode retrieval trace: %w", err)
		}
		if string(trace.ID) != reference.ID {
			return ReplayResult{}, fmt.Errorf("retrieval trace identity mismatch")
		}
		if err := trace.Validate(); err != nil {
			return ReplayResult{}, fmt.Errorf("validate retrieval trace: %w", err)
		}
		selected := 0
		for _, candidate := range trace.Candidates {
			if candidate.Selected {
				selected++
			}
		}
		return ReplayResult{ID: reference.ID, Kind: reference.Kind, CandidateCount: len(trace.Candidates), SelectedCount: selected, TokensUsed: trace.TokensUsed}, nil
	case "lifecycle":
		var operation domain.Operation
		if err := json.Unmarshal(reference.Payload, &operation); err != nil {
			return ReplayResult{}, fmt.Errorf("decode lifecycle trace: %w", err)
		}
		if string(operation.ID) != reference.ID || operation.Phase != domain.PhaseApply || operation.TargetID == "" || operation.Actor == "" || operation.OccurredAt.IsZero() || operation.Result == "" {
			return ReplayResult{}, fmt.Errorf("lifecycle trace is incomplete")
		}
		return ReplayResult{ID: reference.ID, Kind: reference.Kind}, nil
	case "adapter":
		var trace AdapterTrace
		if err := json.Unmarshal(reference.Payload, &trace); err != nil {
			return ReplayResult{}, fmt.Errorf("decode adapter trace: %w", err)
		}
		if trace.ID != reference.ID || trace.Adapter == "" || trace.CaseID == "" || len(trace.DatasetSHA256) != 64 || len(trace.InputIDs) == 0 || len(trace.InputIDs) != len(trace.ResolvedIDs) {
			return ReplayResult{}, fmt.Errorf("adapter trace is incomplete")
		}
		for index := range trace.InputIDs {
			if trace.InputIDs[index] == "" || trace.InputIDs[index] != trace.ResolvedIDs[index] {
				return ReplayResult{}, fmt.Errorf("adapter trace resolution mismatch at index %d", index)
			}
		}
		return ReplayResult{ID: reference.ID, Kind: reference.Kind, CandidateCount: len(trace.InputIDs), SelectedCount: len(trace.ResolvedIDs)}, nil
	default:
		return ReplayResult{}, fmt.Errorf("trace kind %q is unsupported", reference.Kind)
	}
}
