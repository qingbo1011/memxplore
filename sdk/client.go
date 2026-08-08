package sdk

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const maxResponseBytes = 16 << 20

// Client calls one MemXplore daemon using protocol v1.
type Client struct {
	baseURL *url.URL
	token   string
	http    *http.Client
}

// Option customizes a Client.
type Option func(*Client)

// WithBearerToken authenticates daemon requests.
func WithBearerToken(token string) Option { return func(client *Client) { client.token = token } }

// WithHTTPClient supplies a transport, useful for custom TLS and tests.
func WithHTTPClient(client *http.Client) Option { return func(target *Client) { target.http = client } }

// NewClient validates the daemon base URL. The default HTTP client has no implicit mutation or retry policy.
func NewClient(baseURL string, options ...Option) (*Client, error) {
	parsed, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, fmt.Errorf("valid http or https MemXplore base URL is required")
	}
	client := &Client{baseURL: parsed, http: http.DefaultClient}
	for _, option := range options {
		option(client)
	}
	if client.http == nil {
		return nil, fmt.Errorf("HTTP client cannot be nil")
	}
	return client, nil
}

// APIError reports a stable HTTP error code and status.
type APIError struct {
	StatusCode int
	Code       string
	Message    string
}

func (e *APIError) Error() string {
	if e.Code == "" {
		return fmt.Sprintf("MemXplore HTTP %d: %s", e.StatusCode, e.Message)
	}
	return fmt.Sprintf("MemXplore HTTP %d (%s): %s", e.StatusCode, e.Code, e.Message)
}

// Health verifies the unauthenticated liveness route.
func (c *Client) Health(ctx context.Context) error {
	var output struct {
		Status string `json:"status"`
	}
	if err := c.do(ctx, http.MethodGet, "/v1/health", nil, &output); err != nil {
		return err
	}
	if output.Status != "ok" {
		return fmt.Errorf("MemXplore health status %q", output.Status)
	}
	return nil
}

// Version returns compatibility metadata.
func (c *Client) Version(ctx context.Context) (Version, error) {
	var output Version
	err := c.do(ctx, http.MethodGet, "/v1/version", nil, &output)
	return output, err
}

// Remember captures evidence and returns durable job state.
func (c *Client) Remember(ctx context.Context, input RememberRequest) (RememberResponse, error) {
	var output RememberResponse
	err := c.do(ctx, http.MethodPost, "/v1/remember", input, &output)
	return output, err
}

// Recall returns a RecallBundle of evidence.
func (c *Client) Recall(ctx context.Context, input RecallRequest) (RecallBundle, error) {
	var output RecallBundle
	err := c.do(ctx, http.MethodPost, "/v1/recall", input, &output)
	return output, err
}

// Job returns durable job state.
func (c *Client) Job(ctx context.Context, id ID) (Job, error) {
	var output Job
	err := c.do(ctx, http.MethodGet, "/v1/jobs/"+url.PathEscape(string(id)), nil, &output)
	return output, err
}

// Archive removes a memory from normal recall while retaining history.
func (c *Client) Archive(ctx context.Context, id ID) (json.RawMessage, error) {
	return c.rawAction(ctx, http.MethodPost, "/v1/memories/"+url.PathEscape(string(id))+"/archive", nil)
}

// Forget applies policy-aware logical forgetting while retaining audit history.
func (c *Client) Forget(ctx context.Context, id ID) (json.RawMessage, error) {
	return c.rawAction(ctx, http.MethodPost, "/v1/memories/"+url.PathEscape(string(id))+"/forget", nil)
}

// Purge irreversibly removes one memory and transitively derived content.
func (c *Client) Purge(ctx context.Context, id ID) (PurgeReceipt, error) {
	var output PurgeReceipt
	err := c.do(ctx, http.MethodDelete, "/v1/memories/"+url.PathEscape(string(id)), nil, &output)
	return output, err
}

// IngestAgentEvent opts a generic event into durable formation.
func (c *Client) IngestAgentEvent(ctx context.Context, input AgentEventRequest) (RememberResponse, error) {
	var output RememberResponse
	err := c.do(ctx, http.MethodPost, "/v1/agent-events", input, &output)
	return output, err
}

func (c *Client) rawAction(ctx context.Context, method, path string, input any) (json.RawMessage, error) {
	var output json.RawMessage
	err := c.do(ctx, method, path, input, &output)
	return output, err
}

func (c *Client) do(ctx context.Context, method, path string, input, output any) error {
	var body io.Reader
	if input != nil {
		var encoded bytes.Buffer
		if err := json.NewEncoder(&encoded).Encode(input); err != nil {
			return fmt.Errorf("encode request: %w", err)
		}
		body = &encoded
	}
	target := *c.baseURL
	target.Path = strings.TrimRight(target.Path, "/") + path
	request, err := http.NewRequestWithContext(ctx, method, target.String(), body)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		request.Header.Set("Authorization", "Bearer "+c.token)
	}
	response, err := c.http.Do(request)
	if err != nil {
		return fmt.Errorf("call MemXplore: %w", err)
	}
	defer response.Body.Close()
	limited := io.LimitReader(response.Body, maxResponseBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return fmt.Errorf("read MemXplore response: %w", err)
	}
	if len(data) > maxResponseBytes {
		return fmt.Errorf("MemXplore response exceeds %d bytes", maxResponseBytes)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var envelope struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(data, &envelope)
		message := envelope.Error.Message
		if message == "" {
			message = strings.TrimSpace(string(data))
		}
		return &APIError{StatusCode: response.StatusCode, Code: envelope.Error.Code, Message: message}
	}
	if output == nil || len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, output); err != nil {
		return fmt.Errorf("decode MemXplore response: %w", err)
	}
	return nil
}
