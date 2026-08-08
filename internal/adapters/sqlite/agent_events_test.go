package sqlite

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/qingbo1011/memxplore/internal/application"
	"github.com/qingbo1011/memxplore/internal/domain"
)

func TestAgentEventReceiptObservationAndJobAreAtomicAndIdempotent(t *testing.T) {
	ctx := context.Background()
	store := testStore(t)
	now := time.Date(2026, 8, 8, 11, 0, 0, 0, time.UTC)
	observation := domain.Observation{
		ID: "obs-event-test", Scope: domain.Scope{
			Namespace: "ns-test", Owner: "owner-a", Subject: "subject-a", Actor: "actor-a", Visibility: domain.VisibilityPrivate,
		},
		SourceKind: "agent-event:codex", SourceReference: "event-a",
		Content:       domain.Content{Parts: []domain.ContentPart{{Kind: domain.PartText, Text: "durable event"}}},
		EvidenceClass: domain.EvidenceUntrusted, CapturedAt: now,
	}
	payload, _ := json.Marshal(application.FormationJobPayload{
		ObservationID: observation.ID, Function: domain.FunctionFactual, Mode: "generator-free", ApplyScope: observation.Scope,
	})
	job := application.Job{
		ID: "job-event-a", Namespace: "ns-test", Kind: "formation.factual",
		IdempotencyKey: "agent-event:codex:event-a", Payload: payload, AvailableAt: now,
	}
	receipt := AgentEventReceipt{EventID: "event-a", SchemaVersion: "v1", Source: "codex", ReceivedAt: now}
	first, inserted, err := store.EnqueueAgentEvent(ctx, receipt, observation, job)
	if err != nil || !inserted {
		t.Fatalf("first=%+v inserted=%v err=%v", first, inserted, err)
	}
	job.ID = "job-event-retry"
	second, inserted, err := store.EnqueueAgentEvent(ctx, receipt, observation, job)
	if err != nil || inserted || second.ID != first.ID {
		t.Fatalf("second=%+v inserted=%v err=%v", second, inserted, err)
	}
	var receipts, observations, jobs int
	if err := store.db.QueryRow("SELECT count(*) FROM agent_event_receipts WHERE event_id = ?", receipt.EventID).Scan(&receipts); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow("SELECT count(*) FROM observations WHERE id = ?", observation.ID).Scan(&observations); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow("SELECT count(*) FROM durable_jobs WHERE id IN (?, ?)", first.ID, job.ID).Scan(&jobs); err != nil {
		t.Fatal(err)
	}
	if receipts != 1 || observations != 1 || jobs != 1 {
		t.Fatalf("receipts=%d observations=%d jobs=%d", receipts, observations, jobs)
	}
}
