package agentevent

import (
	"testing"
	"time"

	"github.com/qingbo1011/memxplore/internal/domain"
)

func TestCodexAdapterProducesUntrustedVersionedObservation(t *testing.T) {
	data := []byte(`{"id":"event-1","type":"assistant_message","timestamp":"2026-08-08T10:00:00Z","thread_id":"thread-1","turn_id":"turn-1","role":"assistant","text":"Implemented the migration"}`)
	event, err := ParseCodexJSON(data, "owner-a", "subject-a")
	if err != nil {
		t.Fatal(err)
	}
	observation, err := event.Observation("ns-test", "actor-agent", domain.VisibilityPrivate)
	if err != nil {
		t.Fatal(err)
	}
	if event.SchemaVersion != SchemaV1 || event.Source != "codex" || observation.EvidenceClass != domain.EvidenceUntrusted || observation.Scope.Context != "thread-1" {
		t.Fatalf("event=%+v observation=%+v", event, observation)
	}
	if observation.CapturedAt != time.Date(2026, 8, 8, 10, 0, 0, 0, time.UTC) {
		t.Fatalf("captured_at=%v", observation.CapturedAt)
	}
}
