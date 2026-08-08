package sqlite

import (
	"context"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/qingbo1011/memxplore/internal/application"
	"github.com/qingbo1011/memxplore/internal/domain"
)

func putRetrievalFactual(t *testing.T, store *Store, memoryID, versionID, owner string, visibility domain.Visibility, text string, valid, system domain.TimeRange) {
	t.Helper()
	memory, version := testFactualMemory(memoryID, versionID, "obs-"+memoryID, text)
	memory.Scope.Owner = domain.ID(owner)
	memory.Scope.Visibility = visibility
	version.ValidTime = valid
	version.SystemTime = system
	if err := store.PutMemory(context.Background(), memory, version); err != nil {
		t.Fatalf("PutMemory(%s): %v", memoryID, err)
	}
}

func putRetrievalWorking(t *testing.T, store *Store, memoryID, versionID, contextID, text string, now time.Time) {
	t.Helper()
	scope := testObservation("obs-working", text).Scope
	scope.Context = domain.ID(contextID)
	memory := domain.Memory{
		ID: domain.ID(memoryID), Scope: scope, Function: domain.FunctionWorking,
		State: domain.MemoryActive, CurrentVersion: 1, CreatedAt: now,
	}
	version := domain.MemoryVersion{
		ID: domain.ID(versionID), MemoryID: memory.ID, Number: 1, State: domain.VersionCurrent,
		Taxonomy: domain.Taxonomy{
			Forms: []string{"token-flat"}, Functions: []string{"working"}, Dynamics: []string{"formation", "retrieval"},
		},
		ValidTime: domain.TimeRange{From: now.Add(-time.Hour)}, SystemTime: domain.TimeRange{From: now.Add(-time.Hour)},
		Provenance: []domain.EvidenceRef{{ObservationID: "obs-working", PartIndex: 0}},
		Payload: domain.MemoryPayload{Working: &domain.WorkingMemory{
			WorkingSetID: "ws-" + domain.ID(memoryID), TaskID: domain.ID(contextID),
			Goal:  domain.Content{Parts: []domain.ContentPart{{Kind: domain.PartText, Text: "finish retrieval"}}},
			State: domain.Content{Parts: []domain.ContentPart{{Kind: domain.PartText, Text: text}}},
		}},
	}
	if err := store.PutMemory(context.Background(), memory, version); err != nil {
		t.Fatalf("PutMemory(working): %v", err)
	}
}

func TestAuthorizedBitemporalCandidateQueriesAndEmbeddings(t *testing.T) {
	ctx := context.Background()
	store := testStore(t)
	now := time.Date(2026, 8, 8, 3, 0, 0, 0, time.UTC)
	currentValid := domain.TimeRange{From: now.Add(-time.Hour)}
	currentSystem := domain.TimeRange{From: now.Add(-time.Hour)}
	putRetrievalFactual(t, store, "mem-private-a", "mv-private-a", "owner-alice", domain.VisibilityPrivate, "retrieval needle private alice", currentValid, currentSystem)
	putRetrievalFactual(t, store, "mem-private-b", "mv-private-b", "owner-bob", domain.VisibilityPrivate, "retrieval needle private bob", currentValid, currentSystem)
	putRetrievalFactual(t, store, "mem-shared", "mv-shared", "owner-bob", domain.VisibilityShared, "retrieval needle shared", currentValid, currentSystem)
	putRetrievalFactual(t, store, "mem-public", "mv-public", "owner-bob", domain.VisibilityPublic, "retrieval needle public", currentValid, currentSystem)
	expiredAt := now.Add(-time.Minute)
	putRetrievalFactual(t, store, "mem-expired", "mv-expired", "owner-alice", domain.VisibilityPrivate, "retrieval needle expired",
		domain.TimeRange{From: now.Add(-time.Hour), To: &expiredAt}, currentSystem)
	putRetrievalFactual(t, store, "mem-future", "mv-future", "owner-alice", domain.VisibilityPrivate, "retrieval needle future",
		currentValid, domain.TimeRange{From: now.Add(time.Hour)})
	putRetrievalWorking(t, store, "mem-working-other", "mv-working-other", "task-other", "retrieval needle working", now)

	for _, versionID := range []domain.ID{"mv-private-a", "mv-private-b", "mv-shared", "mv-public"} {
		var content string
		if err := store.db.QueryRow("SELECT text_content FROM memory_fts WHERE memory_version_id = ?", versionID).Scan(&content); err != nil {
			t.Fatal(err)
		}
		if err := store.PutEmbedding(ctx, versionID, "fake", "embed-v1", content, []float32{1, 0}, now); err != nil {
			t.Fatalf("PutEmbedding(%s): %v", versionID, err)
		}
	}
	if err := store.PutEmbedding(ctx, "mv-private-a", "fake", "bad-content", "wrong", []float32{1, 0}, now); err == nil {
		t.Fatal("embedding with mismatched immutable content succeeded")
	}

	filter := application.CandidateFilter{
		Access: application.AccessScope{
			PrincipalID: "principal-alice", Namespace: "ns-test", PrivateOwners: []domain.ID{"owner-alice"},
			AllowShared: true, AllowPublic: false,
		},
		Subject: "subject-alice", Context: "task-test", ValidAt: now, SystemAt: now,
	}
	lexical, err := store.SearchLexicalCandidates(ctx, filter, `retrieval OR "private" *`, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(lexical) != 2 || lexical[0].LexicalBM25 == nil {
		t.Fatalf("authorized lexical candidates=%+v", lexical)
	}
	got := map[domain.ID]bool{}
	for _, candidate := range lexical {
		got[candidate.VersionID] = true
	}
	if !got["mv-private-a"] || !got["mv-shared"] || got["mv-private-b"] || got["mv-public"] || got["mv-expired"] || got["mv-future"] || got["mv-working-other"] {
		t.Fatalf("authorization/time/context leak: %+v", got)
	}
	semantic, err := store.ListSemanticCandidates(ctx, filter, "fake", "embed-v1", 20)
	if err != nil || len(semantic) != 2 || len(semantic[0].Vector) != 2 {
		t.Fatalf("semantic candidates=%+v err=%v", semantic, err)
	}
	filter.Access.AllowPublic = true
	lexical, err = store.SearchLexicalCandidates(ctx, filter, "needle", 20)
	if err != nil || len(lexical) != 3 {
		t.Fatalf("public opt-in candidates=%d err=%v", len(lexical), err)
	}
}

func TestEmbeddingRejectsNonFiniteAndPurgeCascades(t *testing.T) {
	ctx := context.Background()
	store := testStore(t)
	now := time.Now().UTC()
	memory, version := testFactualMemory("mem-embedding", "mv-embedding", "obs-embedding", "embedding content")
	if err := store.PutMemory(ctx, memory, version); err != nil {
		t.Fatal(err)
	}
	content := payloadPlainText(version.Payload)
	if err := store.PutEmbedding(ctx, version.ID, "fake", "bad", content, []float32{float32(math.NaN())}, now); err == nil {
		t.Fatal("non-finite embedding succeeded")
	}
	if err := store.PutEmbedding(ctx, version.ID, "fake", "good", content, []float32{1, 0}, now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PurgeMemory(ctx, "receipt-embedding", "ns-test", "actor-admin", memory.ID, now); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := store.db.QueryRow("SELECT count(*) FROM memory_embeddings WHERE memory_version_id = ?", version.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("post-purge embedding count=%d err=%v", count, err)
	}
}

func TestRetrievalTracePersistsCandidateDecisions(t *testing.T) {
	ctx := context.Background()
	store := testStore(t)
	now := time.Now().UTC()
	trace := domain.RetrievalTrace{
		ID: "trace-sqlite", Scope: testObservation("obs-trace", "trace").Scope,
		Query: "trace", StrategyID: "retrieval.hybrid@1.0.0",
		StrategyHash: strings.Repeat("0", 64),
		ValidAt:      now, SystemAt: now, TokenBudget: 10, TokensUsed: 2,
		Candidates: []domain.RetrievalCandidate{{
			MemoryID: "mem-a", VersionID: "mv-a", Selected: true, EstimatedTokens: 2,
			Score: domain.ScoreExplanation{Trust: 0.7, Total: 0.5},
		}},
		StartedAt: now, CompletedAt: now,
	}
	if err := store.PutRetrievalTrace(ctx, trace); err != nil {
		t.Fatal(err)
	}
	var traces, candidates int
	if err := store.db.QueryRow("SELECT count(*) FROM retrieval_traces WHERE id = ?", trace.ID).Scan(&traces); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow("SELECT count(*) FROM retrieval_candidates WHERE trace_id = ?", trace.ID).Scan(&candidates); err != nil {
		t.Fatal(err)
	}
	if traces != 1 || candidates != 1 {
		t.Fatalf("persisted traces=%d candidates=%d", traces, candidates)
	}
}

func TestSafeFTSQueryRejectsSyntaxOnlyInput(t *testing.T) {
	if _, err := safeFTSQuery(`*** ""`); err == nil {
		t.Fatal("syntax-only FTS query succeeded")
	}
	query, err := safeFTSQuery(`alpha OR "beta" *`)
	if err != nil || query != `"alpha" OR "OR" OR "beta"` {
		t.Fatalf("safe query=%q err=%v", query, err)
	}
}
