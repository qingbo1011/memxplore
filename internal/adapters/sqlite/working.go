package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/qingbo1011/memxplore/internal/domain"
)

// PutWorkingSet creates task-scoped working state, excluded from global recall by default.
func (s *Store) PutWorkingSet(ctx context.Context, set domain.WorkingSet) error {
	if err := set.Validate(); err != nil {
		return err
	}
	goal, err := json.Marshal(set.Goal)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
        INSERT INTO working_sets(
            id, namespace_id, owner_id, subject_id, actor_id, context_id, visibility,
            task_id, goal_json, global_recall, expires_at, created_at, updated_at, state
        ) VALUES(?, ?, ?, ?, ?, NULLIF(?, ''), ?, ?, ?, ?, ?, ?, ?, 'active')`,
		set.ID, set.Scope.Namespace, set.Scope.Owner, set.Scope.Subject, set.Scope.Actor, set.Scope.Context,
		set.Scope.Visibility, set.TaskID, string(goal), set.GlobalRecall, nullableTime(set.ExpiresAt),
		formatTime(set.CreatedAt), formatTime(set.UpdatedAt))
	if err != nil {
		return fmt.Errorf("put working set: %w", err)
	}
	return nil
}

// SetWorkingGlobalRecall explicitly changes global recall opt-in.
func (s *Store) SetWorkingGlobalRecall(ctx context.Context, namespace, taskID domain.ID, enabled bool, at time.Time) error {
	if namespace == "" || taskID == "" || at.IsZero() {
		return fmt.Errorf("working namespace, task, and timestamp are required")
	}
	result, err := s.db.ExecContext(ctx, `
        UPDATE working_sets SET global_recall = ?, updated_at = ?
        WHERE namespace_id = ? AND task_id = ? AND state = 'active'`,
		enabled, formatTime(at), namespace, taskID)
	if err != nil {
		return fmt.Errorf("set working global recall: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil || count != 1 {
		return fmt.Errorf("active working set not found")
	}
	return nil
}

// ExpireWorkingSets archives expired task memory and returns expired working-set IDs.
func (s *Store) ExpireWorkingSets(ctx context.Context, now time.Time) ([]domain.ID, error) {
	if now.IsZero() {
		return nil, fmt.Errorf("expiry timestamp is required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin working expiry: %w", err)
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `
        SELECT id, namespace_id, task_id FROM working_sets
        WHERE state = 'active' AND expires_at IS NOT NULL AND expires_at <= ?
        ORDER BY id`, formatTime(now))
	if err != nil {
		return nil, fmt.Errorf("list expired working sets: %w", err)
	}
	type expiredSet struct{ id, namespace, task domain.ID }
	var expired []expiredSet
	for rows.Next() {
		var item expiredSet
		if err := rows.Scan(&item.id, &item.namespace, &item.task); err != nil {
			rows.Close()
			return nil, err
		}
		expired = append(expired, item)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for _, item := range expired {
		if _, err := tx.ExecContext(ctx,
			"UPDATE working_sets SET state = 'expired', updated_at = ? WHERE id = ?", formatTime(now), item.id); err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `
            UPDATE memories SET state = 'archived'
            WHERE namespace_id = ? AND context_id = ? AND function = 'working'`, item.namespace, item.task); err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `
            UPDATE memory_versions SET state = 'archived'
            WHERE id IN (
                SELECT v.id FROM memory_versions v JOIN memories m ON m.id = v.memory_id
                WHERE m.namespace_id = ? AND m.context_id = ? AND m.function = 'working'
                  AND v.version_number = m.current_version
            )`, item.namespace, item.task); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit working expiry: %w", err)
	}
	ids := make([]domain.ID, len(expired))
	for index := range expired {
		ids[index] = expired[index].id
	}
	return ids, nil
}

func validateWorkingReference(ctx context.Context, query rowQuerier, namespace domain.ID, at time.Time, working domain.WorkingMemory) error {
	var setID, setNamespace, taskID domain.ID
	var state string
	var expiresAt sql.NullString
	if err := query.QueryRowContext(ctx, `
		SELECT id, namespace_id, task_id, state, expires_at FROM working_sets WHERE id = ?`, working.WorkingSetID).Scan(
		&setID, &setNamespace, &taskID, &state, &expiresAt); err != nil {
		return fmt.Errorf("load working set: %w", err)
	}
	if setNamespace != namespace || taskID != working.TaskID || state != "active" {
		return fmt.Errorf("working memory does not match an active task-scoped working set")
	}
	if expiresAt.Valid {
		expires, err := parseStoredTime(expiresAt.String)
		if err != nil {
			return err
		}
		if !expires.After(at) {
			return fmt.Errorf("working set has expired")
		}
	}
	return nil
}

type rowQuerier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}
