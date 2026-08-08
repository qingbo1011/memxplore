package domain

import (
	"strings"
	"testing"
	"time"
)

func testScope() Scope {
	return Scope{
		Namespace:  "ns-test",
		Owner:      "owner-alice",
		Subject:    "subject-alice",
		Actor:      "actor-agent",
		Context:    "ctx-task",
		Visibility: VisibilityPrivate,
	}
}

func textContent(value string) Content {
	return Content{Parts: []ContentPart{{Kind: PartText, Text: value}}}
}

func TestObservationPolicyRequiresAuthority(t *testing.T) {
	observation := Observation{
		ID: "obs-1", Scope: testScope(), SourceKind: "user", Content: textContent("remember this"),
		EvidenceClass: EvidencePolicy, CapturedAt: time.Now(),
	}
	if err := observation.Validate(); err == nil || !strings.Contains(err.Error(), "policy_authority") {
		t.Fatalf("Validate() error = %v, want policy authority failure", err)
	}
	observation.PolicyAuthority = "principal:admin"
	if err := observation.Validate(); err != nil {
		t.Fatalf("Validate() with authority: %v", err)
	}
}

func TestTypedContentRejectsAmbiguity(t *testing.T) {
	content := Content{Parts: []ContentPart{{
		Kind: PartText, Text: "hello", Artifact: &ArtifactRef{Digest: "sha256:" + strings.Repeat("0", 64), MediaType: "text/plain"},
	}}}
	if err := content.Validate(); err == nil {
		t.Fatal("Validate() accepted text plus artifact")
	}
}

func TestTaxonomyAllowsValidatedExtension(t *testing.T) {
	taxonomy := Taxonomy{
		Forms: []string{"token-flat", "x-symbolic-tuple"}, Functions: []string{"factual"},
		Dynamics: []string{"formation", "retrieval"}, Tags: []string{"provenance"},
	}
	if err := taxonomy.Validate(); err != nil {
		t.Fatalf("Validate(): %v", err)
	}
	taxonomy.Forms = append(taxonomy.Forms, "custom")
	if err := taxonomy.Validate(); err == nil {
		t.Fatal("Validate() accepted an unregistered extension")
	}
}

func TestFactualMemoryVersionIsBitemporalAndProvenanced(t *testing.T) {
	now := time.Now().UTC()
	confidence := 0.8
	version := MemoryVersion{
		ID: "mv-1", MemoryID: "mem-1", Number: 1, State: VersionCurrent,
		Taxonomy:  Taxonomy{Forms: []string{"token-flat"}, Functions: []string{"factual"}, Dynamics: []string{"formation"}},
		ValidTime: TimeRange{From: now.Add(-time.Hour)}, SystemTime: TimeRange{From: now},
		ConflictGroup: "conflict-color", Provenance: []EvidenceRef{{ObservationID: "obs-1", PartIndex: 0}},
		Payload: MemoryPayload{Factual: &FactualMemory{
			ClaimSubject: "subject-alice", Predicate: "favorite-color", Object: textContent("blue"),
			Epistemic: EpistemicAsserted, Confidence: &confidence,
		}},
	}
	if err := version.Validate(FunctionFactual); err != nil {
		t.Fatalf("Validate(): %v", err)
	}
	if err := version.Validate(FunctionExperiential); err == nil {
		t.Fatal("Validate() accepted payload for the wrong function")
	}
}

func TestWorkingSetIsTaskScopedByDefault(t *testing.T) {
	now := time.Now().UTC()
	expiry := now.Add(time.Hour)
	set := WorkingSet{
		ID: "ws-1", Scope: testScope(), TaskID: "task-1", Goal: textContent("ship safely"),
		ExpiresAt: &expiry, CreatedAt: now, UpdatedAt: now,
	}
	if set.GlobalRecall {
		t.Fatal("zero-value working set unexpectedly enables global recall")
	}
	if err := set.Validate(); err != nil {
		t.Fatalf("Validate(): %v", err)
	}
}

func TestExperientialLessonRequiresOutcomeEvidence(t *testing.T) {
	lesson := ExperientialMemory{
		Lesson:   textContent("verify the migration before switching traffic"),
		Evidence: []LessonEvidence{{EpisodeID: "episode-1"}},
	}
	if err := lesson.Validate(); err == nil {
		t.Fatal("Validate() accepted lesson without outcome evidence")
	}
	lesson.Evidence[0].OutcomeIDs = []ID{"outcome-review", "outcome-test"}
	if err := lesson.Validate(); err != nil {
		t.Fatalf("Validate(): %v", err)
	}
}
