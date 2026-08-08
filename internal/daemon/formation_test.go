package daemon

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/qingbo1011/memxplore/internal/adapters/provider/fake"
	"github.com/qingbo1011/memxplore/internal/adapters/sqlite"
	"github.com/qingbo1011/memxplore/internal/application"
	"github.com/qingbo1011/memxplore/internal/domain"
)

func daemonStore(t *testing.T) *sqlite.Store {
	t.Helper()
	store, err := sqlite.Open(context.Background(), t.TempDir()+"/daemon.sqlite", sqlite.DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func daemonObservation(id string, function domain.MemoryFunction, now time.Time) domain.Observation {
	contextID := domain.ID("task-test")
	if function != domain.FunctionWorking {
		contextID = "context-test"
	}
	return domain.Observation{
		ID: domain.ID(id), Scope: domain.Scope{
			Namespace: "ns-test", Owner: "owner-a", Subject: "subject-a", Actor: "actor-a",
			Context: contextID, Visibility: domain.VisibilityPrivate,
		},
		SourceKind: "test", Content: domain.Content{Parts: []domain.ContentPart{{Kind: domain.PartText, Text: "remember durable content"}}},
		EvidenceClass: domain.EvidenceUntrusted, CapturedAt: now,
	}
}

func TestFormationWorkerCompletesAllFunctionalJobsAndEmbeds(t *testing.T) {
	ctx := context.Background()
	store := daemonStore(t)
	now := time.Date(2026, 8, 8, 11, 0, 0, 0, time.UTC)
	provider := &fake.Provider{}
	worker, err := NewFormationWorker(FormationConfig{
		Store: store, Embedder: provider, EmbeddingProvider: "fake", EmbeddingModel: "fake-v1",
		EmbeddingDimensions: 8, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, function := range []domain.MemoryFunction{domain.FunctionFactual, domain.FunctionExperiential, domain.FunctionWorking} {
		t.Run(string(function), func(t *testing.T) {
			observation := daemonObservation("obs-"+string(function), function, now)
			expires := now.Add(time.Hour)
			payload, err := application.EncodeFormationJob(application.FormationJobPayload{
				ObservationID: observation.ID, Function: function, Mode: "generator-free",
				ApplyScope: observation.Scope, WorkingExpiresAt: &expires,
			})
			if err != nil {
				t.Fatal(err)
			}
			job := application.Job{
				ID: domain.ID("job-" + string(function)), Namespace: observation.Scope.Namespace,
				Kind: "formation." + string(function), IdempotencyKey: "remember-" + string(function),
				Payload: payload, AvailableAt: now,
			}
			if _, inserted, err := store.EnqueueObservation(ctx, observation, job); err != nil || !inserted {
				t.Fatalf("inserted=%v err=%v", inserted, err)
			}
			if err := worker.ProcessOne(ctx, "worker-test"); err != nil {
				t.Fatal(err)
			}
			finished, err := store.Get(ctx, job.ID)
			if err != nil || finished.State != application.JobSucceeded {
				t.Fatalf("job=%+v err=%v", finished, err)
			}
			var result application.FormationJobResult
			if err := json.Unmarshal(finished.Result, &result); err != nil || result.MemoryID == "" || result.VersionID == "" {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			memory, version, err := store.GetMemory(ctx, result.MemoryID)
			if err != nil || memory.Function != function || version.ID != result.VersionID {
				t.Fatalf("memory=%+v version=%+v err=%v", memory, version, err)
			}
			filter := application.CandidateFilter{
				Access:  application.AccessScope{PrincipalID: "actor-a", Namespace: "ns-test", PrivateOwners: []domain.ID{"owner-a"}},
				Subject: "subject-a", Context: observation.Scope.Context, ValidAt: now, SystemAt: now,
			}
			semantic, err := store.ListSemanticCandidates(ctx, filter, "fake", "fake-v1", 10)
			if err != nil || len(semantic) == 0 {
				t.Fatalf("semantic=%+v err=%v", semantic, err)
			}
		})
	}
}

func TestAssistedWorkerUsesConfiguredGenerator(t *testing.T) {
	ctx := context.Background()
	store := daemonStore(t)
	now := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	provider := &fake.Provider{Responses: []application.GenerationResponse{{
		Text: `{"predicate":"preference","text":"concise answers","confidence":0.9}`,
	}}}
	worker, err := NewFormationWorker(FormationConfig{
		Store: store, Generator: provider, GeneratorProvider: "fake", GeneratorModel: "fake-v1",
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	observation := daemonObservation("obs-assisted", domain.FunctionFactual, now)
	payload, _ := application.EncodeFormationJob(application.FormationJobPayload{
		ObservationID: observation.ID, Function: domain.FunctionFactual, Mode: "assisted", ApplyScope: observation.Scope,
	})
	job := application.Job{
		ID: "job-assisted", Namespace: "ns-test", Kind: "formation.factual",
		IdempotencyKey: "assisted", Payload: payload, AvailableAt: now,
	}
	if _, _, err := store.EnqueueObservation(ctx, observation, job); err != nil {
		t.Fatal(err)
	}
	if err := worker.ProcessOne(ctx, "worker-test"); err != nil {
		t.Fatal(err)
	}
	if len(provider.Requests) != 1 {
		t.Fatalf("generator requests=%d", len(provider.Requests))
	}
}
