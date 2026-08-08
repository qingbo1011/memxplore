package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/qingbo1011/memxplore/internal/application"
	"github.com/qingbo1011/memxplore/internal/auth"
	"github.com/qingbo1011/memxplore/internal/domain"
)

func TestMCPLatestDiscoveryAndRequiredRoutingHeaders(t *testing.T) {
	server, _, _ := testServer(t, false)
	body := `{"jsonrpc":"2.0","id":1,"method":"server/discover","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`
	req := httptest.NewRequest(http.MethodPost, "/v1/mcp", bytes.NewBufferString(body))
	req.RemoteAddr = "127.0.0.1:4242"
	req.Header.Set("MCP-Protocol-Version", MCPLatestVersion)
	req.Header.Set("Mcp-Method", "server/discover")
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK || !bytes.Contains(recorder.Body.Bytes(), []byte(MCPLatestVersion)) {
		t.Fatalf("discover status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	req = httptest.NewRequest(http.MethodPost, "/v1/mcp", bytes.NewBufferString(body))
	req.RemoteAddr = "127.0.0.1:4242"
	req.Header.Set("MCP-Protocol-Version", MCPLatestVersion)
	req.Header.Set("Mcp-Method", "tools/list")
	recorder = httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, req)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("mismatched method status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestMCPLegacyInitializeAndDeterministicTools(t *testing.T) {
	server, _, _ := testServer(t, false)
	input := "" +
		`{"jsonrpc":"2.0","id":"init","method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}` + "\n" +
		`{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n" +
		`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}` + "\n"
	var output bytes.Buffer
	principal := auth.Principal{PrincipalID: "local-actor", Namespace: "local", PrivateOwners: []domain.ID{"owner-a"}, Scopes: []auth.Scope{auth.ScopeMemoryRead}}
	if err := server.ServeMCPStdio(context.Background(), bytes.NewBufferString(input), &output, principal); err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(&output)
	var initialize, list MCPResponse
	if err := decoder.Decode(&initialize); err != nil {
		t.Fatal(err)
	}
	if err := decoder.Decode(&list); err != nil {
		t.Fatal(err)
	}
	if initialize.Error != nil || list.Error != nil {
		t.Fatalf("initialize=%+v list=%+v", initialize, list)
	}
	result := list.Result.(map[string]any)
	tools := result["tools"].([]any)
	names := []string{
		tools[0].(map[string]any)["name"].(string),
		tools[1].(map[string]any)["name"].(string),
		tools[2].(map[string]any)["name"].(string),
	}
	if names[0] != "memxplore_job_status" || names[1] != "memxplore_recall" || names[2] != "memxplore_remember" {
		t.Fatalf("tools not deterministic: %v", names)
	}
}

func TestMCPRememberThenRecallStructuredEvidence(t *testing.T) {
	server, _, _ := testServer(t, false)
	principal := server.config.LoopbackPrincipal
	rememberArguments := RememberRequest{
		IdempotencyKey: "mcp-idem", Owner: "owner-a", Subject: "subject-a", Context: "context-a",
		SourceKind: "mcp", Function: "factual", Strategy: "generator-free", WaitMilliseconds: 1000,
		Content: TextContent("MCP stores durable evidence"),
	}
	arguments, _ := json.Marshal(rememberArguments)
	params, _ := json.Marshal(mcpToolCall{Name: "memxplore_remember", Arguments: arguments})
	response := server.handleMCP(context.Background(), principal, MCPRequest{JSONRPC: "2.0", ID: json.RawMessage("1"), Method: "tools/call", Params: params})
	if response.Error != nil {
		t.Fatalf("remember error=%+v", response.Error)
	}
	result := response.Result.(mcpToolResult)
	jobValue := result.StructuredContent["job"].(map[string]any)
	if jobValue["state"] != string(application.JobSucceeded) {
		t.Fatalf("job=%+v", jobValue)
	}
	recallArguments, _ := json.Marshal(RecallRequest{
		Owner: "owner-a", Subject: "subject-a", Context: "context-a", Query: "durable evidence",
		Mode: application.RetrievalLexical, TokenBudget: 256, CandidateLimit: 10,
	})
	params, _ = json.Marshal(mcpToolCall{Name: "memxplore_recall", Arguments: recallArguments})
	response = server.handleMCP(context.Background(), principal, MCPRequest{JSONRPC: "2.0", ID: json.RawMessage("2"), Method: "tools/call", Params: params})
	result = response.Result.(mcpToolResult)
	items := result.StructuredContent["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("recall result=%+v", result.StructuredContent)
	}
}
