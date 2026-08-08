// Package application defines use-case ports without depending on transports or vendors.
package application

import (
	"context"
	"encoding/json"
	"fmt"
)

// Message is one provider-neutral generator message.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// GenerationRequest asks a generator for text or schema-constrained JSON.
type GenerationRequest struct {
	Model          string          `json:"model"`
	Messages       []Message       `json:"messages"`
	JSONSchemaName string          `json:"json_schema_name,omitempty"`
	JSONSchema     json.RawMessage `json:"json_schema,omitempty"`
	Temperature    float64         `json:"temperature,omitempty"`
	MaxTokens      int             `json:"max_tokens,omitempty"`
}

// Usage is provider-reported token accounting.
type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

// GenerationResponse is the normalized generator result.
type GenerationResponse struct {
	Text         string `json:"text"`
	FinishReason string `json:"finish_reason,omitempty"`
	Usage        Usage  `json:"usage"`
}

// Generator is the only capability formation strategies need from an LLM.
type Generator interface {
	Generate(context.Context, GenerationRequest) (GenerationResponse, error)
}

// EmbeddingRequest asks for embeddings in input order.
type EmbeddingRequest struct {
	Model      string   `json:"model"`
	Input      []string `json:"input"`
	Dimensions int      `json:"dimensions,omitempty"`
}

// EmbeddingResponse contains one finite vector per input.
type EmbeddingResponse struct {
	Vectors [][]float32 `json:"vectors"`
	Usage   Usage       `json:"usage"`
}

// EmbeddingProvider is independent of retrieval and storage implementations.
type EmbeddingProvider interface {
	Embed(context.Context, EmbeddingRequest) (EmbeddingResponse, error)
}

// Validate checks a request before an adapter performs I/O.
func (r GenerationRequest) Validate() error {
	if r.Model == "" || len(r.Messages) == 0 {
		return fmt.Errorf("generation model and messages are required")
	}
	for index, message := range r.Messages {
		switch message.Role {
		case "system", "user", "assistant":
		default:
			return fmt.Errorf("message %d has invalid role %q", index, message.Role)
		}
		if message.Content == "" {
			return fmt.Errorf("message %d content is required", index)
		}
	}
	if len(r.JSONSchema) > 0 {
		if r.JSONSchemaName == "" || !json.Valid(r.JSONSchema) {
			return fmt.Errorf("valid named JSON schema is required")
		}
	}
	if r.Temperature < 0 || r.Temperature > 2 || r.MaxTokens < 0 {
		return fmt.Errorf("generation sampling parameters are invalid")
	}
	return nil
}

// Validate checks embedding input and dimensional expectations.
func (r EmbeddingRequest) Validate() error {
	if r.Model == "" || len(r.Input) == 0 || r.Dimensions < 0 {
		return fmt.Errorf("embedding model, input, and non-negative dimensions are required")
	}
	for index, input := range r.Input {
		if input == "" {
			return fmt.Errorf("embedding input %d is empty", index)
		}
	}
	return nil
}
