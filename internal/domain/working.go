package domain

import (
	"fmt"
	"time"
)

// WorkingSet is recoverable, task-scoped state excluded from global recall by default.
type WorkingSet struct {
	ID           ID         `json:"id"`
	Scope        Scope      `json:"scope"`
	TaskID       ID         `json:"task_id"`
	Goal         Content    `json:"goal"`
	GlobalRecall bool       `json:"global_recall"`
	ExpiresAt    *time.Time `json:"expires_at,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

// WorkingMemory is a compacted state version anchored to a working set and goal.
type WorkingMemory struct {
	WorkingSetID  ID      `json:"working_set_id"`
	TaskID        ID      `json:"task_id"`
	Goal          Content `json:"goal"`
	State         Content `json:"state"`
	CompactedFrom []ID    `json:"compacted_from,omitempty"`
}

// Validate checks task scoping and TTL.
func (w WorkingSet) Validate() error {
	if err := validateID("working_set.id", w.ID, true); err != nil {
		return err
	}
	if err := w.Scope.Validate(); err != nil {
		return err
	}
	if err := validateID("working_set.task_id", w.TaskID, true); err != nil {
		return err
	}
	if err := w.Goal.Validate(); err != nil {
		return err
	}
	if w.CreatedAt.IsZero() || w.UpdatedAt.Before(w.CreatedAt) {
		return fmt.Errorf("working set timestamps are invalid")
	}
	if w.ExpiresAt != nil && !w.ExpiresAt.After(w.CreatedAt) {
		return fmt.Errorf("working set expiry must be after creation")
	}
	return nil
}

// Validate ensures compaction retains a task and explicit goal.
func (w WorkingMemory) Validate() error {
	if err := validateID("working.working_set_id", w.WorkingSetID, true); err != nil {
		return err
	}
	if err := validateID("working.task_id", w.TaskID, true); err != nil {
		return err
	}
	if err := w.Goal.Validate(); err != nil {
		return fmt.Errorf("working.goal: %w", err)
	}
	if err := w.State.Validate(); err != nil {
		return fmt.Errorf("working.state: %w", err)
	}
	return nil
}
