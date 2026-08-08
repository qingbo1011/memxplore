package domain

import "fmt"

// EpistemicStatus distinguishes evidence confidence from retrieval score.
type EpistemicStatus string

const (
	EpistemicObserved  EpistemicStatus = "observed"
	EpistemicAsserted  EpistemicStatus = "asserted"
	EpistemicInferred  EpistemicStatus = "inferred"
	EpistemicContested EpistemicStatus = "contested"
	EpistemicUnknown   EpistemicStatus = "unknown"
)

// FactualMemory is a claim with explicit subject, provenance, and epistemic state.
type FactualMemory struct {
	ClaimSubject ID              `json:"claim_subject"`
	Predicate    string          `json:"predicate"`
	Object       Content         `json:"object"`
	Epistemic    EpistemicStatus `json:"epistemic"`
	Confidence   *float64        `json:"confidence,omitempty"`
}

// Validate checks claim structure without collapsing conflicts.
func (f FactualMemory) Validate() error {
	if err := validateID("factual.claim_subject", f.ClaimSubject, true); err != nil {
		return err
	}
	if err := validateRequiredText("factual.predicate", f.Predicate, 255); err != nil {
		return err
	}
	if err := f.Object.Validate(); err != nil {
		return fmt.Errorf("factual.object: %w", err)
	}
	switch f.Epistemic {
	case EpistemicObserved, EpistemicAsserted, EpistemicInferred, EpistemicContested, EpistemicUnknown:
	default:
		return fmt.Errorf("factual.epistemic %q is invalid", f.Epistemic)
	}
	if f.Confidence != nil && (*f.Confidence < 0 || *f.Confidence > 1) {
		return fmt.Errorf("factual.confidence must be within [0,1]")
	}
	return nil
}
