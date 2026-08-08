package evolution

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/qingbo1011/memxplore/internal/adapters/provider/fake"
	"github.com/qingbo1011/memxplore/internal/application"
	"github.com/qingbo1011/memxplore/internal/domain"
)

func fixtures(function domain.MemoryFunction) (domain.Memory, domain.MemoryVersion, domain.Observation) {
	now := time.Date(2026, 8, 8, 6, 0, 0, 0, time.UTC)
	scope := domain.Scope{
		Namespace: "ns-test", Owner: "owner-a", Subject: "subject-a", Actor: "actor-a",
		Context: "task-a", Visibility: domain.VisibilityPrivate,
	}
	memory := domain.Memory{ID: "mem-test", Scope: scope, Function: function, State: domain.MemoryActive, CurrentVersion: 1, CreatedAt: now}
	version := domain.MemoryVersion{
		ID: "mv-test", MemoryID: memory.ID, Number: 1, State: domain.VersionCurrent,
		Taxonomy:  domain.Taxonomy{Forms: []string{"token-flat"}, Functions: []string{string(function)}, Dynamics: []string{"formation", "evolution"}},
		ValidTime: domain.TimeRange{From: now}, SystemTime: domain.TimeRange{From: now},
		Provenance: []domain.EvidenceRef{{ObservationID: "obs-old", PartIndex: 0}},
	}
	old := textContent("old state")
	switch function {
	case domain.FunctionFactual:
		version.Payload = domain.MemoryPayload{Factual: &domain.FactualMemory{
			ClaimSubject: scope.Subject, Predicate: "preference", Object: old, Epistemic: domain.EpistemicObserved,
		}}
	case domain.FunctionExperiential:
		version.Payload = domain.MemoryPayload{Experiential: &domain.ExperientialMemory{
			Lesson: old, Evidence: []domain.LessonEvidence{{EpisodeID: "episode-old", OutcomeIDs: []domain.ID{"outcome-old"}}},
		}}
	case domain.FunctionWorking:
		version.Payload = domain.MemoryPayload{Working: &domain.WorkingMemory{
			WorkingSetID: "ws-test", TaskID: scope.Context, Goal: textContent("finish task"), State: old,
			CompactedFrom: []domain.ID{"obs-old"},
		}}
	}
	observation := domain.Observation{
		ID: "obs-new", Scope: scope, SourceKind: "test", Content: textContent("new state"),
		EvidenceClass: domain.EvidenceUntrusted, CapturedAt: now.Add(time.Hour),
	}
	return memory, version, observation
}

func TestGeneratorFreeEvolutionCoversAllFunctions(t *testing.T) {
	for _, function := range []domain.MemoryFunction{domain.FunctionFactual, domain.FunctionExperiential, domain.FunctionWorking} {
		t.Run(string(function), func(t *testing.T) {
			memory, version, observation := fixtures(function)
			strategy, err := NewGeneratorFree(function)
			if err != nil {
				t.Fatal(err)
			}
			proposal, err := strategy.Propose(context.Background(), memory, version, observation)
			if err != nil {
				t.Fatal(err)
			}
			var evolution application.MemoryEvolution
			if err := json.Unmarshal(proposal.Payload, &evolution); err != nil {
				t.Fatal(err)
			}
			if proposal.Kind != application.ProposalUpdate || evolution.Mode != application.EvolutionSupersede || evolution.Payload.Validate(function) != nil {
				t.Fatalf("proposal=%+v evolution=%+v", proposal, evolution)
			}
		})
	}
}

func TestFactualConflictAndNoChange(t *testing.T) {
	memory, version, observation := fixtures(domain.FunctionFactual)
	strategy, _ := NewGeneratorFree(domain.FunctionFactual)
	observation.Metadata = map[string]string{"evolution": "conflict"}
	proposal, err := strategy.Propose(context.Background(), memory, version, observation)
	if err != nil {
		t.Fatal(err)
	}
	var evolution application.MemoryEvolution
	if err := json.Unmarshal(proposal.Payload, &evolution); err != nil || evolution.Mode != application.EvolutionConflict {
		t.Fatalf("evolution=%+v err=%v", evolution, err)
	}
	observation.Content = version.Payload.Factual.Object
	if _, err := strategy.Propose(context.Background(), memory, version, observation); !errors.Is(err, application.ErrNoChange) {
		t.Fatalf("no-change err=%v", err)
	}
}

func TestAssistedEvolutionCoversAllFunctions(t *testing.T) {
	outputs := map[domain.MemoryFunction]string{
		domain.FunctionFactual:      `{"mode":"conflict","predicate":"preference","text":"new fact","confidence":0.7}`,
		domain.FunctionExperiential: `{"mode":"supersede","text":"new lesson"}`,
		domain.FunctionWorking:      `{"mode":"supersede","goal":"finish safely","text":"new compact state"}`,
	}
	for _, function := range []domain.MemoryFunction{domain.FunctionFactual, domain.FunctionExperiential, domain.FunctionWorking} {
		t.Run(string(function), func(t *testing.T) {
			memory, version, observation := fixtures(function)
			provider := &fake.Provider{Responses: []application.GenerationResponse{{Text: outputs[function]}}}
			strategy, err := NewAssisted(function, provider, "fake", "fake-v1")
			if err != nil {
				t.Fatal(err)
			}
			proposal, err := strategy.Propose(context.Background(), memory, version, observation)
			if err != nil {
				t.Fatal(err)
			}
			if proposal.Provider != "fake" || len(provider.Requests) != 1 || len(provider.Requests[0].JSONSchema) == 0 {
				t.Fatalf("proposal=%+v requests=%+v", proposal, provider.Requests)
			}
		})
	}
}
