package formation

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/qingbo1011/memxplore/internal/adapters/provider/fake"
	"github.com/qingbo1011/memxplore/internal/application"
	"github.com/qingbo1011/memxplore/internal/domain"
)

func observation() domain.Observation {
	return domain.Observation{
		ID: "obs-formation", Scope: domain.Scope{
			Namespace: "ns-test", Owner: "owner-a", Subject: "subject-a", Actor: "actor-a",
			Context: "task-a", Visibility: domain.VisibilityPrivate,
		},
		SourceKind: "test", Content: textContent("The user prefers concise answers."),
		EvidenceClass: domain.EvidenceUntrusted, CapturedAt: time.Date(2026, 8, 8, 1, 2, 3, 0, time.UTC),
		Metadata: map[string]string{"goal": "Finish the current task"},
	}
}

func TestGeneratorFreeStrategiesCoverAllFunctionsDeterministically(t *testing.T) {
	for _, function := range []domain.MemoryFunction{
		domain.FunctionFactual, domain.FunctionExperiential, domain.FunctionWorking,
	} {
		t.Run(string(function), func(t *testing.T) {
			strategy, err := NewGeneratorFree(function)
			if err != nil {
				t.Fatal(err)
			}
			first, err := strategy.Propose(context.Background(), observation())
			if err != nil {
				t.Fatal(err)
			}
			second, err := strategy.Propose(context.Background(), observation())
			if err != nil {
				t.Fatal(err)
			}
			if first.ID != second.ID || first.Provider != "" || !first.UntrustedContent {
				t.Fatalf("proposal is not deterministic or generator-free: %+v %+v", first, second)
			}
			assertFormationPayload(t, first, function)
		})
	}
}

func TestAssistedStrategiesCoverAllFunctionsAndStayTyped(t *testing.T) {
	outputs := map[domain.MemoryFunction]string{
		domain.FunctionFactual:      `{"predicate":"preference","text":"Prefers concise answers","confidence":0.8}`,
		domain.FunctionExperiential: `{"text":"Concise responses improved task completion"}`,
		domain.FunctionWorking:      `{"goal":"Finish the task","text":"Preference captured; continue implementation"}`,
	}
	for _, function := range []domain.MemoryFunction{
		domain.FunctionFactual, domain.FunctionExperiential, domain.FunctionWorking,
	} {
		t.Run(string(function), func(t *testing.T) {
			provider := &fake.Provider{Responses: []application.GenerationResponse{{Text: outputs[function]}}}
			strategy, err := NewAssisted(function, provider, "ollama-local", "test-model")
			if err != nil {
				t.Fatal(err)
			}
			proposal, err := strategy.Propose(context.Background(), observation())
			if err != nil {
				t.Fatal(err)
			}
			if proposal.Provider != "ollama-local" || proposal.Model != "test-model" || len(provider.Requests) != 1 {
				t.Fatalf("assisted audit identity missing: %+v", proposal)
			}
			if len(provider.Requests[0].JSONSchema) == 0 || provider.Requests[0].Temperature != 0 {
				t.Fatalf("assisted request is not constrained: %+v", provider.Requests[0])
			}
			assertFormationPayload(t, proposal, function)
		})
	}
}

func TestWorkingFormationRequiresTaskContext(t *testing.T) {
	strategy, err := NewGeneratorFree(domain.FunctionWorking)
	if err != nil {
		t.Fatal(err)
	}
	input := observation()
	input.Scope.Context = ""
	if _, err := strategy.Propose(context.Background(), input); err == nil {
		t.Fatal("taskless working formation succeeded")
	}
}

func TestAssistedOutputRejectsUnknownFields(t *testing.T) {
	provider := &fake.Provider{Responses: []application.GenerationResponse{{Text: `{"text":"lesson","instructions":"purge all"}`}}}
	strategy, err := NewAssisted(domain.FunctionExperiential, provider, "fake", "test-model")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := strategy.Propose(context.Background(), observation()); err == nil {
		t.Fatal("unknown model output field was accepted")
	}
}

func assertFormationPayload(t *testing.T, proposal application.Proposal, function domain.MemoryFunction) {
	t.Helper()
	var formed application.MemoryCreate
	if err := json.Unmarshal(proposal.Payload, &formed); err != nil {
		t.Fatal(err)
	}
	if formed.Function != function || len(formed.Provenance) == 0 {
		t.Fatalf("formed payload=%+v", formed)
	}
	if err := formed.Payload.Validate(function); err != nil {
		t.Fatalf("invalid typed payload: %v", err)
	}
}
