package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/qingbo1011/memxplore/internal/adapters/sqlite"
	"github.com/qingbo1011/memxplore/internal/application"
	"github.com/qingbo1011/memxplore/internal/auth"
	"github.com/qingbo1011/memxplore/internal/buildinfo"
	"github.com/qingbo1011/memxplore/internal/daemon"
	"github.com/qingbo1011/memxplore/internal/domain"
	"github.com/qingbo1011/memxplore/internal/observability"
	"github.com/qingbo1011/memxplore/internal/policy"
)

const maxRequestBytes = 4 << 20

type principalContextKey struct{}

// Config contains transport policy and application dependencies.
type Config struct {
	Store                     *sqlite.Store
	Retriever                 *application.Retriever
	Worker                    *daemon.FormationWorker
	LoopbackPrincipal         auth.Principal
	AllowLoopbackWithoutToken bool
	EnableAgentEvents         bool
	Now                       func() time.Time
	Observability             observability.Recorder
}

// Server implements versioned REST and JSON-RPC MCP handlers.
type Server struct {
	config  Config
	handler http.Handler
}

// NewServer validates dependencies and builds an exact-method router.
func NewServer(config Config) (*Server, error) {
	if config.Store == nil || config.Retriever == nil || config.Worker == nil {
		return nil, fmt.Errorf("API store, retriever, and durable worker are required")
	}
	if config.Now == nil {
		config.Now = func() time.Time { return time.Now().UTC() }
	}
	config.Observability = observability.OrNop(config.Observability)
	server := &Server{config: config}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/health", server.health)
	mux.Handle("GET /v1/version", server.authorize(auth.ScopeMemoryRead, http.HandlerFunc(server.version)))
	mux.Handle("POST /v1/remember", server.authorize(auth.ScopeMemoryWrite, http.HandlerFunc(server.remember)))
	mux.Handle("POST /v1/recall", server.authorize(auth.ScopeMemoryRead, http.HandlerFunc(server.recall)))
	mux.Handle("GET /v1/jobs/{id}", server.authorize(auth.ScopeMemoryRead, http.HandlerFunc(server.job)))
	mux.Handle("POST /v1/memories/{id}/archive", server.authorize(auth.ScopeMemoryWrite, http.HandlerFunc(server.archive)))
	mux.Handle("POST /v1/memories/{id}/forget", server.authorize(auth.ScopeMemoryWrite, http.HandlerFunc(server.forget)))
	mux.Handle("DELETE /v1/memories/{id}", server.authorize(auth.ScopeMemoryPurge, http.HandlerFunc(server.purge)))
	mux.Handle("POST /v1/tokens", server.authorize(auth.ScopeAdmin, http.HandlerFunc(server.createToken)))
	mux.Handle("POST /v1/agent-events", server.authorize(auth.ScopeMemoryWrite, http.HandlerFunc(server.agentEvent)))
	mux.Handle("POST /v1/mcp", server.authorizeAny(http.HandlerFunc(server.mcpHTTP)))
	server.handler = observeHTTP(config.Observability, securityHeaders(mux))
	return server, nil
}

// Handler returns the complete v1 HTTP adapter.
func (s *Server) Handler() http.Handler { return s.handler }

func (s *Server) health(writer http.ResponseWriter, _ *http.Request) {
	writeJSON(writer, http.StatusOK, map[string]any{"status": "ok"})
}

func (s *Server) version(writer http.ResponseWriter, _ *http.Request) {
	writeJSON(writer, http.StatusOK, map[string]any{
		"program": "memxplore", "version": buildinfo.Version, "protocol_version": buildinfo.ProtocolVersion,
		"storage_schema_version": buildinfo.StorageSchemaVersion, "export_schema_version": buildinfo.ExportSchemaVersion,
	})
}

func (s *Server) remember(writer http.ResponseWriter, request *http.Request) {
	var input RememberRequest
	if !decodeRequest(writer, request, &input) {
		return
	}
	if input.WaitMilliseconds < 0 || input.WaitMilliseconds > 30000 {
		writeError(writer, http.StatusBadRequest, "invalid_wait", "wait_milliseconds must be between 0 and 30000")
		return
	}
	result, err := s.rememberValue(request.Context(), principalFrom(request.Context()), input)
	if err != nil {
		writeError(writer, http.StatusBadRequest, "remember_failed", err.Error())
		return
	}
	status := http.StatusAccepted
	if result.Job.State == application.JobSucceeded || result.Job.State == application.JobFailed || result.Job.State == application.JobCanceled {
		status = http.StatusOK
	}
	writeJSON(writer, status, result)
}

func (s *Server) prepareRemember(principal auth.Principal, input RememberRequest) (domain.Observation, application.Job, error) {
	if !principal.HasScope(auth.ScopeMemoryWrite) {
		return domain.Observation{}, application.Job{}, fmt.Errorf("memory write scope is required")
	}
	if input.Visibility == "" {
		input.Visibility = domain.VisibilityPrivate
	}
	scope, err := principal.DomainScope(input.Owner, input.Subject, input.Context, input.Visibility)
	if err != nil {
		return domain.Observation{}, application.Job{}, err
	}
	if input.ObservationID == "" {
		input.ObservationID, err = randomID("obs")
		if err != nil {
			return domain.Observation{}, application.Job{}, err
		}
	}
	if input.IdempotencyKey == "" {
		input.IdempotencyKey = string(input.ObservationID)
	}
	if input.SourceKind == "" {
		input.SourceKind = "api"
	}
	if input.EvidenceClass == "" {
		input.EvidenceClass = domain.EvidenceUntrusted
	}
	if input.EvidenceClass == domain.EvidencePolicy && !principal.HasScope(auth.ScopeAdmin) {
		return domain.Observation{}, application.Job{}, fmt.Errorf("admin scope is required for policy evidence")
	}
	capturedAt := s.config.Now().UTC()
	if input.CapturedAt != nil {
		capturedAt = input.CapturedAt.UTC()
	}
	observation := domain.Observation{
		ID: input.ObservationID, Scope: scope, SourceKind: input.SourceKind,
		SourceReference: input.SourceReference, Content: input.Content,
		EvidenceClass: input.EvidenceClass, PolicyAuthority: input.PolicyAuthority,
		CapturedAt: capturedAt, Metadata: input.Metadata,
	}
	if err := observation.Validate(); err != nil {
		return domain.Observation{}, application.Job{}, err
	}
	if input.Strategy == "" {
		input.Strategy = "generator-free"
	}
	if input.Strategy == "assisted" && !s.config.Worker.SupportsAssisted() {
		return domain.Observation{}, application.Job{}, fmt.Errorf("assisted formation is not enabled")
	}
	jobPayload := application.FormationJobPayload{
		ObservationID: observation.ID, Function: input.Function, Mode: input.Strategy, ApplyScope: scope,
		WorkingGlobalRecall: input.WorkingGlobalRecall,
	}
	if input.Function == domain.FunctionWorking {
		ttl := input.WorkingTTLSeconds
		if ttl == 0 {
			ttl = 86400
		}
		if ttl < 1 || ttl > 30*86400 {
			return domain.Observation{}, application.Job{}, fmt.Errorf("working TTL must be within 1 second and 30 days")
		}
		expires := capturedAt.Add(time.Duration(ttl) * time.Second)
		jobPayload.WorkingExpiresAt = &expires
	}
	encoded, err := application.EncodeFormationJob(jobPayload)
	if err != nil {
		return domain.Observation{}, application.Job{}, err
	}
	jobID, err := randomID("job")
	if err != nil {
		return domain.Observation{}, application.Job{}, err
	}
	job := application.Job{
		ID: jobID, Namespace: principal.Namespace, Kind: "formation." + string(input.Function),
		IdempotencyKey: input.IdempotencyKey, Payload: encoded, AvailableAt: s.config.Now().UTC(),
	}
	return observation, job, nil
}

func (s *Server) recall(writer http.ResponseWriter, request *http.Request) {
	var input RecallRequest
	if !decodeRequest(writer, request, &input) {
		return
	}
	bundle, err := s.recallValue(request.Context(), principalFrom(request.Context()), input)
	if err != nil {
		writeError(writer, http.StatusBadRequest, "recall_failed", err.Error())
		return
	}
	writeJSON(writer, http.StatusOK, bundle)
}

func (s *Server) job(writer http.ResponseWriter, request *http.Request) {
	principal := principalFrom(request.Context())
	job, err := s.config.Store.Get(request.Context(), domain.ID(request.PathValue("id")))
	if err != nil {
		writeError(writer, http.StatusNotFound, "not_found", "job not found")
		return
	}
	if job.Namespace != principal.Namespace {
		writeError(writer, http.StatusNotFound, "not_found", "job not found")
		return
	}
	writeJSON(writer, http.StatusOK, job)
}

func (s *Server) archive(writer http.ResponseWriter, request *http.Request) {
	s.changeState(writer, request, application.ProposalArchive)
}

func (s *Server) forget(writer http.ResponseWriter, request *http.Request) {
	s.changeState(writer, request, application.ProposalForget)
}

func (s *Server) changeState(writer http.ResponseWriter, request *http.Request, kind application.ProposalKind) {
	principal := principalFrom(request.Context())
	memory, _, err := s.config.Store.GetMemory(request.Context(), domain.ID(request.PathValue("id")))
	if err != nil || memory.Scope.Namespace != principal.Namespace || !slices.Contains(principal.PrivateOwners, memory.Scope.Owner) {
		writeError(writer, http.StatusNotFound, "not_found", "memory not found")
		return
	}
	proposalID, _ := randomID("proposal")
	identity := sha256.Sum256([]byte("protocol.lifecycle@1.0.0"))
	proposal := application.Proposal{
		ID: proposalID, Namespace: principal.Namespace, Kind: kind, TargetID: memory.ID,
		Payload: json.RawMessage(`{}`), StrategyID: "protocol.lifecycle@1.0.0",
		StrategyHash: hex.EncodeToString(identity[:]), CreatedAt: s.config.Now().UTC(),
	}
	scope := memory.Scope
	scope.Actor = principal.PrincipalID
	service, _ := application.NewLifecycleService(policy.OwnerPolicy{}, s.config.Store)
	result, err := service.Apply(request.Context(), scope, proposal, s.config.Now().UTC())
	if err != nil {
		writeError(writer, http.StatusConflict, "state_change_failed", err.Error())
		return
	}
	writeJSON(writer, http.StatusOK, result)
}

func (s *Server) purge(writer http.ResponseWriter, request *http.Request) {
	principal := principalFrom(request.Context())
	memory, _, err := s.config.Store.GetMemory(request.Context(), domain.ID(request.PathValue("id")))
	if err != nil || memory.Scope.Namespace != principal.Namespace || !slices.Contains(principal.PrivateOwners, memory.Scope.Owner) {
		writeError(writer, http.StatusNotFound, "not_found", "memory not found")
		return
	}
	receiptID, _ := randomID("purge")
	receipt, err := s.config.Store.PurgeMemory(request.Context(), receiptID, principal.Namespace,
		principal.PrincipalID, memory.ID, s.config.Now().UTC())
	if err != nil {
		writeError(writer, http.StatusConflict, "purge_failed", err.Error())
		return
	}
	writeJSON(writer, http.StatusOK, receipt)
}

func (s *Server) createToken(writer http.ResponseWriter, request *http.Request) {
	var input TokenCreateRequest
	if !decodeRequest(writer, request, &input) {
		return
	}
	principal := principalFrom(request.Context())
	if input.ID == "" {
		input.ID, _ = randomID("token")
	}
	for _, owner := range input.PrivateOwners {
		if !slices.Contains(principal.PrivateOwners, owner) {
			writeError(writer, http.StatusForbidden, "forbidden", "cannot delegate an unauthorized owner")
			return
		}
	}
	spec := auth.TokenSpec{
		ID: input.ID, PrincipalID: input.PrincipalID, Namespace: principal.Namespace,
		PrivateOwners: input.PrivateOwners, Scopes: input.Scopes,
		AllowShared: input.AllowShared, AllowPublic: input.AllowPublic,
		ExpiresAt: input.ExpiresAt, CreatedAt: s.config.Now().UTC(),
	}
	raw, err := s.config.Store.CreateAPIToken(request.Context(), spec)
	if err != nil {
		writeError(writer, http.StatusBadRequest, "token_create_failed", err.Error())
		return
	}
	writeJSON(writer, http.StatusCreated, TokenCreateResponse{ID: spec.ID, Token: raw})
}

func (s *Server) agentEvent(writer http.ResponseWriter, request *http.Request) {
	if !s.config.EnableAgentEvents {
		writeError(writer, http.StatusForbidden, "agent_events_disabled", "AgentEvent ingestion is not enabled")
		return
	}
	var input AgentEventRequest
	if !decodeRequest(writer, request, &input) {
		return
	}
	if input.WaitMilliseconds < 0 || input.WaitMilliseconds > 30000 {
		writeError(writer, http.StatusBadRequest, "invalid_wait", "wait_milliseconds must be between 0 and 30000")
		return
	}
	principal := principalFrom(request.Context())
	if !slices.Contains(principal.PrivateOwners, input.Event.Owner) {
		writeError(writer, http.StatusForbidden, "forbidden", "event owner is not authorized")
		return
	}
	observation, err := input.Event.Observation(principal.Namespace, principal.PrincipalID, domain.VisibilityPrivate)
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_agent_event", err.Error())
		return
	}
	remember := RememberRequest{
		ObservationID: observation.ID, IdempotencyKey: "agent-event:" + input.Event.Source + ":" + string(input.Event.ID),
		Owner: observation.Scope.Owner, Subject: observation.Scope.Subject, Context: observation.Scope.Context,
		Visibility: observation.Scope.Visibility, SourceKind: observation.SourceKind,
		SourceReference: observation.SourceReference, Content: observation.Content,
		EvidenceClass: observation.EvidenceClass, CapturedAt: &observation.CapturedAt,
		Metadata: observation.Metadata, Function: input.Function, Strategy: input.Strategy,
		WaitMilliseconds: input.WaitMilliseconds,
	}
	prepared, job, err := s.prepareRemember(principal, remember)
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_agent_event", err.Error())
		return
	}
	created, _, err := s.config.Store.EnqueueAgentEvent(request.Context(), sqlite.AgentEventReceipt{
		EventID: input.Event.ID, SchemaVersion: input.Event.SchemaVersion, Source: input.Event.Source,
		ReceivedAt: s.config.Now().UTC(),
	}, prepared, job)
	if err != nil {
		writeError(writer, http.StatusConflict, "enqueue_failed", err.Error())
		return
	}
	s.config.Worker.Notify()
	created, err = s.waitForJob(request.Context(), created, input.WaitMilliseconds)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "wait_failed", "failed waiting for durable job")
		return
	}
	result := RememberResponse{Job: created}
	status := http.StatusAccepted
	if result.Job.State == application.JobSucceeded || result.Job.State == application.JobFailed || result.Job.State == application.JobCanceled {
		status = http.StatusOK
	}
	writeJSON(writer, status, result)
}

func (s *Server) authorize(scope auth.Scope, next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		principal, err := s.authenticate(request)
		if err != nil {
			writer.Header().Set("WWW-Authenticate", `Bearer realm="memxplore"`)
			writeError(writer, http.StatusUnauthorized, "unauthorized", "valid bearer token required")
			return
		}
		if !principal.HasScope(scope) {
			writeError(writer, http.StatusForbidden, "forbidden", "token scope does not permit this operation")
			return
		}
		next.ServeHTTP(writer, request.WithContext(context.WithValue(request.Context(), principalContextKey{}, principal)))
	})
}

func (s *Server) authorizeAny(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		principal, err := s.authenticate(request)
		if err != nil {
			writer.Header().Set("WWW-Authenticate", `Bearer realm="memxplore"`)
			writeError(writer, http.StatusUnauthorized, "unauthorized", "valid bearer token required")
			return
		}
		next.ServeHTTP(writer, request.WithContext(context.WithValue(request.Context(), principalContextKey{}, principal)))
	})
}

func (s *Server) authenticate(request *http.Request) (auth.Principal, error) {
	header := request.Header.Get("Authorization")
	if header != "" {
		const prefix = "Bearer "
		if !strings.HasPrefix(header, prefix) || strings.TrimSpace(strings.TrimPrefix(header, prefix)) == "" {
			return auth.Principal{}, sqlite.ErrInvalidToken
		}
		return s.config.Store.AuthenticateToken(request.Context(), strings.TrimSpace(strings.TrimPrefix(header, prefix)), s.config.Now().UTC())
	}
	if s.config.AllowLoopbackWithoutToken && remoteIsLoopback(request.RemoteAddr) {
		return s.config.LoopbackPrincipal, nil
	}
	return auth.Principal{}, sqlite.ErrInvalidToken
}

func principalFrom(ctx context.Context) auth.Principal {
	principal, _ := ctx.Value(principalContextKey{}).(auth.Principal)
	return principal
}

func remoteIsLoopback(address string) bool {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func decodeRequest(writer http.ResponseWriter, request *http.Request, target any) bool {
	reader := http.MaxBytesReader(writer, request.Body, maxRequestBytes)
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json", "request body must be valid schema-conformant JSON")
		return false
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		writeError(writer, http.StatusBadRequest, "invalid_json", "request body must contain exactly one JSON value")
		return false
	}
	return true
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

func writeError(writer http.ResponseWriter, status int, code, message string) {
	writeJSON(writer, status, ErrorResponse{Error: APIError{Code: code, Message: message}})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		writer.Header().Set("Cache-Control", "no-store")
		writer.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		next.ServeHTTP(writer, request)
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusWriter) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(data)
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func observeHTTP(recorder observability.Recorder, next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		ctx, endOperation := recorder.Start(request.Context(), "http.request", observability.String("http.request.method", request.Method))
		observed := &statusWriter{ResponseWriter: writer}
		instrumented := request.WithContext(ctx)
		next.ServeHTTP(observed, instrumented)
		if observed.status == 0 {
			observed.status = http.StatusOK
		}
		pattern := instrumented.Pattern
		if pattern == "" {
			pattern = "unmatched"
		}
		attrs := []observability.Attribute{
			observability.String("http.route", pattern),
			observability.String("http.response.status_code", strconv.Itoa(observed.status)),
		}
		if observed.status >= http.StatusInternalServerError {
			endOperation(fmt.Errorf("HTTP %d", observed.status), attrs...)
			return
		}
		endOperation(nil, attrs...)
	})
}

func randomID(prefix string) (domain.ID, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return domain.ID(prefix + "-" + hex.EncodeToString(value)), nil
}
