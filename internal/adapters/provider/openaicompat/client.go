// Package openaicompat adapts an explicitly configured OpenAI-compatible endpoint.
// It never discovers credentials or endpoints from the process environment.
package openaicompat

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/qingbo1011/memxplore/internal/application"
)

const maxResponseBytes = 16 << 20

// Config must be supplied by an authorized caller; no environment fallback exists.
type Config struct {
	BaseURL string
	APIKey  string
	Client  *http.Client
}

// Client implements provider-neutral generation and embedding ports.
type Client struct {
	baseURL *url.URL
	apiKey  string
	http    *http.Client
}

// New validates explicit configuration.
func New(config Config) (*Client, error) {
	parsed, err := url.Parse(config.BaseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, fmt.Errorf("OpenAI-compatible base URL must be an absolute HTTP(S) URL")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	parsed.RawQuery = ""
	parsed.Fragment = ""
	httpClient := config.Client
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 2 * time.Minute}
	}
	return &Client{baseURL: parsed, apiKey: config.APIKey, http: httpClient}, nil
}

// Generate calls /chat/completions and supports strict JSON-schema response format.
func (c *Client) Generate(ctx context.Context, request application.GenerationRequest) (application.GenerationResponse, error) {
	if err := request.Validate(); err != nil {
		return application.GenerationResponse{}, err
	}
	body := chatRequest{
		Model: request.Model, Messages: request.Messages, Temperature: request.Temperature,
		MaxTokens: request.MaxTokens,
	}
	if len(request.JSONSchema) > 0 {
		body.ResponseFormat = &responseFormat{Type: "json_schema", JSONSchema: schemaFormat{
			Name: request.JSONSchemaName, Strict: true, Schema: request.JSONSchema,
		}}
	}
	var response chatResponse
	if err := c.post(ctx, "/chat/completions", body, &response); err != nil {
		return application.GenerationResponse{}, err
	}
	if len(response.Choices) == 0 {
		return application.GenerationResponse{}, fmt.Errorf("provider returned no generation choices")
	}
	choice := response.Choices[0]
	return application.GenerationResponse{
		Text: choice.Message.Content, FinishReason: choice.FinishReason,
		Usage: application.Usage{InputTokens: response.Usage.Prompt, OutputTokens: response.Usage.Completion, TotalTokens: response.Usage.Total},
	}, nil
}

// Embed calls /embeddings and restores the requested order using response indexes.
func (c *Client) Embed(ctx context.Context, request application.EmbeddingRequest) (application.EmbeddingResponse, error) {
	if err := request.Validate(); err != nil {
		return application.EmbeddingResponse{}, err
	}
	body := embeddingRequest{Model: request.Model, Input: request.Input, Dimensions: request.Dimensions}
	var response embeddingResponse
	if err := c.post(ctx, "/embeddings", body, &response); err != nil {
		return application.EmbeddingResponse{}, err
	}
	if len(response.Data) != len(request.Input) {
		return application.EmbeddingResponse{}, fmt.Errorf("provider returned %d embeddings for %d inputs", len(response.Data), len(request.Input))
	}
	vectors := make([][]float32, len(request.Input))
	for _, item := range response.Data {
		if item.Index < 0 || item.Index >= len(vectors) || vectors[item.Index] != nil {
			return application.EmbeddingResponse{}, fmt.Errorf("provider returned invalid embedding index %d", item.Index)
		}
		if request.Dimensions > 0 && len(item.Embedding) != request.Dimensions {
			return application.EmbeddingResponse{}, fmt.Errorf("embedding %d has dimension %d, want %d", item.Index, len(item.Embedding), request.Dimensions)
		}
		for _, value := range item.Embedding {
			if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
				return application.EmbeddingResponse{}, fmt.Errorf("embedding %d contains a non-finite value", item.Index)
			}
		}
		vectors[item.Index] = item.Embedding
	}
	return application.EmbeddingResponse{Vectors: vectors, Usage: application.Usage{
		InputTokens: response.Usage.Prompt, TotalTokens: response.Usage.Total,
	}}, nil
}

func (c *Client) post(ctx context.Context, path string, input, output any) error {
	encoded, err := json.Marshal(input)
	if err != nil {
		return fmt.Errorf("encode provider request: %w", err)
	}
	endpoint := *c.baseURL
	endpoint.Path += path
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(encoded))
	if err != nil {
		return fmt.Errorf("create provider request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		request.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	response, err := c.http.Do(request)
	if err != nil {
		return fmt.Errorf("call provider: %w", err)
	}
	defer response.Body.Close()
	limited := io.LimitReader(response.Body, maxResponseBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return fmt.Errorf("read provider response: %w", err)
	}
	if len(data) > maxResponseBytes {
		return fmt.Errorf("provider response exceeds %d bytes", maxResponseBytes)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("provider returned HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(data)))
	}
	if err := json.Unmarshal(data, output); err != nil {
		return fmt.Errorf("decode provider response: %w", err)
	}
	return nil
}

type chatRequest struct {
	Model          string                `json:"model"`
	Messages       []application.Message `json:"messages"`
	Temperature    float64               `json:"temperature,omitempty"`
	MaxTokens      int                   `json:"max_tokens,omitempty"`
	ResponseFormat *responseFormat       `json:"response_format,omitempty"`
}

type responseFormat struct {
	Type       string       `json:"type"`
	JSONSchema schemaFormat `json:"json_schema"`
}

type schemaFormat struct {
	Name   string          `json:"name"`
	Strict bool            `json:"strict"`
	Schema json.RawMessage `json:"schema"`
}

type chatResponse struct {
	Choices []struct {
		Message      application.Message `json:"message"`
		FinishReason string              `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		Prompt     int `json:"prompt_tokens"`
		Completion int `json:"completion_tokens"`
		Total      int `json:"total_tokens"`
	} `json:"usage"`
}

type embeddingRequest struct {
	Model      string   `json:"model"`
	Input      []string `json:"input"`
	Dimensions int      `json:"dimensions,omitempty"`
}

type embeddingResponse struct {
	Data []struct {
		Index     int       `json:"index"`
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
	Usage struct {
		Prompt int `json:"prompt_tokens"`
		Total  int `json:"total_tokens"`
	} `json:"usage"`
}
