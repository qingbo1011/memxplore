package sqlite

import (
	"context"
	"fmt"
	"time"

	"github.com/qingbo1011/memxplore/internal/domain"
)

// PurgeReceipt is deliberately non-content-bearing.
type PurgeReceipt struct {
	ID                domain.ID
	Namespace         domain.ID
	TargetID          domain.ID
	Actor             domain.ID
	VersionsDeleted   int
	ArtifactsDetached int
	PurgedAt          time.Time
}

// PurgeMemory irreversibly removes content, dependencies, and FTS rows and writes a non-content receipt.
func (s *Store) PurgeMemory(ctx context.Context, receiptID, namespace, actor, memoryID domain.ID, purgedAt time.Time) (PurgeReceipt, error) {
	receipt := PurgeReceipt{ID: receiptID, Namespace: namespace, TargetID: memoryID, Actor: actor, PurgedAt: purgedAt.UTC()}
	if receiptID == "" || namespace == "" || actor == "" || memoryID == "" || purgedAt.IsZero() {
		return receipt, fmt.Errorf("purge requires receipt, namespace, actor, target, and time")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return receipt, fmt.Errorf("begin purge: %w", err)
	}
	defer tx.Rollback()
	if err := tx.QueryRowContext(ctx,
		"SELECT count(*) FROM memory_versions v JOIN memories m ON m.id = v.memory_id WHERE m.id = ? AND m.namespace_id = ?",
		memoryID, namespace).Scan(&receipt.VersionsDeleted); err != nil {
		return receipt, fmt.Errorf("count purge versions: %w", err)
	}
	if receipt.VersionsDeleted == 0 {
		return receipt, fmt.Errorf("memory not found in namespace")
	}
	if err := tx.QueryRowContext(ctx, `
        SELECT count(*) FROM artifact_references
        WHERE owner_type = 'memory' AND owner_id = ?`, memoryID).Scan(&receipt.ArtifactsDetached); err != nil {
		return receipt, fmt.Errorf("count purge artifacts: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM memory_fts WHERE memory_id = ? AND namespace_id = ?", memoryID, namespace); err != nil {
		return receipt, fmt.Errorf("purge search index: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM artifact_references WHERE owner_type = 'memory' AND owner_id = ?", memoryID); err != nil {
		return receipt, fmt.Errorf("detach purge artifacts: %w", err)
	}
	result, err := tx.ExecContext(ctx, "DELETE FROM memories WHERE id = ? AND namespace_id = ?", memoryID, namespace)
	if err != nil {
		return receipt, fmt.Errorf("purge memory: %w", err)
	}
	deleted, err := result.RowsAffected()
	if err != nil || deleted != 1 {
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
