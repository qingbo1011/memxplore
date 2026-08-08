package domain

import (
	"fmt"
	"time"
)

// Episode records a bounded task trajectory without forcing it into one text blob.
type Episode struct {
	ID             ID        `json:"id"`
	Scope          Scope     `json:"scope"`
	Task           Content   `json:"task"`
	ObservationIDs []ID      `json:"observation_ids"`
	StartedAt      time.Time `json:"started_at"`
	EndedAt        time.Time `json:"ended_at"`
}

// Outcome records feedback from an independent source.
type Outcome struct {
	ID         ID        `json:"id"`
	EpisodeID  ID        `json:"episode_id"`
	Source     ID        `json:"source"`
	Kind       string    `json:"kind"`
	Value      float64   `json:"value"`
	Evidence   Content   `json:"evidence"`
	ObservedAt time.Time `json:"observed_at"`
}

// LessonEvidence identifies the episode/outcome basis for a lesson.
type LessonEvidence struct {
	EpisodeID  ID   `json:"episode_id"`
	OutcomeIDs []ID `json:"outcome_ids"`
}

// UsageFeedback tracks whether a retrieved lesson helped, without silently rewriting it.
type UsageFeedback struct {
	TraceID    ID        `json:"trace_id"`
	Source     ID        `json:"source"`
	Value      float64   `json:"value"`
	RecordedAt time.Time `json:"recorded_at"`
}

// ExperientialMemory stores a reusable lesson and its evidence.
type ExperientialMemory struct {
	Lesson   Content          `json:"lesson"`
	Evidence []LessonEvidence `json:"evidence"`
	Feedback []UsageFeedback  `json:"feedback,omitempty"`
}

// Validate checks lesson evidence and feedback ranges.
func (e ExperientialMemory) Validate() error {
	if err := e.Lesson.Validate(); err != nil {
		return fmt.Errorf("experiential.lesson: %w", err)
	}
	if len(e.Evidence) == 0 {
		return fmt.Errorf("experiential.evidence requires at least one episode")
	}
	for _, evidence := range e.Evidence {
		if err := validateID("experiential.evidence.episode_id", evidence.EpisodeID, true); err != nil {
			return err
		}
		if len(evidence.OutcomeIDs) == 0 {
			return fmt.Errorf("experiential lesson evidence requires an outcome")
		}
	}
	for _, feedback := range e.Feedback {
		if err := validateID("experiential.feedback.trace_id", feedback.TraceID, true); err != nil {
			return err
		}
		if err := validateID("experiential.feedback.source", feedback.Source, true); err != nil {
			return err
		}
		if feedback.Value < -1 || feedback.Value > 1 {
			return fmt.Errorf("experiential feedback value must be within [-1,1]")
		}
		if feedback.RecordedAt.IsZero() {
			return fmt.Errorf("experiential feedback recorded_at is required")
		}
	}
	return nil
}

// Validate checks episode bounds and references.
func (e Episode) Validate() error {
	if err := validateID("episode.id", e.ID, true); err != nil {
		return err
	}
	if err := e.Scope.Validate(); err != nil {
		return err
	}
	if err := e.Task.Validate(); err != nil {
		return err
	}
	if e.StartedAt.IsZero() || !e.EndedAt.After(e.StartedAt) {
		return fmt.Errorf("episode requires ordered start and end times")
	}
	for _, observationID := range e.ObservationIDs {
		if err := validateID("episode.observation_id", observationID, true); err != nil {
			return err
		}
	}
	return nil
}

// Validate checks outcome identity, source, evidence, and time.
func (o Outcome) Validate() error {
	for field, id := range map[string]ID{"outcome.id": o.ID, "outcome.episode_id": o.EpisodeID, "outcome.source": o.Source} {
		if err := validateID(field, id, true); err != nil {
			return err
		}
	}
	if err := validateRequiredText("outcome.kind", o.Kind, 64); err != nil {
		return err
	}
	if err := o.Evidence.Validate(); err != nil {
		return err
	}
	if o.ObservedAt.IsZero() {
		return fmt.Errorf("outcome.observed_at is required")
	}
	return nil
}
