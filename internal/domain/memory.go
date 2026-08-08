package domain

import (
	"fmt"
	"time"
)

// MemoryState is lifecycle state, deliberately excluding irreversible purge.
type MemoryState string

const (
	MemoryActive    MemoryState = "active"
	MemoryArchived  MemoryState = "archived"
	MemoryForgotten MemoryState = "forgotten"
)

// VersionState captures evolution of an immutable version.
type VersionState string

const (
	VersionCurrent    VersionState = "current"
	VersionSuperseded VersionState = "superseded"
	VersionArchived   VersionState = "archived"
	VersionForgotten  VersionState = "forgotten"
	VersionStale      VersionState = "stale"
)

// EvidenceRef links a memory version to immutable source evidence.
type EvidenceRef struct {
	ObservationID ID     `json:"observation_id"`
	PartIndex     int    `json:"part_index"`
	Excerpt       string `json:"excerpt,omitempty"`
}

// Memory is the stable identity shared by immutable versions.
type Memory struct {
	ID             ID             `json:"id"`
	Scope          Scope          `json:"scope"`
	Function       MemoryFunction `json:"function"`
	State          MemoryState    `json:"state"`
	CurrentVersion int            `json:"current_version"`
	CreatedAt      time.Time      `json:"created_at"`
}

// Validate checks stable memory identity and state.
func (m Memory) Validate() error {
	if err := validateID("memory.id", m.ID, true); err != nil {
		return err
	}
	if err := m.Scope.Validate(); err != nil {
		return err
	}
	switch m.Function {
	case FunctionFactual, FunctionExperiential, FunctionWorking:
	default:
		return fmt.Errorf("memory.function %q is invalid", m.Function)
	}
	switch m.State {
	case MemoryActive, MemoryArchived, MemoryForgotten:
	default:
		return fmt.Errorf("memory.state %q is invalid", m.State)
	}
	if m.CurrentVersion < 1 {
		return fmt.Errorf("memory.current_version must be positive")
	}
	if m.CreatedAt.IsZero() {
		return fmt.Errorf("memory.created_at is required")
	}
	return nil
}

// MemoryVersion is immutable bitemporal content. Conflicting current versions may coexist.
type MemoryVersion struct {
	ID            ID            `json:"id"`
	MemoryID      ID            `json:"memory_id"`
	Number        int           `json:"number"`
	State         VersionState  `json:"state"`
	Taxonomy      Taxonomy      `json:"taxonomy"`
	ValidTime     TimeRange     `json:"valid_time"`
	SystemTime    TimeRange     `json:"system_time"`
	ConflictGroup ID            `json:"conflict_group,omitempty"`
	Supersedes    []ID          `json:"supersedes,omitempty"`
	DerivedFrom   []ID          `json:"derived_from,omitempty"`
	Provenance    []EvidenceRef `json:"provenance"`
	Payload       MemoryPayload `json:"payload"`
}

// MemoryPayload contains exactly one function-specific representation.
type MemoryPayload struct {
	Factual      *FactualMemory      `json:"factual,omitempty"`
	Experiential *ExperientialMemory `json:"experiential,omitempty"`
	Working      *WorkingMemory      `json:"working,omitempty"`
}

// Validate checks bitemporal invariants and exactly one payload.
func (v MemoryVersion) Validate(function MemoryFunction) error {
	if err := validateID("memory_version.id", v.ID, true); err != nil {
		return err
	}
	if err := validateID("memory_version.memory_id", v.MemoryID, true); err != nil {
		return err
	}
	if v.Number < 1 {
		return fmt.Errorf("memory_version.number must be positive")
	}
	switch v.State {
	case VersionCurrent, VersionSuperseded, VersionArchived, VersionForgotten, VersionStale:
	default:
		return fmt.Errorf("memory_version.state %q is invalid", v.State)
	}
	if err := v.Taxonomy.Validate(); err != nil {
		return err
	}
	if err := v.ValidTime.Validate("memory_version.valid_time"); err != nil {
		return err
	}
	if err := v.SystemTime.Validate("memory_version.system_time"); err != nil {
		return err
	}
	if err := validateID("memory_version.conflict_group", v.ConflictGroup, false); err != nil {
		return err
	}
	if len(v.Provenance) == 0 {
		return fmt.Errorf("memory_version.provenance requires evidence")
	}
	for _, evidence := range v.Provenance {
		if err := validateID("memory_version.provenance.observation_id", evidence.ObservationID, true); err != nil {
			return err
		}
		if evidence.PartIndex < 0 {
			return fmt.Errorf("memory_version.provenance.part_index cannot be negative")
		}
	}
	return v.Payload.Validate(function)
}

// Validate checks that the payload matches the memory function.
func (p MemoryPayload) Validate(function MemoryFunction) error {
	count := 0
	if p.Factual != nil {
		count++
	}
	if p.Experiential != nil {
		count++
	}
	if p.Working != nil {
		count++
	}
	if count != 1 {
		return fmt.Errorf("memory payload must contain exactly one function")
	}
	switch function {
	case FunctionFactual:
		if p.Factual == nil {
			return fmt.Errorf("factual memory requires factual payload")
		}
		return p.Factual.Validate()
	case FunctionExperiential:
		if p.Experiential == nil {
			return fmt.Errorf("experiential memory requires experiential payload")
		}
		return p.Experiential.Validate()
	case FunctionWorking:
		if p.Working == nil {
			return fmt.Errorf("working memory requires working payload")
		}
		return p.Working.Validate()
	default:
		return fmt.Errorf("memory function %q is invalid", function)
	}
}
