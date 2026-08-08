package application

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/qingbo1011/memxplore/internal/domain"
)

type retrievalRepository struct {
	lexical  []StoredCandidate
	semantic []StoredCandidate
	filter   CandidateFilter
}

func (r *retrievalRepository) SearchLexicalCandidates(_ context.Context, filter CandidateFilter, _ string, _ int) ([]StoredCandidate, error) {
	r.filter = filter
	return append([]StoredCandidate(nil), r.lexical...), nil
}

func (r *retrievalRepository) ListSemanticCandidates(_ context.Context, filter CandidateFilter, _, _ string, _ int) ([]StoredCandidate, error) {
	r.filter = filter
	return append([]StoredCandidate(nil), r.semantic...), nil
}

type fixedEmbedder struct {
	vector []float32
	err    error
}

func (e fixedEmbedder) Embed(_ context.Context, _ EmbeddingRequest) (EmbeddingResponse, error) {
	if e.err != nil {
		return EmbeddingResponse{}, e.err
	}
	return EmbeddingResponse{Vectors: [][]float32{append([]float32(nil), e.vector...)}}, nil
}

type traceRecorder struct{ traces []domain.RetrievalTrace }

func (r *traceRecorder) PutRetrievalTrace(_ context.Context, trace domain.RetrievalTrace) error {
	r.traces = append(r.traces, trace)
	return nil
}

func recallRequest(mode RetrievalMode) RecallRequest {
	now := time.Date(2026, 8, 8, 2, 0, 0, 0, time.UTC)
	return RecallRequest{
		TraceID: "trace-test", Scope: domain.Scope{
			Namespace: "ns-test", Owner: "owner-a", Subject: "subject-a", Actor: "principal-a",
			Context: "task-a", Visibility: domain.VisibilityPrivate,
		},
		Access: AccessScope{
			PrincipalID: "principal-a", Namespace: "ns-test", PrivateOwners: []domain.ID{"owner-a"},
			AllowShared: true, AllowPublic: true,
		},
		Query: "concise answers", Functions: []domain.MemoryFunction{domain.FunctionFactual}, Mode: mode,
		ValidAt: now, SystemAt: now, TokenBudget: 100, CandidateLimit: 10,
	}
}

func factualCandidate(memoryID, versionID, text string, score float64) StoredCandidate {
	return StoredCandidate{
		MemoryID: domain.ID(memoryID), VersionID: domain.ID(versionID), Function: domain.FunctionFactual, Text: text,
		Payload: domain.MemoryPayload{Factual: &domain.FactualMemory{
			ClaimSubject: "subject-a", Predicate: "preference",
			Object:    domain.Content{Parts: []domain.ContentPart{{Kind: domain.PartText, Text: text}}},
			Epistemic: domain.EpistemicObserved,
		}},
		Provenance:  []domain.EvidenceRef{{ObservationID: domain.ID("obs-" + versionID), PartIndex: 0}},
		LexicalBM25: &score,
	}
}

func TestAutoWithoutEmbeddingsFallsBackAndReturnsEvidenceBundle(t *testing.T) {
	first := factualCandidate("mem-a", "mv-a", "concise answers are preferred", -2)
	first.ConflictGroup = "preference-conflict"
	duplicate := factualCandidate("mem-b", "mv-b", "  CONCISE answers are preferred ", -1.5)
	alternative := factualCandidate("mem-c", "mv-c", "detailed answers are preferred", -1)
	alternative.ConflictGroup = "preference-conflict"
	repository := &retrievalRepository{lexical: []StoredCandidate{first, duplicate, alternative}}
	sink := &traceRecorder{}
	now := recallRequest(RetrievalAuto).ValidAt
	retriever, err := NewRetriever(RetrieverConfig{Repository: repository, TraceSink: sink, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := retriever.Recall(context.Background(), recallRequest(RetrievalAuto))
	if err != nil {
		t.Fatal(err)
	}
	if bundle.Mode != RetrievalLexical || bundle.FallbackReason != "embedding_not_configured" {
		t.Fatalf("mode=%q fallback=%q", bundle.Mode, bundle.FallbackReason)
	}
	if len(bundle.Items) != 2 || len(bundle.Groups) != 1 || !bundle.Groups[0].Conflict {
		t.Fatalf("bundle did not preserve conflict group and dedupe: %+v", bundle)
	}
	if len(bundle.Trace.Candidates) != 3 || bundle.Trace.Candidates[1].DuplicateOf != "mv-a" {
		t.Fatalf("dedupe trace=%+v", bundle.Trace.Candidates)
	}
	if len(sink.traces) != 1 || repository.filter.Context != "task-a" {
		t.Fatalf("trace/filter missing: traces=%d filter=%+v", len(sink.traces), repository.filter)
	}
}

func TestExactCosineSemanticOrdersCandidates(t *testing.T) {
	orthogonal := factualCandidate("mem-a", "mv-a", "orthogonal", 0)
	orthogonal.Vector = []float32{0, 1}
	matching := factualCandidate("mem-b", "mv-b", "matching", 0)
	matching.Vector = []float32{1, 0}
	repository := &retrievalRepository{semantic: []StoredCandidate{orthogonal, matching}}
	now := recallRequest(RetrievalSemantic).ValidAt
	retriever, err := NewRetriever(RetrieverConfig{
		Repository: repository, Embedder: fixedEmbedder{vector: []float32{1, 0}},
		EmbeddingProvider: "fake", EmbeddingModel: "fake-v1", EmbeddingDimensions: 2,
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := retriever.Recall(context.Background(), recallRequest(RetrievalSemantic))
	if err != nil {
		t.Fatal(err)
	}
	if len(bundle.Items) != 2 || bundle.Items[0].VersionID != "mv-b" || bundle.Items[0].Score.Semantic == nil || math.Abs(*bundle.Items[0].Score.Semantic-1) > 1e-12 {
		t.Fatalf("semantic bundle=%+v", bundle)
	}
}

func TestAutoHybridUsesRRFAndFallsBackOnProviderFailure(t *testing.T) {
	lexicalFirst := factualCandidate("mem-a", "mv-a", "lexical", -2)
	semanticFirst := factualCandidate("mem-b", "mv-b", "semantic", -1)
	lexicalFirst.Vector = []float32{0, 1}
	semanticFirst.Vector = []float32{1, 0}
	repository := &retrievalRepository{
		lexical:  []StoredCandidate{lexicalFirst, semanticFirst},
		semantic: []StoredCandidate{lexicalFirst, semanticFirst},
	}
	now := recallRequest(RetrievalAuto).ValidAt
	retriever, err := NewRetriever(RetrieverConfig{
		Repository: repository, Embedder: fixedEmbedder{vector: []float32{1, 0}},
		EmbeddingProvider: "fake", EmbeddingModel: "fake-v1", EmbeddingDimensions: 2,
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := retriever.Recall(context.Background(), recallRequest(RetrievalAuto))
	if err != nil {
		t.Fatal(err)
	}
	if bundle.Mode != RetrievalHybrid || len(bundle.Items) != 2 || bundle.Items[0].Score.RRF == nil {
		t.Fatalf("hybrid bundle=%+v", bundle)
	}

	failing, err := NewRetriever(RetrieverConfig{
		Repository: repository, Embedder: fixedEmbedder{err: errors.New("offline")},
		EmbeddingProvider: "fake", EmbeddingModel: "fake-v1", EmbeddingDimensions: 2,
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	fallback, err := failing.Recall(context.Background(), recallRequest(RetrievalAuto))
	if err != nil || fallback.Mode != RetrievalLexical || fallback.FallbackReason != "embedding_unavailable" {
		t.Fatalf("fallback=%+v err=%v", fallback, err)
	}
}

func TestExplicitSemanticRequiresProviderAndBudgetIsHard(t *testing.T) {
	repository := &retrievalRepository{lexical: []StoredCandidate{factualCandidate("mem-a", "mv-a", "this text exceeds one token", -1)}}
	now := recallRequest(RetrievalSemantic).ValidAt
	retriever, err := NewRetriever(RetrieverConfig{Repository: repository, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := retriever.Recall(context.Background(), recallRequest(RetrievalSemantic)); err == nil {
		t.Fatal("semantic retrieval without provider succeeded")
	}
	request := recallRequest(RetrievalLexical)
	request.TokenBudget = 1
	bundle, err := retriever.Recall(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if len(bundle.Items) != 0 || bundle.Trace.TokensUsed != 0 || bundle.Trace.Candidates[0].Selected {
		t.Fatalf("token budget was exceeded: %+v", bundle)
	}
}

func TestCosineRejectsInvalidVectors(t *testing.T) {
	if _, err := cosine([]float32{0, 0}, []float32{1, 0}); err == nil {
		t.Fatal("zero vector accepted")
	}
	if _, err := cosine([]float32{float32(math.NaN())}, []float32{1}); err == nil {
		t.Fatal("non-finite vector accepted")
	}
}
