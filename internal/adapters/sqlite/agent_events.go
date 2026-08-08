package sqlite

import (
	"context"
	"fmt"
	"time"

	"github.com/qingbo1011/memxplore/internal/application"
	"github.com/qingbo1011/memxplore/internal/domain"
)

// AgentEventReceipt identifies one atomically accepted external event without copying its content.
type AgentEventReceipt struct {
	EventID       domain.ID
	SchemaVersion string
	Source        string
	ReceivedAt    time.Time
}

// EnqueueAgentEvent atomically persists an event receipt, observation, and durable formation job.
func (s *Store) EnqueueAgentEvent(ctx context.Context, receipt AgentEventReceipt, observation domain.Observation, job application.Job) (application.Job, bool, error) {
	if receipt.EventID == "" || receipt.SchemaVersion == "" || receipt.Source == "" || receipt.ReceivedAt.IsZero() {
		return application.Job{}, false, fmt.Errorf("AgentEvent receipt requires event id, schema, source, and receive time")
	}
	return s.enqueueObservation(ctx, observation, job, &receipt)
}
