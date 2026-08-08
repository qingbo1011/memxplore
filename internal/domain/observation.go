package domain

import (
	"fmt"
	"time"
)

// EvidenceClass controls whether stored content may ever act as policy.
type EvidenceClass string

const (
	EvidenceUntrusted EvidenceClass = "untrusted"
	EvidenceTrusted   EvidenceClass = "trusted"
	EvidencePolicy    EvidenceClass = "policy"
)

// Observation is an immutable captured input to memory formation.
type Observation struct {
	ID              ID                `json:"id"`
	Scope           Scope             `json:"scope"`
	SourceKind      string            `json:"source_kind"`
	SourceReference string            `json:"source_reference,omitempty"`
	Content         Content           `json:"content"`
	EvidenceClass   EvidenceClass     `json:"evidence_class"`
	PolicyAuthority string            `json:"policy_authority,omitempty"`
	CapturedAt      time.Time         `json:"captured_at"`
	Metadata        map[string]string `json:"metadata,omitempty"`
}

// Validate enforces that ordinary observations cannot become instructions.
func (o Observation) Validate() error {
	if err := validateID("observation.id", o.ID, true); err != nil {
		return err
	}
	if err := o.Scope.Validate(); err != nil {
		return err
	}
	if err := validateRequiredText("observation.source_kind", o.SourceKind, 64); err != nil {
		return err
	}
	if err := o.Content.Validate(); err != nil {
		return err
	}
	if o.CapturedAt.IsZero() {
		return fmt.Errorf("observation.captured_at is required")
	}
	switch o.EvidenceClass {
	case EvidenceUntrusted, EvidenceTrusted:
		if o.PolicyAuthority != "" {
			return fmt.Errorf("non-policy observation cannot name policy authority")
		}
	case EvidencePolicy:
		if err := validateRequiredText("observation.policy_authority", o.PolicyAuthority, 128); err != nil {
			return err
		}
	default:
		return fmt.Errorf("observation.evidence_class %q is invalid", o.EvidenceClass)
	}
	return nil
}
