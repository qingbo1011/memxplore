package domain

import "fmt"

var (
	knownForms = map[string]struct{}{
		"token-flat": {}, "token-planar": {}, "token-hierarchical": {}, "parametric": {}, "latent": {},
	}
	knownFunctions = map[string]struct{}{
		"factual": {}, "experiential": {}, "working": {},
	}
	knownDynamics = map[string]struct{}{
		"formation": {}, "evolution": {}, "retrieval": {},
	}
)

// MemoryFunction is the primary functional payload of a memory version.
type MemoryFunction string

const (
	FunctionFactual      MemoryFunction = "factual"
	FunctionExperiential MemoryFunction = "experiential"
	FunctionWorking      MemoryFunction = "working"
)

// Taxonomy is multi-label research metadata with validated x- extension labels.
type Taxonomy struct {
	Forms     []string `json:"forms"`
	Functions []string `json:"functions"`
	Dynamics  []string `json:"dynamics"`
	Tags      []string `json:"tags,omitempty"`
}

// Validate checks registered labels, extensions, tags, and duplicates.
func (t Taxonomy) Validate() error {
	if err := validateLabels("taxonomy.forms", t.Forms, knownForms, true); err != nil {
		return err
	}
	if err := validateLabels("taxonomy.functions", t.Functions, knownFunctions, true); err != nil {
		return err
	}
	if err := validateLabels("taxonomy.dynamics", t.Dynamics, knownDynamics, true); err != nil {
		return err
	}
	seen := make(map[string]struct{}, len(t.Tags))
	for _, tag := range t.Tags {
		if !tagPattern.MatchString(tag) {
			return fmt.Errorf("taxonomy tag %q is invalid", tag)
		}
		if _, duplicate := seen[tag]; duplicate {
			return fmt.Errorf("taxonomy contains duplicate tag %q", tag)
		}
		seen[tag] = struct{}{}
	}
	return nil
}
