package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/qingbo1011/memxplore/internal/application"
	"github.com/qingbo1011/memxplore/internal/auth"
	"github.com/qingbo1011/memxplore/internal/buildinfo"
	"github.com/qingbo1011/memxplore/internal/domain"
)

const (
	// MCPLatestVersion is the stateless MCP revision implemented by MemXplore.
	MCPLatestVersion = "2026-07-28"
	mcpLegacyVersion = "2025-11-25"
)

// MCPRequest is one JSON-RPC 2.0 request or notification. MCP batching is intentionally unsupported.
type MCPRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// MCPResponse is one JSON-RPC 2.0 response.
type MCPResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *MCPError       `json:"error,omitempty"`
}

// MCPError is a JSON-RPC protocol error.
type MCPError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

type mcpTool struct {
	Name         string         `json:"name"`
	Title        string         `json:"title,omitempty"`
	Description  string         `json:"description"`
	InputSchema  map[string]any `json:"inputSchema"`
	OutputSchema map[string]any `json:"outputSchema,omitempty"`
	Annotations  map[string]any `json:"annotations,omitempty"`
}

type mcpToolCall struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
	Meta      json.RawMessage `json:"_meta,omitempty"`
}

type mcpText struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type mcpToolResult struct {
	Content           []mcpText      `json:"content"`
	StructuredContent map[string]any `json:"structuredContent,omitempty"`
	IsError           bool           `json:"isError,omitempty"`
}

func (s *Server) mcpHTTP(writer http.ResponseWriter, request *http.Request) {
	var input MCPRequest
	if !decodeRequest(writer, request, &input) {
		return
	}
	if version := request.Header.Get("MCP-Protocol-Version"); version != "" && version != MCPLatestVersion {
		writeMCP(writer, http.StatusBadRequest, mcpFailure(input.ID, -32022, "Unsupported protocol version"))
		return
	}
	if request.Header.Get("MCP-Protocol-Version") == MCPLatestVersion {
		if request.Header.Get("Mcp-Method") != input.Method {
			writeMCP(writer, http.StatusBadRequest, mcpFailure(input.ID, -32600, "Mcp-Method header does not match request"))
			return
		}
		if input.Method == "tools/call" {
			var call mcpToolCall
			if json.Unmarshal(input.Params, &call) != nil || request.Header.Get("Mcp-Name") != call.Name {
				writeMCP(writer, http.StatusBadRequest, mcpFailure(input.ID, -32600, "Mcp-Name header does not match request"))
				return
			}
		}
	}
	response := s.handleMCP(request.Context(), principalFrom(request.Context()), input)
	if response == nil {
		writer.WriteHeader(http.StatusAccepted)
		return
	}
	writeMCP(writer, http.StatusOK, response)
}

// ServeMCPStdio serves newline-delimited JSON-RPC over stdin/stdout without logging to stdout.
func (s *Server) ServeMCPStdio(ctx context.Context, input io.Reader, output io.Writer, principal auth.Principal) error {
	decoder := json.NewDecoder(input)
	encoder := json.NewEncoder(output)
	for {
		var request MCPRequest
		if err := decoder.Decode(&request); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			if encodeErr := encoder.Encode(mcpFailure(nil, -32700, "Parse error")); encodeErr != nil {
				return encodeErr
			}
			return fmt.Errorf("decode MCP request: %w", err)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		response := s.handleMCP(ctx, principal, request)
		if response != nil {
			if err := encoder.Encode(response); err != nil {
				return fmt.Errorf("encode MCP response: %w", err)
			}
		}
	}
}

func (s *Server) handleMCP(ctx context.Context, principal auth.Principal, request MCPRequest) *MCPResponse {
	if request.JSONRPC != "2.0" || request.Method == "" || string(request.ID) == "null" {
		return mcpFailure(request.ID, -32600, "Invalid Request")
	}
	if len(request.ID) == 0 {
		// MCP notifications never receive responses, including unknown notifications.
		return nil
	}
	switch request.Method {
	case "server/discover":
		return mcpSuccess(request.ID, map[string]any{
			"resultType": "complete", "supportedVersions": []string{MCPLatestVersion, mcpLegacyVersion, "2025-06-18"},
			"capabilities": map[string]any{"tools": map[string]any{}},
			"serverInfo":   map[string]any{"name": "memxplore", "version": buildinfo.Version},
			"instructions": "Recall returns structured evidence, not a generated answer. Remember is durable and may return a queued job.",
		})
	case "initialize":
		var params struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		if err := json.Unmarshal(request.Params, &params); err != nil || params.ProtocolVersion == "" {
			return mcpFailure(request.ID, -32602, "Invalid initialize parameters")
		}
		version := params.ProtocolVersion
		if version != mcpLegacyVersion && version != "2025-06-18" {
			version = mcpLegacyVersion
		}
		return mcpSuccess(request.ID, map[string]any{
			"protocolVersion": version, "capabilities": map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":   map[string]any{"name": "memxplore", "version": buildinfo.Version},
			"instructions": "Recall returns structured evidence, not a generated answer.",
		})
	case "ping":
		return mcpSuccess(request.ID, map[string]any{})
	case "tools/list":
		return mcpSuccess(request.ID, map[string]any{
			"tools": mcpTools(), "ttlMs": 300000, "cacheScope": "global",
		})
	case "tools/call":
		return s.callMCPTool(ctx, principal, request.ID, request.Params)
	default:
		return mcpFailure(request.ID, -32601, "Method not found")
	}
}

func mcpTools() []mcpTool {
	stringProperty := func(description string) map[string]any {
		return map[string]any{"type": "string", "description": description}
	}
	return []mcpTool{
		{
			Name: "memxplore_job_status", Title: "Get memory job status",
			Description: "Read the durable state and result of a MemXplore formation job.",
			InputSchema: map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{
				"id": stringProperty("Durable job identifier returned by memxplore_remember."),
			}, "required": []string{"id"}},
			Annotations: map[string]any{"readOnlyHint": true, "destructiveHint": false, "idempotentHint": true, "openWorldHint": false},
		},
		{
			Name: "memxplore_recall", Title: "Recall memory evidence",
			Description: "Return a token-budgeted RecallBundle of provenance-bearing evidence. This never generates an answer.",
			InputSchema: map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{
				"owner": stringProperty("Authorized memory owner."), "subject": stringProperty("Data subject."),
				"context": stringProperty("Optional context or task identifier."), "query": stringProperty("Retrieval query."),
				"mode":            map[string]any{"type": "string", "enum": []string{"auto", "lexical", "semantic", "hybrid"}},
				"token_budget":    map[string]any{"type": "integer", "minimum": 1},
				"candidate_limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 250},
			}, "required": []string{"owner", "subject", "query"}},
			Annotations: map[string]any{"readOnlyHint": true, "destructiveHint": false, "idempotentHint": true, "openWorldHint": false},
		},
		{
			Name: "memxplore_remember", Title: "Remember evidence",
			Description: "Capture one observation and enqueue durable factual, experiential, or working-memory formation.",
			InputSchema: map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{
				"idempotency_key": stringProperty("Caller-stable retry key."), "owner": stringProperty("Authorized memory owner."),
				"subject": stringProperty("Data subject."), "context": stringProperty("Context; required for working memory."),
				"source_kind":       stringProperty("Evidence source kind."),
				"content":           map[string]any{"type": "object", "properties": map[string]any{"parts": map[string]any{"type": "array"}}, "required": []string{"parts"}},
				"function":          map[string]any{"type": "string", "enum": []string{"factual", "experiential", "working"}},
				"strategy":          map[string]any{"type": "string", "enum": []string{"generator-free", "assisted"}},
				"wait_milliseconds": map[string]any{"type": "integer", "minimum": 0, "maximum": 30000},
			}, "required": []string{"idempotency_key", "owner", "subject", "content", "function"}},
			Annotations: map[string]any{"readOnlyHint": false, "destructiveHint": false, "idempotentHint": true, "openWorldHint": false},
		},
	}
}

func (s *Server) callMCPTool(ctx context.Context, principal auth.Principal, id, params json.RawMessage) *MCPResponse {
	var call mcpToolCall
	if err := strictUnmarshal(params, &call); err != nil || call.Name == "" {
		return mcpFailure(id, -32602, "Invalid tool call parameters")
	}
	var value any
	var err error
	switch call.Name {
	case "memxplore_job_status":
		if !principal.HasScope(auth.ScopeMemoryRead) {
			err = fmt.Errorf("memory read scope is required")
			break
		}
		var input struct {
			ID domain.ID `json:"id"`
		}
		if err = strictUnmarshal(call.Arguments, &input); err == nil {
			var job application.Job
			job, err = s.config.Store.Get(ctx, input.ID)
			if err == nil && job.Namespace != principal.Namespace {
				err = application.ErrJobNotFound
			}
			value = map[string]any{"job": job}
		}
	case "memxplore_recall":
		if !principal.HasScope(auth.ScopeMemoryRead) {
			err = fmt.Errorf("memory read scope is required")
			break
		}
		var input RecallRequest
		if err = strictUnmarshal(call.Arguments, &input); err == nil {
			value, err = s.recallValue(ctx, principal, input)
		}
	case "memxplore_remember":
		if !principal.HasScope(auth.ScopeMemoryWrite) {
			err = fmt.Errorf("memory write scope is required")
			break
		}
		var input RememberRequest
		if err = strictUnmarshal(call.Arguments, &input); err == nil {
			value, err = s.rememberValue(ctx, principal, input)
		}
	default:
		return mcpFailure(id, -32602, "Unknown tool: "+call.Name)
	}
	if err != nil {
		return mcpSuccess(id, mcpToolResult{Content: []mcpText{{Type: "text", Text: err.Error()}}, IsError: true})
	}
	structured, encoded, err := mcpStructured(value)
	if err != nil {
		return mcpFailure(id, -32603, "Internal error")
	}
	return mcpSuccess(id, mcpToolResult{
		Content: []mcpText{{Type: "text", Text: string(encoded)}}, StructuredContent: structured,
	})
}

func (s *Server) rememberValue(ctx context.Context, principal auth.Principal, input RememberRequest) (RememberResponse, error) {
	if input.WaitMilliseconds < 0 || input.WaitMilliseconds > 30000 {
		return RememberResponse{}, fmt.Errorf("wait_milliseconds must be between 0 and 30000")
	}
	observation, job, err := s.prepareRemember(principal, input)
	if err != nil {
		return RememberResponse{}, err
	}
	created, _, err := s.config.Store.EnqueueObservation(ctx, observation, job)
	if err != nil {
		return RememberResponse{}, err
	}
	s.config.Worker.Notify()
	created, err = s.waitForJob(ctx, created, input.WaitMilliseconds)
	if err != nil {
		return RememberResponse{}, err
	}
	return RememberResponse{Job: created}, nil
}

func (s *Server) waitForJob(ctx context.Context, job application.Job, milliseconds int) (application.Job, error) {
	if milliseconds == 0 {
		return job, nil
	}
	waitCtx, cancel := context.WithTimeout(ctx, time.Duration(milliseconds)*time.Millisecond)
	defer cancel()
	terminal, err := s.config.Store.Wait(waitCtx, job.ID, 10*time.Millisecond)
	if err == nil {
		return terminal, nil
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return job, nil
	}
	return application.Job{}, err
}

func (s *Server) recallValue(ctx context.Context, principal auth.Principal, input RecallRequest) (application.RecallBundle, error) {
	scope, err := principal.DomainScope(input.Owner, input.Subject, input.Context, domain.VisibilityPrivate)
	if err != nil {
		return application.RecallBundle{}, err
	}
	now := s.config.Now().UTC()
	validAt, systemAt := now, now
	if input.ValidAt != nil {
		validAt = input.ValidAt.UTC()
	}
	if input.SystemAt != nil {
		systemAt = input.SystemAt.UTC()
	}
	if input.TokenBudget == 0 {
		input.TokenBudget = 2048
	}
	if input.CandidateLimit == 0 {
		input.CandidateLimit = 20
	}
	return s.config.Retriever.Recall(ctx, application.RecallRequest{
		Scope: scope, Access: principal.AccessScope(), Query: input.Query, Functions: input.Functions,
		Mode: input.Mode, ValidAt: validAt, SystemAt: systemAt, TokenBudget: input.TokenBudget,
		CandidateLimit: input.CandidateLimit, IncludeGlobalWorking: input.IncludeGlobalWorking,
	})
}

func strictUnmarshal(value []byte, target any) error {
	if len(value) == 0 {
		value = []byte(`{}`)
	}
	decoder := json.NewDecoder(strings.NewReader(string(value)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("expected one JSON value")
	}
	return nil
}

func mcpStructured(value any) (map[string]any, []byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, nil, err
	}
	var structured map[string]any
	if err := json.Unmarshal(encoded, &structured); err != nil {
		return nil, nil, err
	}
	return structured, encoded, nil
}

func mcpSuccess(id json.RawMessage, result any) *MCPResponse {
	return &MCPResponse{JSONRPC: "2.0", ID: id, Result: result}
}

func mcpFailure(id json.RawMessage, code int, message string) *MCPResponse {
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	return &MCPResponse{JSONRPC: "2.0", ID: id, Error: &MCPError{Code: code, Message: message}}
}

func writeMCP(writer http.ResponseWriter, status int, response *MCPResponse) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(response)
}
