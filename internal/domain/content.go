package domain

import (
	"encoding/hex"
	"fmt"
	"strings"
)

// PartKind identifies an explicit content representation.
type PartKind string

const (
	PartText     PartKind = "text"
	PartArtifact PartKind = "artifact"
)

// ArtifactRef references immutable content-addressed bytes held outside memory rows.
type ArtifactRef struct {
	Digest    string `json:"digest"`
	MediaType string `json:"media_type"`
	Size      int64  `json:"size"`
}

// Validate requires a sha256 content address and descriptive media type.
func (r ArtifactRef) Validate() error {
	const prefix = "sha256:"
	if !strings.HasPrefix(r.Digest, prefix) {
		return fmt.Errorf("artifact.digest must use sha256")
	}
	digest := strings.TrimPrefix(r.Digest, prefix)
	decoded, err := hex.DecodeString(digest)
	if err != nil || len(decoded) != 32 {
		return fmt.Errorf("artifact.digest must contain 32-byte sha256 hex")
	}
	if err := validateRequiredText("artifact.media_type", r.MediaType, 255); err != nil {
		return err
	}
	if r.Size < 0 {
		return fmt.Errorf("artifact.size cannot be negative")
	}
	return nil
}

// ContentPart contains exactly one typed representation.
type ContentPart struct {
	Kind     PartKind     `json:"kind"`
	Text     string       `json:"text,omitempty"`
	Artifact *ArtifactRef `json:"artifact,omitempty"`
}

// Validate rejects ambiguous or empty parts.
func (p ContentPart) Validate() error {
	switch p.Kind {
	case PartText:
		if p.Artifact != nil {
			return fmt.Errorf("text part cannot contain an artifact")
		}
		return validateRequiredText("content.text", p.Text, 1<<20)
	case PartArtifact:
		if p.Text != "" || p.Artifact == nil {
			return fmt.Errorf("artifact part requires only artifact metadata")
		}
		return p.Artifact.Validate()
	default:
		return fmt.Errorf("content part kind %q is unsupported", p.Kind)
	}
}

// Content is an ordered collection of typed parts.
type Content struct {
	Parts []ContentPart `json:"parts"`
}

// Validate checks every content part.
func (c Content) Validate() error {
	if len(c.Parts) == 0 {
		return fmt.Errorf("content requires at least one part")
	}
	for index, part := range c.Parts {
		if err := part.Validate(); err != nil {
			return fmt.Errorf("content.parts[%d]: %w", index, err)
		}
	}
	return nil
}

// PlainText returns the text parts used by lexical and embedding adapters.
func (c Content) PlainText() string {
	parts := make([]string, 0, len(c.Parts))
	for _, part := range c.Parts {
		if part.Kind == PartText {
			parts = append(parts, strings.TrimSpace(part.Text))
		}
	}
	return strings.Join(parts, "\n")
}
