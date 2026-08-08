package sqlite

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/qingbo1011/memxplore/internal/application"
	"github.com/qingbo1011/memxplore/internal/domain"
)

func TestExperientialEpisodesOutcomesAndFeedbackRemainIndependent(t *testing.T) {
	ctx := context.Background()
	store := testStore(t)
	base := time.Date(2026, 8, 8, 7, 0, 0, 0, time.UTC)
	scope := testObservation("obs-episode", "task evidence").Scope
	episode := domain.Episode{
		ID: "episode-test", Scope: scope,
		Task:           domain.Content{Parts: []domain.ContentPart{{Kind: domain.PartText, Text: "apply a safe migration"}}},
		ObservationIDs: []domain.ID{"obs-episode"}, StartedAt: base, EndedAt: base.Add(time.Hour),
	}
	outcomes := []domain.Outcome{
		{
			ID: "outcome-review", EpisodeID: episode.ID, Source: "reviewer-a", Kind: "review", Value: 1,
			Evidence:   domain.Content{Parts: []domain.ContentPart{{Kind: domain.PartText, Text: "review passed"}}},
			ObservedAt: base.Add(time.Hour),
		},
		{
			ID: "outcome-test", EpisodeID: episode.ID, Source: "ci-system", Kind: "test", Value: 1,
			Evidence:   domain.Content{Parts: []domain.ContentPart{{Kind: domain.PartText, Text: "tests passed"}}},
			ObservedAt: base.Add(time.Hour),
		},
	}
	if err := store.PutEpisode(ctx, episode, outcomes); err != nil {
		t.Fatal(err)
	}
	create := application.MemoryCreate{
		Scope: scope, Function: domain.FunctionExperiential,
		Taxonomy: domain.Taxonomy{
			Forms: []string{"token-flat"}, Functions: []string{"experiential"}, Dynamics: []string{"formation", "evolution", "retrieval"},
		},
		Payload: domain.MemoryPayload{Experiential: &domain.ExperientialMemory{
			Lesson:   domain.Content{Parts: []domain.ContentPart{{Kind: domain.PartText, Text: "verify before switching traffic"}}},
			Evidence: []domain.LessonEvidence{{EpisodeID: episode.ID, OutcomeIDs: []domain.ID{"outcome-review", "outcome-test"}}},
		}},
		Provenance: []domain.EvidenceRef{{ObservationID: "obs-episode", PartIndex: 0}},
		ValidTime:  &domain.TimeRange{From: base},
	}
	memory, version, _, err := store.ApplyProposal(ctx,
		proposal("proposal-experiential", application.ProposalCreate, "", create, base), "actor-test", base.Add(2*time.Hour))
	if err != nil || memory.Function != domain.FunctionExperiential {
		t.Fatalf("memory=%+v version=%+v err=%v", memory, version, err)
	}
	var payloadBefore string
	if err := store.db.QueryRow("SELECT payload_json FROM memory_versions WHERE id = ?", version.ID).Scan(&payloadBefore); err != nil {
		t.Fatal(err)
	}
	trace := domain.RetrievalTrace{
		ID: "trace-feedback", Scope: scope, Query: "migration lesson",
		StrategyID: "retrieval.lexical@1.0.0", StrategyHash: strings.Repeat("2", 64),
		Authorization: domain.RetrievalAuthorization{PrincipalID: "actor-test", PrivateOwners: []domain.ID{"owner-alice"}},
		ValidAt:       base, SystemAt: base.Add(3 * time.Hour), TokenBudget: 10,
		StartedAt: base.Add(3 * time.Hour), CompletedAt: base.Add(3 * time.Hour),
	}
	if err := store.PutRetrievalTrace(ctx, trace); err != nil {
		t.Fatal(err)
	}
	feedback := domain.UsageFeedback{
		TraceID: trace.ID, Source: "reviewer-a", Value: 0.8, RecordedAt: base.Add(4 * time.Hour),
	}
	if err := store.RecordUsageFeedback(ctx, version.ID, feedback); err != nil {
		t.Fatal(err)
	}
	got, err := store.UsageFeedback(ctx, version.ID)
	if err != nil || len(got) != 1 || got[0].Value != 0.8 {
		t.Fatalf("feedback=%+v err=%v", got, err)
	}
	var payloadAfter string
	if err := store.db.QueryRow("SELECT payload_json FROM memory_versions WHERE id = ?", version.ID).Scan(&payloadAfter); err != nil {
		t.Fatal(err)
	}
	if payloadBefore != payloadAfter {
		t.Fatal("usage feedback silently rewrote lesson content")
	}

	invalid := create
	invalid.Payload.Experiential = &domain.ExperientialMemory{
		Lesson:   create.Payload.Experiential.Lesson,
		Evidence: []domain.LessonEvidence{{EpisodeID: episode.ID, OutcomeIDs: []domain.ID{"missing-outcome"}}},
	}
	if _, _, _, err := store.ApplyProposal(ctx,
		proposal("proposal-bad-lesson", application.ProposalCreate, "", invalid, base), "actor-test", base.Add(5*time.Hour)); err == nil {
		t.Fatal("lesson with missing outcome succeeded")
	}
}

func TestWorkingMemoryIsTaskScopedGlobalOptInAndTTLBounded(t *testing.T) {
	ctx := context.Background()
	store := testStore(t)
	base := time.Date(2026, 8, 8, 8, 0, 0, 0, time.UTC)
	expires := base.Add(time.Hour)
	scope := testObservation("obs-working-set", "working state").Scope
	scope.Context = "task-working"
	set := domain.WorkingSet{
		ID: "ws-working", Scope: scope, TaskID: "task-working",
		Goal:      domain.Content{Parts: []domain.ContentPart{{Kind: domain.PartText, Text: "finish the release"}}},
		ExpiresAt: &expires, CreatedAt: base, UpdatedAt: base,
	}
	if err := store.PutWorkingSet(ctx, set); err != nil {
		t.Fatal(err)
	}
	create := application.MemoryCreate{
		Scope: scope, Function: domain.FunctionWorking,
		Taxonomy: domain.Taxonomy{
			Forms: []string{"token-flat"}, Functions: []string{"working"}, Dynamics: []string{"formation", "evolution", "retrieval"},
		},
		Payload: domain.MemoryPayload{Working: &domain.WorkingMemory{
			WorkingSetID: set.ID, TaskID: set.TaskID, Goal: set.Goal,
			State:         domain.Content{Parts: []domain.ContentPart{{Kind: domain.PartText, Text: "working implementation in progress"}}},
			CompactedFrom: []domain.ID{"obs-working-set"},
		}},
		Provenance: []domain.EvidenceRef{{ObservationID: "obs-working-set", PartIndex: 0}},
		ValidTime:  &domain.TimeRange{From: base},
	}
	memory, _, _, err := store.ApplyProposal(ctx,
		proposal("proposal-working", application.ProposalCreate, "", create, base), "actor-test", base.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	filter := application.CandidateFilter{
		Access: application.AccessScope{
			PrincipalID: "owner-alice", Namespace: "ns-test", PrivateOwners: []domain.ID{"owner-alice"},
		},
		Subject: "subject-alice", Context: set.TaskID,
		ValidAt: base.Add(30 * time.Minute), SystemAt: base.Add(30 * time.Minute),
	}
	hits, err := store.SearchLexicalCandidates(ctx, filter, "working implementation", 10)
	if err != nil || len(hits) != 1 || hits[0].MemoryID != memory.ID {
		t.Fatalf("task hits=%+v err=%v", hits, err)
	}
	filter.Context = "task-other"
	hits, err = store.SearchLexicalCandidates(ctx, filter, "working implementation", 10)
	if err != nil || len(hits) != 0 {
		t.Fatalf("cross-task hits=%+v err=%v", hits, err)
	}
	filter.Context = ""
	filter.IncludeGlobalWorking = true
	hits, err = store.SearchLexicalCandidates(ctx, filter, "working implementation", 10)
	if err != nil || len(hits) != 0 {
		t.Fatalf("non-opted global hits=%+v err=%v", hits, err)
	}
	if err := store.SetWorkingGlobalRecall(ctx, "ns-test", set.TaskID, true, base.Add(40*time.Minute)); err != nil {
		t.Fatal(err)
	}
	hits, err = store.SearchLexicalCandidates(ctx, filter, "working implementation", 10)
	if err != nil || len(hits) != 1 {
		t.Fatalf("opted global hits=%+v err=%v", hits, err)
	}
	expired, err := store.ExpireWorkingSets(ctx, expires)
	if err != nil || len(expired) != 1 || expired[0] != set.ID {
		t.Fatalf("expired=%+v err=%v", expired, err)
	}
	filter.SystemAt = expires.Add(time.Second)
	filter.ValidAt = expires.Add(time.Second)
	hits, err = store.SearchLexicalCandidates(ctx, filter, "working implementation", 10)
	if err != nil || len(hits) != 0 {
		t.Fatalf("expired global hits=%+v err=%v", hits, err)
	}
	var state string
	if err := store.db.QueryRow("SELECT state FROM memories WHERE id = ?", memory.ID).Scan(&state); err != nil || state != "archived" {
		t.Fatalf("expired memory state=%q err=%v", state, err)
	}
}
