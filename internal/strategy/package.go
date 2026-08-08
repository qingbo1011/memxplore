// Package strategy describes reproducible, versioned memory algorithms.
package strategy

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// Fidelity describes how strongly a package is verified against its source.
type Fidelity string

const (
	FidelityOriginal         Fidelity = "original"
	FidelityReimplementation Fidelity = "reimplementation"
	FidelityAdaptation       Fidelity = "adaptation"
	FidelityBaseline         Fidelity = "baseline"
)

// RepairPolicy bounds schema repair instead of hiding retries in adapters.
type RepairPolicy struct {
	MaxAttempts int  `json:"max_attempts"`
	Strict      bool `json:"strict"`
}

// Package is the complete immutable identity of a strategy implementation.
type Package struct {
	ID             string          `json:"id"`
	Version        string          `json:"version"`
	Implementation string          `json:"implementation"`
	Fidelity       Fidelity        `json:"fidelity"`
	Prompt         string          `json:"prompt,omitempty"`
	JSONSchema     json.RawMessage `json:"json_schema,omitempty"`
	Parameters     json.RawMessage `json:"parameters"`
	Capabilities   []string        `json:"capabilities"`
	Repair         RepairPolicy    `json:"repair"`
	PaperSources   []string        `json:"paper_sources,omitempty"`
}

// Validate checks versioning, provenance, and all embedded JSON.
func (p Package) Validate() error {
	if strings.TrimSpace(p.ID) == "" || strings.TrimSpace(p.Version) == "" || strings.TrimSpace(p.Implementation) == "" {
		return fmt.Errorf("strategy id, version, and implementation are required")
	}
	switch p.Fidelity {
	case FidelityOriginal, FidelityReimplementation, FidelityAdaptation, FidelityBaseline:
	default:
		return fmt.Errorf("strategy fidelity %q is invalid", p.Fidelity)
	}
	if !json.Valid(p.Parameters) || (len(p.JSONSchema) > 0 && !json.Valid(p.JSONSchema)) {
		return fmt.Errorf("strategy parameters and schema must be valid JSON")
	}
	if len(p.Capabilities) == 0 || p.Repair.MaxAttempts < 0 {
		return fmt.Errorf("strategy capabilities and non-negative repair attempts are required")
	}
	return nil
}

// Hash returns the SHA-256 of canonical JSON with set-like slices sorted.
func (p Package) Hash() (string, error) {
	canonical, err := p.canonicalJSON()
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(canonical)
	return hex.EncodeToString(digest[:]), nil
}

// ExperimentHash binds strategy identity to provider, model, and fixture corpus.
func ExperimentHash(p Package, provider, model string, fixtureDigests []string) (string, error) {
	strategyHash, err := p.Hash()
	if err != nil {
		return "", err
	}
	fixtures := append([]string(nil), fixtureDigests...)
	slices.Sort(fixtures)
	encoded, err := json.Marshal(struct {
		Strategy string   `json:"strategy"`
		Provider string   `json:"provider"`
		Model    string   `json:"model"`
		Fixtures []string `json:"fixtures"`
	}{strategyHash, provider, model, fixtures})
	if err != nil {
		return "", fmt.Errorf("encode experiment identity: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func (p Package) canonicalJSON() ([]byte, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	p.Capabilities = sortedUnique(p.Capabilities)
	p.PaperSources = sortedUnique(p.PaperSources)
	parameters, err := canonicalRaw(p.Parameters)
	if err != nil {
		return nil, err
	}
	p.Parameters = parameters
	if len(p.JSONSchema) > 0 {
		p.JSONSchema, err = canonicalRaw(p.JSONSchema)
		if err != nil {
			return nil, err
		}
	}
	return json.Marshal(p)
}

func canonicalRaw(raw json.RawMessage) (json.RawMessage, error) {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("decode strategy JSON: %w", err)
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("canonicalize strategy JSON: %w", err)
	}
	return encoded, nil
}

func sortedUnique(values []string) []string {
	result := append([]string(nil), values...)
	slices.Sort(result)
	return slices.Compact(result)
}
