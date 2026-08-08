package application

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/qingbo1011/memxplore/internal/domain"
	"github.com/qingbo1011/memxplore/internal/observability"
	strategydef "github.com/qingbo1011/memxplore/internal/strategy"
)

// RetrievalMode selects the auditable candidate-ranking algorithm.
type RetrievalMode string

const (
	RetrievalAuto     RetrievalMode = "auto"
	RetrievalLexical  RetrievalMode = "lexical"
	RetrievalSemantic RetrievalMode = "semantic"
	RetrievalHybrid   RetrievalMode = "hybrid"
)

// AccessScope is the authorization result supplied to the retrieval use case.
// Adapters must filter private content before returning candidate payloads.
type AccessScope struct {
	PrincipalID   domain.ID   `json:"principal_id"`
	Namespace     domain.ID   `json:"namespace"`
	PrivateOwners []domain.ID `json:"private_owners"`
	AllowShared   bool        `json:"allow_shared"`
	AllowPublic   bool        `json:"allow_public"`
}

// CandidateFilter is pushed into storage before content is materialized.
type CandidateFilter struct {
	Access               AccessScope
	Subject              domain.ID
	Context              domain.ID
	Functions            []domain.MemoryFunction
	ValidAt              time.Time
	SystemAt             time.Time
	IncludeGlobalWorking bool
}

// RecallRequest asks for evidence, never a generated answer.
type RecallRequest struct {
	TraceID              domain.ID
	Scope                domain.Scope
	Access               AccessScope
	Query                string
	Functions            []domain.MemoryFunction
	Mode                 RetrievalMode
	ValidAt              time.Time
	SystemAt             time.Time
	TokenBudget          int
	CandidateLimit       int
	IncludeGlobalWorking bool
}

// StoredCandidate is a permission- and time-filtered immutable memory version.
type StoredCandidate struct {
	MemoryID      domain.ID
	VersionID     domain.ID
	Function      domain.MemoryFunction
	ConflictGroup domain.ID
	Payload       domain.MemoryPayload
	Provenance    []domain.EvidenceRef
	Text          string
	LexicalBM25   *float64
	SemanticScore *float64
	Vector        []float32
}

// CandidateRepository is the retrieval-facing persistence port.
type CandidateRepository interface {
	SearchLexicalCandidates(context.Context, CandidateFilter, string, int) ([]StoredCandidate, error)
	ListSemanticCandidates(context.Context, CandidateFilter, string, string, int) ([]StoredCandidate, error)
}

// RetrievalTraceSink persists an immutable decision trace.
type RetrievalTraceSink interface {
	PutRetrievalTrace(context.Context, domain.RetrievalTrace) error
}

// RecallItem contains typed source evidence and an independent score explanation.
type RecallItem struct {
	MemoryID        domain.ID               `json:"memory_id"`
	VersionID       domain.ID               `json:"version_id"`
	Function        domain.MemoryFunction   `json:"function"`
	ConflictGroup   domain.ID               `json:"conflict_group,omitempty"`
	Payload         domain.MemoryPayload    `json:"payload"`
	Provenance      []domain.EvidenceRef    `json:"provenance"`
	EstimatedTokens int                     `json:"estimated_tokens"`
	Score           domain.ScoreExplanation `json:"score"`
}

// RecallGroup keeps conflicting alternatives visible to downstream callers.
type RecallGroup struct {
	ID       string       `json:"id"`
	Conflict bool         `json:"conflict"`
	Items    []RecallItem `json:"items"`
}

// RecallBundle is structured retrieval evidence. It is deliberately not an answer.
type RecallBundle struct {
	Query          string                `json:"query"`
	Mode           RetrievalMode         `json:"mode"`
	FallbackReason string                `json:"fallback_reason,omitempty"`
	Items          []RecallItem          `json:"items"`
	Groups         []RecallGroup         `json:"groups"`
	Trace          domain.RetrievalTrace `json:"trace"`
}

// RetrieverConfig binds semantic retrieval to one explicit embedding identity.
type RetrieverConfig struct {
	Repository          CandidateRepository
	TraceSink           RetrievalTraceSink
	Embedder            EmbeddingProvider
	EmbeddingProvider   string
	EmbeddingModel      string
	EmbeddingDimensions int
	RRFConstant         int
	Now                 func() time.Time
	Observability       observability.Recorder
}

// Retriever executes deterministic lexical, exact-cosine, and RRF hybrid recall.
type Retriever struct {
	repository  CandidateRepository
	traceSink   RetrievalTraceSink
	embedder    EmbeddingProvider
	provider    string
	model       string
	dimensions  int
	rrfConstant int
	now         func() time.Time
	observer    observability.Recorder
}

// NewRetriever validates all configured retrieval capabilities.
func NewRetriever(config RetrieverConfig) (*Retriever, error) {
	if config.Repository == nil {
		return nil, fmt.Errorf("candidate repository is required")
	}
	if config.Embedder != nil && (config.EmbeddingProvider == "" || config.EmbeddingModel == "" || config.EmbeddingDimensions < 1) {
		return nil, fmt.Errorf("configured embedder requires provider, model, and dimensions")
	}
	if config.Embedder == nil && (config.EmbeddingProvider != "" || config.EmbeddingModel != "" || config.EmbeddingDimensions != 0) {
		return nil, fmt.Errorf("embedding identity cannot be configured without an embedder")
	}
	if config.RRFConstant <= 0 {
		config.RRFConstant = 60
	}
	if config.Now == nil {
		config.Now = func() time.Time { return time.Now().UTC() }
	}
	return &Retriever{
		repository: config.Repository, traceSink: config.TraceSink, embedder: config.Embedder,
		provider: config.EmbeddingProvider, model: config.EmbeddingModel,
		dimensions: config.EmbeddingDimensions, rrfConstant: config.RRFConstant, now: config.Now,
		observer: observability.OrNop(config.Observability),
	}, nil
}

// Recall returns a token-budgeted bundle and persists the full candidate trace when configured.
func (r *Retriever) Recall(ctx context.Context, request RecallRequest) (_ RecallBundle, finalErr error) {
	requestedMode := string(request.Mode)
	if requestedMode == "" {
		requestedMode = string(RetrievalAuto)
	}
	ctx, endOperation := r.observer.Start(ctx, "memory.recall", observability.String("requested_mode", requestedMode))
	defer func() { endOperation(finalErr) }()
	if err := validateRecallRequest(request); err != nil {
		return RecallBundle{}, err
	}
	startedAt := r.now().UTC()
	mode, fallback, err := r.resolveMode(request.Mode)
	if err != nil {
		return RecallBundle{}, err
	}
	filter := CandidateFilter{
		Access: request.Access, Subject: request.Scope.Subject, Context: request.Scope.Context,
		Functions: append([]domain.MemoryFunction(nil), request.Functions...),
		ValidAt:   request.ValidAt, SystemAt: request.SystemAt,
		IncludeGlobalWorking: request.IncludeGlobalWorking,
	}
	poolLimit := request.CandidateLimit * 4
	if poolLimit > 1000 {
		poolLimit = 1000
	}
	var lexical, semantic []StoredCandidate
	if mode == RetrievalLexical || mode == RetrievalHybrid {
		lexical, err = r.repository.SearchLexicalCandidates(ctx, filter, request.Query, poolLimit)
		if err != nil {
			return RecallBundle{}, fmt.Errorf("lexical recall: %w", err)
		}
	}
	if mode == RetrievalSemantic || mode == RetrievalHybrid {
		semantic, err = r.semanticCandidates(ctx, filter, request.Query, poolLimit)
		if err != nil {
			if request.Mode != RetrievalAuto {
				return RecallBundle{}, err
			}
			mode = RetrievalLexical
			fallback = "embedding_unavailable"
			if lexical == nil {
				lexical, err = r.repository.SearchLexicalCandidates(ctx, filter, request.Query, poolLimit)
				if err != nil {
					return RecallBundle{}, fmt.Errorf("lexical fallback: %w", err)
				}
			}
		}
		if err == nil && len(semantic) == 0 && request.Mode == RetrievalAuto {
			mode = RetrievalLexical
			fallback = "no_compatible_embeddings"
			if lexical == nil {
				lexical, err = r.repository.SearchLexicalCandidates(ctx, filter, request.Query, poolLimit)
				if err != nil {
					return RecallBundle{}, fmt.Errorf("lexical fallback: %w", err)
				}
			}
		}
		if err == nil && len(semantic) == 0 && request.Mode == RetrievalHybrid && len(lexical) > 0 {
			return RecallBundle{}, fmt.Errorf("hybrid retrieval found no compatible stored embeddings")
		}
	}
	ranked := rankCandidates(mode, lexical, semantic, r.rrfConstant)
	if len(ranked) > request.CandidateLimit {
		ranked = ranked[:request.CandidateLimit]
	}
	items, traceCandidates, tokensUsed := selectCandidates(ranked, request.TokenBudget)
	groups := groupRecallItems(items)
	traceID := request.TraceID
	if traceID == "" {
		traceID, err = randomTraceID()
		if err != nil {
			return RecallBundle{}, err
		}
	}
	completedAt := r.now().UTC()
	definition := r.strategyPackage(mode)
	strategyHash, err := definition.Hash()
	if err != nil {
		return RecallBundle{}, fmt.Errorf("hash retrieval strategy: %w", err)
	}
	trace := domain.RetrievalTrace{
		ID: traceID, Scope: request.Scope, Query: request.Query,
		StrategyID: definition.ID + "@" + definition.Version, StrategyHash: strategyHash,
		FallbackReason: fallback,
		Authorization: domain.RetrievalAuthorization{
			PrincipalID:   request.Access.PrincipalID,
			PrivateOwners: append([]domain.ID(nil), request.Access.PrivateOwners...),
			AllowShared:   request.Access.AllowShared, AllowPublic: request.Access.AllowPublic,
		},
		Functions:            append([]domain.MemoryFunction(nil), request.Functions...),
		IncludeGlobalWorking: request.IncludeGlobalWorking,
		ValidAt:              request.ValidAt, SystemAt: request.SystemAt,
		TokenBudget: request.TokenBudget, TokensUsed: tokensUsed, Candidates: traceCandidates,
		StartedAt: startedAt, CompletedAt: completedAt,
	}
	if err := trace.Validate(); err != nil {
		return RecallBundle{}, fmt.Errorf("validate retrieval trace: %w", err)
	}
	if r.traceSink != nil {
		if err := r.traceSink.PutRetrievalTrace(ctx, trace); err != nil {
			return RecallBundle{}, fmt.Errorf("persist retrieval trace: %w", err)
		}
	}
	metricAttrs := []observability.Attribute{observability.String("mode", string(mode))}
	r.observer.Observe(ctx, observability.MetricRetrievalCandidates, float64(len(traceCandidates)), metricAttrs...)
	r.observer.Observe(ctx, observability.MetricRetrievalSelected, float64(len(items)), metricAttrs...)
	r.observer.Observe(ctx, observability.MetricRetrievalTokens, float64(tokensUsed), metricAttrs...)
	return RecallBundle{
		Query: request.Query, Mode: mode, FallbackReason: fallback,
		Items: items, Groups: groups, Trace: trace,
	}, nil
}

func (r *Retriever) resolveMode(requested RetrievalMode) (RetrievalMode, string, error) {
	switch requested {
	case "", RetrievalAuto:
		if r.embedder != nil {
			return RetrievalHybrid, "", nil
		}
		return RetrievalLexical, "embedding_not_configured", nil
	case RetrievalLexical:
		return requested, "", nil
	case RetrievalSemantic, RetrievalHybrid:
		if r.embedder == nil {
			return "", "", fmt.Errorf("%s retrieval requires a configured embedding provider", requested)
		}
		return requested, "", nil
	default:
		return "", "", fmt.Errorf("retrieval mode %q is invalid", requested)
	}
}

func (r *Retriever) semanticCandidates(ctx context.Context, filter CandidateFilter, query string, limit int) ([]StoredCandidate, error) {
	response, err := r.embedder.Embed(ctx, EmbeddingRequest{
		Model: r.model, Input: []string{query}, Dimensions: r.dimensions,
	})
	if err != nil {
		return nil, fmt.Errorf("embed recall query: %w", err)
	}
	if len(response.Vectors) != 1 || len(response.Vectors[0]) != r.dimensions {
		return nil, fmt.Errorf("query embedding dimension mismatch")
	}
	if err := validateEmbeddingVector(response.Vectors[0]); err != nil {
		return nil, fmt.Errorf("query embedding: %w", err)
	}
	const exactScanLimit = 10000
	candidates, err := r.repository.ListSemanticCandidates(ctx, filter, r.provider, r.model, exactScanLimit+1)
	if err != nil {
		return nil, fmt.Errorf("load semantic candidates: %w", err)
	}
	if len(candidates) > exactScanLimit {
		return nil, fmt.Errorf("exact semantic candidate set exceeds scan limit %d", exactScanLimit)
	}
	queryVector := response.Vectors[0]
	for index := range candidates {
		if len(candidates[index].Vector) != r.dimensions {
			return nil, fmt.Errorf("stored embedding %s dimension mismatch", candidates[index].VersionID)
		}
		score, err := cosine(queryVector, candidates[index].Vector)
		if err != nil {
			return nil, fmt.Errorf("score embedding %s: %w", candidates[index].VersionID, err)
		}
		candidates[index].SemanticScore = &score
	}
	sort.Slice(candidates, func(i, j int) bool {
		left, right := *candidates[i].SemanticScore, *candidates[j].SemanticScore
		if left != right {
			return left > right
		}
		return candidates[i].VersionID < candidates[j].VersionID
	})
	if len(candidates) > limit {
		candidates = candidates[:limit]
	}
	return candidates, nil
}

func (r *Retriever) strategyPackage(mode RetrievalMode) strategydef.Package {
	parameters, _ := json.Marshal(map[string]any{
		"mode": mode, "rrf_constant": r.rrfConstant, "embedding_provider": r.provider,
		"embedding_model": r.model, "embedding_dimensions": r.dimensions, "exact_scan_limit": 10000,
	})
	capabilities := []string{"retrieval", string(mode)}
	if mode == RetrievalSemantic || mode == RetrievalHybrid {
		capabilities = append(capabilities, "exact-cosine")
	}
	if mode == RetrievalHybrid {
		capabilities = append(capabilities, "rrf")
	}
	return strategydef.Package{
		ID: "retrieval." + string(mode), Version: "1.0.0",
		Implementation: "internal/application/retrieval.go", Label: strategydef.ImplementationReference,
		Fidelity: strategydef.FidelityConceptual, Parameters: parameters, Capabilities: capabilities,
		Repair:       strategydef.RepairPolicy{Strict: true},
		PaperSources: []string{"survey:section-3.4", "survey:section-5"},
	}
}

type rankedCandidate struct {
	stored   StoredCandidate
	lexical  *float64
	semantic *float64
	rrf      *float64
	trust    float64
	total    float64
}

func rankCandidates(mode RetrievalMode, lexical, semantic []StoredCandidate, rrfConstant int) []rankedCandidate {
	byVersion := make(map[domain.ID]*rankedCandidate, len(lexical)+len(semantic))
	lexicalRanks := make(map[domain.ID]int, len(lexical))
	semanticRanks := make(map[domain.ID]int, len(semantic))
	for index, candidate := range lexical {
		item := candidate
		byVersion[candidate.VersionID] = &rankedCandidate{stored: item}
		lexicalRanks[candidate.VersionID] = index + 1
		if candidate.LexicalBM25 != nil {
			score := *candidate.LexicalBM25
			byVersion[candidate.VersionID].lexical = &score
		}
	}
	for index, candidate := range semantic {
		item := byVersion[candidate.VersionID]
		if item == nil {
			copy := candidate
			item = &rankedCandidate{stored: copy}
			byVersion[candidate.VersionID] = item
		}
		semanticRanks[candidate.VersionID] = index + 1
		if candidate.SemanticScore != nil {
			score := *candidate.SemanticScore
			item.semantic = &score
		}
	}
	result := make([]rankedCandidate, 0, len(byVersion))
	for versionID, item := range byVersion {
		item.trust = candidateTrust(item.stored)
		switch mode {
		case RetrievalLexical:
			item.total = 1/float64(rrfConstant+lexicalRanks[versionID]) + item.trust*0.001
		case RetrievalSemantic:
			if item.semantic != nil {
				item.total = *item.semantic + item.trust*0.001
			}
		case RetrievalHybrid:
			rrf := 0.0
			if rank := lexicalRanks[versionID]; rank > 0 {
				rrf += 1 / float64(rrfConstant+rank)
			}
			if rank := semanticRanks[versionID]; rank > 0 {
				rrf += 1 / float64(rrfConstant+rank)
			}
			item.rrf = &rrf
			item.total = rrf + item.trust*0.001
		}
		result = append(result, *item)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].total != result[j].total {
			return result[i].total > result[j].total
		}
		if result[i].trust != result[j].trust {
			return result[i].trust > result[j].trust
		}
		return result[i].stored.VersionID < result[j].stored.VersionID
	})
	return result
}

func selectCandidates(ranked []rankedCandidate, budget int) ([]RecallItem, []domain.RetrievalCandidate, int) {
	items := make([]RecallItem, 0, len(ranked))
	trace := make([]domain.RetrievalCandidate, 0, len(ranked))
	seen := make(map[string]domain.ID, len(ranked))
	used := 0
	for _, candidate := range ranked {
		estimated := estimateTokens(candidate.stored.Text)
		key := dedupeKey(candidate.stored)
		duplicate := seen[key]
		if duplicate == "" {
			seen[key] = candidate.stored.VersionID
		}
		selected := duplicate == "" && used+estimated <= budget
		score := domain.ScoreExplanation{
			Lexical: candidate.lexical, Semantic: candidate.semantic, RRF: candidate.rrf,
			Trust: candidate.trust, Total: candidate.total,
		}
		trace = append(trace, domain.RetrievalCandidate{
			MemoryID: candidate.stored.MemoryID, VersionID: candidate.stored.VersionID,
			ConflictGroup: candidate.stored.ConflictGroup, Selected: selected, DuplicateOf: duplicate,
			EstimatedTokens: estimated, Score: score,
		})
		if !selected {
			continue
		}
		used += estimated
		items = append(items, RecallItem{
			MemoryID: candidate.stored.MemoryID, VersionID: candidate.stored.VersionID,
			Function: candidate.stored.Function, ConflictGroup: candidate.stored.ConflictGroup,
			Payload: candidate.stored.Payload, Provenance: candidate.stored.Provenance,
			EstimatedTokens: estimated, Score: score,
		})
	}
	return items, trace, used
}

func groupRecallItems(items []RecallItem) []RecallGroup {
	groups := make([]RecallGroup, 0, len(items))
	indexes := make(map[string]int, len(items))
	for _, item := range items {
		id := "memory:" + string(item.MemoryID)
		conflict := item.ConflictGroup != ""
		if conflict {
			id = "conflict:" + string(item.ConflictGroup)
		}
		index, exists := indexes[id]
		if !exists {
			indexes[id] = len(groups)
			groups = append(groups, RecallGroup{ID: id, Conflict: conflict})
			index = len(groups) - 1
		}
		groups[index].Items = append(groups[index].Items, item)
	}
	return groups
}

func candidateTrust(candidate StoredCandidate) float64 {
	switch {
	case candidate.Payload.Factual != nil:
		value := map[domain.EpistemicStatus]float64{
			domain.EpistemicObserved: 0.9, domain.EpistemicAsserted: 0.7,
			domain.EpistemicInferred: 0.5, domain.EpistemicContested: 0.2, domain.EpistemicUnknown: 0.3,
		}[candidate.Payload.Factual.Epistemic]
		if candidate.Payload.Factual.Confidence != nil {
			value *= *candidate.Payload.Factual.Confidence
		}
		return value
	case candidate.Payload.Experiential != nil:
		return 0.7
	case candidate.Payload.Working != nil:
		return 0.6
	default:
		return 0
	}
}

func cosine(left, right []float32) (float64, error) {
	if len(left) == 0 || len(left) != len(right) {
		return 0, fmt.Errorf("cosine vectors must have equal non-zero dimensions")
	}
	var dot, leftSquared, rightSquared float64
	for index := range left {
		l, r := float64(left[index]), float64(right[index])
		if math.IsNaN(l) || math.IsInf(l, 0) || math.IsNaN(r) || math.IsInf(r, 0) {
			return 0, fmt.Errorf("cosine vector contains a non-finite value")
		}
		dot += l * r
		leftSquared += l * l
		rightSquared += r * r
	}
	if leftSquared == 0 || rightSquared == 0 {
		return 0, fmt.Errorf("cosine vector has zero norm")
	}
	return dot / (math.Sqrt(leftSquared) * math.Sqrt(rightSquared)), nil
}

func validateEmbeddingVector(vector []float32) error {
	if len(vector) == 0 {
		return fmt.Errorf("vector is empty")
	}
	var squared float64
	for _, value := range vector {
		converted := float64(value)
		if math.IsNaN(converted) || math.IsInf(converted, 0) {
			return fmt.Errorf("vector contains a non-finite value")
		}
		squared += converted * converted
	}
	if squared == 0 {
		return fmt.Errorf("vector has zero norm")
	}
	return nil
}

func validateRecallRequest(request RecallRequest) error {
	if err := request.Scope.Validate(); err != nil {
		return err
	}
	if request.Access.PrincipalID == "" || request.Access.Namespace == "" || request.Access.Namespace != request.Scope.Namespace {
		return fmt.Errorf("authorized principal and matching namespace are required")
	}
	if len(request.Access.PrivateOwners) == 0 {
		return fmt.Errorf("at least one authorized private owner is required")
	}
	seenOwners := make(map[domain.ID]struct{}, len(request.Access.PrivateOwners))
	for _, owner := range request.Access.PrivateOwners {
		if owner == "" {
			return fmt.Errorf("authorized private owner cannot be empty")
		}
		if _, duplicate := seenOwners[owner]; duplicate {
			return fmt.Errorf("authorized private owners contain duplicate %s", owner)
		}
		seenOwners[owner] = struct{}{}
	}
	if strings.TrimSpace(request.Query) == "" || utf8.RuneCountInString(request.Query) > 10000 {
		return fmt.Errorf("recall query is required and cannot exceed 10000 characters")
	}
	if request.ValidAt.IsZero() || request.SystemAt.IsZero() || request.TokenBudget < 1 || request.TokenBudget > 1_000_000 {
		return fmt.Errorf("recall times and token budget within [1,1000000] are required")
	}
	if request.CandidateLimit < 1 || request.CandidateLimit > 250 {
		return fmt.Errorf("candidate limit must be within [1,250]")
	}
	seenFunctions := make(map[domain.MemoryFunction]struct{}, len(request.Functions))
	for _, function := range request.Functions {
		if !slices.Contains([]domain.MemoryFunction{domain.FunctionFactual, domain.FunctionExperiential, domain.FunctionWorking}, function) {
			return fmt.Errorf("memory function %q is invalid", function)
		}
		if _, duplicate := seenFunctions[function]; duplicate {
			return fmt.Errorf("memory functions contain duplicate %q", function)
		}
		seenFunctions[function] = struct{}{}
	}
	return nil
}

func estimateTokens(text string) int {
	characters := utf8.RuneCountInString(text)
	if characters == 0 {
		return 1
	}
	return (characters + 3) / 4
}

func dedupeKey(candidate StoredCandidate) string {
	text := strings.ToLower(strings.Join(strings.Fields(candidate.Text), " "))
	if text == "" {
		text = string(candidate.VersionID)
	}
	return string(candidate.Function) + "\x00" + text
}

func randomTraceID() (domain.ID, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("generate retrieval trace id: %w", err)
	}
	return domain.ID("trace-" + hex.EncodeToString(value[:])), nil
}
