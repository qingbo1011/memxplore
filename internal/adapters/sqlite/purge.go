package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/qingbo1011/memxplore/internal/domain"
)

// PurgeMode controls how derived content is handled.
type PurgeMode string

const (
	// PurgeCascade physically removes transitively derived memories.
	PurgeCascade PurgeMode = "cascade"
	// PurgeMarkStale removes only the target and excludes descendants pending rebuild.
	PurgeMarkStale PurgeMode = "mark-stale"
)

// PurgeReceipt is deliberately non-content-bearing.
type PurgeReceipt struct {
	ID                domain.ID `json:"id"`
	Namespace         domain.ID `json:"namespace"`
	TargetID          domain.ID `json:"target_id"`
	Actor             domain.ID `json:"actor"`
	VersionsDeleted   int       `json:"versions_deleted"`
	ArtifactsDetached int       `json:"artifacts_detached"`
	PurgedAt          time.Time `json:"purged_at"`
}

// PurgeMemory irreversibly removes content, dependencies, and FTS rows and writes a non-content receipt.
func (s *Store) PurgeMemory(ctx context.Context, receiptID, namespace, actor, memoryID domain.ID, purgedAt time.Time) (PurgeReceipt, error) {
	return s.PurgeMemoryWithMode(ctx, receiptID, namespace, actor, memoryID, PurgeCascade, purgedAt)
}

// PurgeMemoryWithMode performs a cascading privacy purge or an explicit research rebuild workflow.
func (s *Store) PurgeMemoryWithMode(ctx context.Context, receiptID, namespace, actor, memoryID domain.ID, mode PurgeMode, purgedAt time.Time) (PurgeReceipt, error) {
	receipt := PurgeReceipt{ID: receiptID, Namespace: namespace, TargetID: memoryID, Actor: actor, PurgedAt: purgedAt.UTC()}
	if receiptID == "" || namespace == "" || actor == "" || memoryID == "" || purgedAt.IsZero() {
		return receipt, fmt.Errorf("purge requires receipt, namespace, actor, target, and time")
	}
	if mode != PurgeCascade && mode != PurgeMarkStale {
		return receipt, fmt.Errorf("purge mode %q is invalid", mode)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return receipt, fmt.Errorf("begin purge: %w", err)
	}
	defer tx.Rollback()
	memoryIDs, descendantVersions, err := purgeTargets(ctx, tx, namespace, memoryID)
	if err != nil {
		return receipt, err
	}
	if len(memoryIDs) == 0 {
		return receipt, fmt.Errorf("memory not found in namespace")
	}
	if mode == PurgeMarkStale {
		memoryIDs = memoryIDs[:1]
		if len(descendantVersions) > 0 {
			placeholders := strings.TrimRight(strings.Repeat("?,", len(descendantVersions)), ",")
			args := make([]any, len(descendantVersions))
			for index := range descendantVersions {
				args[index] = descendantVersions[index]
			}
			if _, err := tx.ExecContext(ctx,
				"UPDATE memory_versions SET state = 'stale' WHERE id IN ("+placeholders+") AND state = 'current'", args...); err != nil {
				return receipt, fmt.Errorf("mark purge descendants stale: %w", err)
			}
		}
	}
	placeholders := strings.TrimRight(strings.Repeat("?,", len(memoryIDs)), ",")
	args := make([]any, len(memoryIDs))
	for index := range memoryIDs {
		args[index] = memoryIDs[index]
	}
	if err := tx.QueryRowContext(ctx,
		"SELECT count(*) FROM memory_versions WHERE memory_id IN ("+placeholders+")", args...).Scan(&receipt.VersionsDeleted); err != nil {
		return receipt, fmt.Errorf("count purge versions: %w", err)
	}
	if err := tx.QueryRowContext(ctx,
		"SELECT count(*) FROM artifact_references WHERE owner_type = 'memory' AND owner_id IN ("+placeholders+")", args...).Scan(&receipt.ArtifactsDetached); err != nil {
		return receipt, fmt.Errorf("count purge artifacts: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM memory_fts WHERE memory_id IN ("+placeholders+") AND namespace_id = ?", append(args, namespace)...); err != nil {
		return receipt, fmt.Errorf("purge search index: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM artifact_references WHERE owner_type = 'memory' AND owner_id IN ("+placeholders+")", args...); err != nil {
		return receipt, fmt.Errorf("detach purge artifacts: %w", err)
	}
	result, err := tx.ExecContext(ctx, "DELETE FROM memories WHERE id IN ("+placeholders+") AND namespace_id = ?", append(args, namespace)...)
	if err != nil {
		return receipt, fmt.Errorf("purge memory: %w", err)
	}
	deleted, err := result.RowsAffected()
	if err != nil || deleted != int64(len(memoryIDs)) {
		return receipt, fmt.Errorf("purge removed %d memory rows", deleted)
	}
	if _, err := tx.ExecContext(ctx, `
        INSERT INTO purge_receipts(
            id, namespace_id, target_type, target_id, actor_id,
            versions_deleted, artifacts_detached, purged_at
        ) VALUES(?, ?, 'memory', ?, ?, ?, ?, ?)`,
		receipt.ID, receipt.Namespace, receipt.TargetID, receipt.Actor,
		receipt.VersionsDeleted, receipt.ArtifactsDetached, formatTime(receipt.PurgedAt)); err != nil {
		return receipt, fmt.Errorf("insert purge receipt: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return receipt, fmt.Errorf("commit purge: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		return receipt, fmt.Errorf("truncate purge WAL: %w", err)
	}
	return receipt, nil
}

func purgeTargets(ctx context.Context, tx *sql.Tx, namespace, memoryID domain.ID) ([]domain.ID, []domain.ID, error) {
	rows, err := tx.QueryContext(ctx, `
		WITH RECURSIVE version_tree(id) AS (
			SELECT v.id
			FROM memory_versions v JOIN memories m ON m.id = v.memory_id
			WHERE m.id = ? AND m.namespace_id = ?
			UNION
			SELECT d.child_version_id
			FROM memory_dependencies d JOIN version_tree ON d.parent_version_id = version_tree.id
		)
		SELECT DISTINCT m.id, v.id, CASE WHEN m.id = ? THEN 0 ELSE 1 END AS derived
        FROM version_tree
        JOIN memory_versions v ON v.id = version_tree.id
        JOIN memories m ON m.id = v.memory_id
        WHERE m.namespace_id = ?
		ORDER BY derived, m.id, v.id`, memoryID, namespace, memoryID, namespace)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve purge dependency closure: %w", err)
	}
	defer rows.Close()
	var memories []domain.ID
	var descendants []domain.ID
	seen := make(map[domain.ID]struct{})
	for rows.Next() {
		var foundMemory, versionID domain.ID
		var depth int
		if err := rows.Scan(&foundMemory, &versionID, &depth); err != nil {
			return nil, nil, err
		}
		if _, exists := seen[foundMemory]; !exists {
			seen[foundMemory] = struct{}{}
			memories = append(memories, foundMemory)
		}
		if depth > 0 {
			descendants = append(descendants, versionID)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	return memories, descendants, nil
}
