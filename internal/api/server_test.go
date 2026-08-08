package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/qingbo1011/memxplore/internal/adapters/sqlite"
	"github.com/qingbo1011/memxplore/internal/agentevent"
	"github.com/qingbo1011/memxplore/internal/application"
	"github.com/qingbo1011/memxplore/internal/auth"
	"github.com/qingbo1011/memxplore/internal/daemon"
	"github.com/qingbo1011/memxplore/internal/domain"
	"github.com/qingbo1011/memxplore/internal/observability"
)

func testServer(t *testing.T, enableAgentEvents bool) (*Server, *sqlite.Store, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	store, err := sqlite.Open(ctx, t.TempDir()+"/api.sqlite", sqlite.DefaultOptions())
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 8, 10, 0, 0, 0, time.UTC)
	retriever, err := application.NewRetriever(application.RetrieverConfig{
		Repository: store, TraceSink: store, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	worker, err := daemon.NewFormationWorker(daemon.FormationConfig{
		Store: store, PollInterval: time.Millisecond, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(Config{
		Store: store, Retriever: retriever, Worker: worker,
		LoopbackPrincipal: auth.Principal{
			PrincipalID: "local-actor", Namespace: "local", PrivateOwners: []domain.ID{"owner-a"},
			Scopes: []auth.Scope{auth.ScopeMemoryRead, auth.ScopeMemoryWrite, auth.ScopeMemoryPurge, auth.ScopeAdmin},
		},
		AllowLoopbackWithoutToken: true, EnableAgentEvents: enableAgentEvents,
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = worker.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		_ = store.Close()
	})
	return server, store, cancel
}

func request(t *testing.T, handler http.Handler, method, path, remote, bearer string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var encoded bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&encoded).Encode(body); err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(method, path, &encoded)
	req.RemoteAddr = remote
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	return recorder
}

func TestRememberRecallAndJobOverLoopback(t *testing.T) {
	server, _, _ := testServer(t, false)
	remember := RememberRequest{
		IdempotencyKey: "idem-one", Owner: "owner-a", Subject: "subject-a", Context: "context-a",
		SourceKind: "test", Function: domain.FunctionFactual, Strategy: "generator-free",
		Content:          domain.Content{Parts: []domain.ContentPart{{Kind: domain.PartText, Text: "Ada prefers concise release notes"}}},
		WaitMilliseconds: 1000,
	}
	created := request(t, server.Handler(), http.MethodPost, "/v1/remember", "127.0.0.1:4242", "", remember)
	if created.Code != http.StatusOK {
		t.Fatalf("remember status=%d body=%s", created.Code, created.Body.String())
	}
	var response RememberResponse
	if err := json.Unmarshal(created.Body.Bytes(), &response); err != nil || response.Job.State != application.JobSucceeded {
		t.Fatalf("job=%+v err=%v", response.Job, err)
	}
	job := request(t, server.Handler(), http.MethodGet, "/v1/jobs/"+string(response.Job.ID), "127.0.0.1:4242", "", nil)
	if job.Code != http.StatusOK {
		t.Fatalf("job status=%d body=%s", job.Code, job.Body.String())
	}
	recalled := request(t, server.Handler(), http.MethodPost, "/v1/recall", "127.0.0.1:4242", "", RecallRequest{
		Owner: "owner-a", Subject: "subject-a", Context: "context-a", Query: "concise release notes",
		Mode: application.RetrievalLexical, TokenBudget: 256, CandidateLimit: 10,
	})
	if recalled.Code != http.StatusOK {
		t.Fatalf("recall status=%d body=%s", recalled.Code, recalled.Body.String())
	}
	var bundle application.RecallBundle
	if err := json.Unmarshal(recalled.Body.Bytes(), &bundle); err != nil || len(bundle.Items) != 1 {
		t.Fatalf("items=%d err=%v body=%s", len(bundle.Items), err, recalled.Body.String())
	}
}

func TestBearerAuthenticationAndScope(t *testing.T) {
	server, store, _ := testServer(t, false)
	now := time.Date(2026, 8, 8, 10, 0, 0, 0, time.UTC)
	token, err := store.CreateAPIToken(context.Background(), auth.TokenSpec{
		ID: "token-reader", PrincipalID: "reader", Namespace: "local", PrivateOwners: []domain.ID{"owner-a"},
		Scopes: []auth.Scope{auth.ScopeMemoryRead}, CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	missing := request(t, server.Handler(), http.MethodGet, "/v1/version", "192.0.2.10:4242", "", nil)
	if missing.Code != http.StatusUnauthorized {
		t.Fatalf("missing token status=%d", missing.Code)
	}
	allowed := request(t, server.Handler(), http.MethodGet, "/v1/version", "192.0.2.10:4242", token, nil)
	if allowed.Code != http.StatusOK {
		t.Fatalf("reader status=%d body=%s", allowed.Code, allowed.Body.String())
	}
	denied := request(t, server.Handler(), http.MethodPost, "/v1/remember", "192.0.2.10:4242", token, RememberRequest{})
	if denied.Code != http.StatusForbidden {
		t.Fatalf("write with read token status=%d body=%s", denied.Code, denied.Body.String())
	}
}

func TestStrictJSONWaitValidationAndAgentEventOptIn(t *testing.T) {
	server, _, _ := testServer(t, false)
	raw := bytes.NewBufferString(`{"owner":"owner-a","unknown":true}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/recall", raw)
	req.RemoteAddr = "127.0.0.1:4242"
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, req)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("unknown field status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	invalidWait := request(t, server.Handler(), http.MethodPost, "/v1/remember", "127.0.0.1:4242", "", RememberRequest{WaitMilliseconds: 30001})
	if invalidWait.Code != http.StatusBadRequest {
		t.Fatalf("invalid wait status=%d body=%s", invalidWait.Code, invalidWait.Body.String())
	}
	disabled := request(t, server.Handler(), http.MethodPost, "/v1/agent-events", "127.0.0.1:4242", "", AgentEventRequest{})
	if disabled.Code != http.StatusForbidden {
		t.Fatalf("agent event status=%d body=%s", disabled.Code, disabled.Body.String())
	}
}

func TestAgentEventReplayReturnsSameDurableJob(t *testing.T) {
	server, _, _ := testServer(t, true)
	input := AgentEventRequest{
		Event: agentevent.Event{
			SchemaVersion: agentevent.SchemaV1, ID: "event-a", Source: "codex", Type: agentevent.EventMessage,
			Owner: "owner-a", Subject: "subject-a", Context: "context-a", Content: TextContent("event evidence"),
			OccurredAt: time.Date(2026, 8, 8, 9, 0, 0, 0, time.UTC),
		},
		Function: domain.FunctionFactual, Strategy: "generator-free", WaitMilliseconds: 1000,
	}
	first := request(t, server.Handler(), http.MethodPost, "/v1/agent-events", "127.0.0.1:4242", "", input)
	second := request(t, server.Handler(), http.MethodPost, "/v1/agent-events", "127.0.0.1:4242", "", input)
	if first.Code != http.StatusOK || second.Code != http.StatusOK {
		t.Fatalf("first=%d %s second=%d %s", first.Code, first.Body.String(), second.Code, second.Body.String())
	}
	var firstResponse, secondResponse RememberResponse
	if err := json.Unmarshal(first.Body.Bytes(), &firstResponse); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(second.Body.Bytes(), &secondResponse); err != nil {
		t.Fatal(err)
	}
	if firstResponse.Job.ID == "" || firstResponse.Job.ID != secondResponse.Job.ID {
		t.Fatalf("first job=%s second job=%s", firstResponse.Job.ID, secondResponse.Job.ID)
	}
}

func TestHTTPObservabilityUsesRoutePatternWithoutRequestContent(t *testing.T) {
	recorder := &recordingObserver{}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/jobs/{id}", func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	})
	handler := observeHTTP(recorder, mux)
	request := httptest.NewRequest(http.MethodGet, "/v1/jobs/private-job-id?token=secret", nil)
	handler.ServeHTTP(httptest.NewRecorder(), request)
	joined := recorder.joinedAttributes()
	if !strings.Contains(joined, "http.route=GET /v1/jobs/{id}") || !strings.Contains(joined, "http.response.status_code=204") {
		t.Fatalf("attributes=%s", joined)
	}
	if strings.Contains(joined, "private-job-id") || strings.Contains(joined, "secret") {
		t.Fatalf("request content leaked into telemetry: %s", joined)
	}
}

type recordingObserver struct {
	attributes []observability.Attribute
}

func (r *recordingObserver) Start(ctx context.Context, _ string, attributes ...observability.Attribute) (context.Context, observability.EndOperation) {
	r.attributes = append(r.attributes, attributes...)
	return ctx, func(_ error, final ...observability.Attribute) {
		r.attributes = append(r.attributes, final...)
	}
}

func (*recordingObserver) Observe(context.Context, string, float64, ...observability.Attribute) {}

func (r *recordingObserver) joinedAttributes() string {
	values := make([]string, len(r.attributes))
	for index, attribute := range r.attributes {
		values[index] = attribute.Key + "=" + attribute.Value
	}
	return strings.Join(values, "\n")
}
