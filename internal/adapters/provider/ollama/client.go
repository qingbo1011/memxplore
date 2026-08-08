// Package ollama adapts an explicitly configured Ollama native endpoint.
package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/qingbo1011/memxplore/internal/application"
)

const maxResponseBytes = 16 << 20

// Config supplies the local endpoint and optional thinking behavior explicitly.
type Config struct {
	BaseURL string
	Think   *bool
	Client  *http.Client
}

// Client implements the provider-neutral Generator port using /api/chat.
type Client struct {
	baseURL *url.URL
	think   *bool
	http    *http.Client
}

// New validates explicit Ollama configuration without environment discovery.
func New(config Config) (*Client, error) {
	parsed, err := url.Parse(config.BaseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, fmt.Errorf("ollama base URL must be an absolute HTTP(S) URL")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	parsed.RawQuery = ""
	parsed.Fragment = ""
	httpClient := config.Client
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 2 * time.Minute}
	}
	return &Client{baseURL: parsed, think: config.Think, http: httpClient}, nil
}

// Generate calls Ollama's native non-streaming chat endpoint.
func (c *Client) Generate(ctx context.Context, request application.GenerationRequest) (application.GenerationResponse, error) {
	if err := request.Validate(); err != nil {
		return application.GenerationResponse{}, err
	}
	body := chatRequest{
		Model: request.Model, Messages: request.Messages, Stream: false, Think: c.think,
		Options: chatOptions{Temperature: request.Temperature, NumPredict: request.MaxTokens},
	}
	if len(request.JSONSchema) > 0 {
		body.Format = request.JSONSchema
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return application.GenerationResponse{}, fmt.Errorf("encode Ollama request: %w", err)
	}
	endpoint := *c.baseURL
	endpoint.Path += "/api/chat"
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(encoded))
	if err != nil {
		return application.GenerationResponse{}, fmt.Errorf("create Ollama request: %w", err)
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	response, err := c.http.Do(httpRequest)
	if err != nil {
		return application.GenerationResponse{}, fmt.Errorf("call Ollama: %w", err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return application.GenerationResponse{}, fmt.Errorf("read Ollama response: %w", err)
	}
	if len(data) > maxResponseBytes {
		return application.GenerationResponse{}, fmt.Errorf("ollama response exceeds %d bytes", maxResponseBytes)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		message := strings.TrimSpace(string(data))
		if len(message) > 4096 {
			message = message[:4096]
		}
		return application.GenerationResponse{}, fmt.Errorf("ollama returned HTTP %d: %s", response.StatusCode, message)
	}
	var decoded chatResponse
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		return application.GenerationResponse{}, fmt.Errorf("decode Ollama response: %w", err)
	}
	return application.GenerationResponse{
		Text: decoded.Message.Content, FinishReason: decoded.DoneReason,
		Usage: application.Usage{
			InputTokens: decoded.PromptEvalCount, OutputTokens: decoded.EvalCount,
			TotalTokens: decoded.PromptEvalCount + decoded.EvalCount,
		},
	}, nil
}

type chatRequest struct {
	Model    string                `json:"model"`
	Messages []application.Message `json:"messages"`
	Stream   bool                  `json:"stream"`
	Think    *bool                 `json:"think,omitempty"`
	Format   json.RawMessage       `json:"format,omitempty"`
	Options  chatOptions           `json:"options"`
}

type chatOptions struct {
	Temperature float64 `json:"temperature"`
	NumPredict  int     `json:"num_predict,omitempty"`
}

type chatResponse struct {
	Model   string `json:"model"`
	Created string `json:"created_at"`
	Message struct {
		Role      string `json:"role"`
		Content   string `json:"content"`
		Thinking  string `json:"thinking,omitempty"`
		Images    []any  `json:"images,omitempty"`
		ToolCalls []any  `json:"tool_calls,omitempty"`
	} `json:"message"`
	Done               bool   `json:"done"`
	DoneReason         string `json:"done_reason"`
	TotalDuration      int64  `json:"total_duration"`
	LoadDuration       int64  `json:"load_duration"`
	PromptEvalCount    int    `json:"prompt_eval_count"`
	PromptEvalDuration int64  `json:"prompt_eval_duration"`
	EvalCount          int    `json:"eval_count"`
	EvalDuration       int64  `json:"eval_duration"`
}
