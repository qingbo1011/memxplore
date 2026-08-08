package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/qingbo1011/memxplore/internal/domain"
)

// PutObservation stores one immutable observation.
func (s *Store) PutObservation(ctx context.Context, observation domain.Observation) error {
	return insertObservation(ctx, s.db, observation, time.Now().UTC())
}

type contextExecutor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func insertObservation(ctx context.Context, executor contextExecutor, observation domain.Observation, storedAt time.Time) error {
	if err := observation.Validate(); err != nil {
		return fmt.Errorf("validate observation: %w", err)
	}
	content, err := json.Marshal(observation.Content)
	if err != nil {
		return fmt.Errorf("encode observation content: %w", err)
	}
	metadata, err := json.Marshal(observation.Metadata)
	if err != nil {
		return fmt.Errorf("encode observation metadata: %w", err)
	}
	_, err = executor.ExecContext(ctx, `
        INSERT INTO observations(
            id, namespace_id, owner_id, subject_id, actor_id, context_id, visibility,
            source_kind, source_reference, content_json, evidence_class, policy_authority,
            metadata_json, captured_at, stored_at
        ) VALUES(?, ?, ?, ?, ?, NULLIF(?, ''), ?, ?, NULLIF(?, ''), ?, ?, NULLIF(?, ''), ?, ?, ?)`,
		observation.ID, observation.Scope.Namespace, observation.Scope.Owner, observation.Scope.Subject,
		observation.Scope.Actor, observation.Scope.Context, observation.Scope.Visibility,
		observation.SourceKind, observation.SourceReference, string(content), observation.EvidenceClass,
		observation.PolicyAuthority, string(metadata), formatTime(observation.CapturedAt), formatTime(storedAt))
	if err != nil {
		return fmt.Errorf("insert observation: %w", err)
	}
	return nil
}

// PutMemory atomically stores a stable memory, its first version, dependencies, and FTS text.
func (s *Store) PutMemory(ctx context.Context, memory domain.Memory, version domain.MemoryVersion) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin memory insert: %w", err)
	}
	defer tx.Rollback()
	if err := insertMemoryRows(ctx, tx, memory, version); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit memory insert: %w", err)
	}
	return nil
}

func insertMemoryRows(ctx context.Context, executor contextExecutor, memory domain.Memory, version domain.MemoryVersion) error {
	if err := memory.Validate(); err != nil {
		return fmt.Errorf("validate memory: %w", err)
	}
	if version.MemoryID != memory.ID || version.Number != memory.CurrentVersion {
		return fmt.Errorf("memory and version identity do not agree")
	}
	if err := version.Validate(memory.Function); err != nil {
		return fmt.Errorf("validate memory version: %w", err)
	}
	taxonomy, payload, provenance, supersedes, derivedFrom, err := encodeVersion(version)
	if err != nil {
		return err
	}
	if _, err := executor.ExecContext(ctx, `
        INSERT INTO memories(
            id, namespace_id, owner_id, subject_id, actor_id, context_id, visibility,
            function, state, current_version, created_at
        ) VALUES(?, ?, ?, ?, ?, NULLIF(?, ''), ?, ?, ?, ?, ?)`,
		memory.ID, memory.Scope.Namespace, memory.Scope.Owner, memory.Scope.Subject, memory.Scope.Actor,
		memory.Scope.Context, memory.Scope.Visibility, memory.Function, memory.State, memory.CurrentVersion,
		formatTime(memory.CreatedAt)); err != nil {
		return fmt.Errorf("insert memory: %w", err)
	}
	if _, err := executor.ExecContext(ctx, `
        INSERT INTO memory_versions(
            id, memory_id, version_number, state, taxonomy_json, payload_json, provenance_json,
            supersedes_json, derived_from_json, valid_from, valid_to, system_from, system_to,
            conflict_group_id, created_at
        ) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULLIF(?, ''), ?)`,
		version.ID, version.MemoryID, version.Number, version.State, string(taxonomy), string(payload), string(provenance),
		string(supersedes), string(derivedFrom), formatTime(version.ValidTime.From), nullableTime(version.ValidTime.To),
		formatTime(version.SystemTime.From), nullableTime(version.SystemTime.To), version.ConflictGroup,
		formatTime(version.SystemTime.From)); err != nil {
		return fmt.Errorf("insert memory version: %w", err)
	}
	for _, parentID := range version.DerivedFrom {
		if _, err := executor.ExecContext(ctx,
			"INSERT INTO memory_dependencies(parent_version_id, child_version_id) VALUES(?, ?)", parentID, version.ID); err != nil {
			return fmt.Errorf("insert memory dependency: %w", err)
		}
	}
	if _, err := executor.ExecContext(ctx,
		"INSERT INTO memory_fts(memory_version_id, memory_id, namespace_id, text_content) VALUES(?, ?, ?, ?)",
		version.ID, memory.ID, memory.Scope.Namespace, payloadPlainText(version.Payload)); err != nil {
		return fmt.Errorf("index memory version: %w", err)
	}
	return nil
}

func encodeVersion(version domain.MemoryVersion) (taxonomy, payload, provenance, supersedes, derivedFrom []byte, err error) {
	values := []any{version.Taxonomy, version.Payload, version.Provenance, version.Supersedes, version.DerivedFrom}
	encoded := make([][]byte, len(values))
	for index, value := range values {
		encoded[index], err = json.Marshal(value)
		if err != nil {
			return nil, nil, nil, nil, nil, fmt.Errorf("encode memory version field %d: %w", index, err)
		}
	}
	return encoded[0], encoded[1], encoded[2], encoded[3], encoded[4], nil
}

func payloadPlainText(payload domain.MemoryPayload) string {
	switch {
	case payload.Factual != nil:
		return payload.Factual.Predicate + "\n" + payload.Factual.Object.PlainText()
	case payload.Experiential != nil:
		return payload.Experiential.Lesson.PlainText()
	case payload.Working != nil:
		return payload.Working.Goal.PlainText() + "\n" + payload.Working.State.PlainText()
	default:
		return ""
	}
}

func formatTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}

func nullableTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return formatTime(*value)
}

// LexicalHit is a raw FTS5/BM25 baseline result; lower BM25 is better.
type LexicalHit struct {
	MemoryID  domain.ID
	VersionID domain.ID
	BM25      float64
}

// SearchLexical performs namespace-filtered FTS5/BM25 retrieval.
func (s *Store) SearchLexical(ctx context.Context, namespace domain.ID, query string, limit int) ([]LexicalHit, error) {
	if namespace == "" || query == "" || limit < 1 || limit > 1000 {
		return nil, fmt.Errorf("namespace, query, and limit within [1,1000] are required")
	}
	rows, err := s.db.QueryContext(ctx, `
        SELECT memory_id, memory_version_id, bm25(memory_fts, 0.0, 0.0, 0.0, 1.0)
        FROM memory_fts
        WHERE memory_fts MATCH ? AND namespace_id = ?
        ORDER BY bm25(memory_fts, 0.0, 0.0, 0.0, 1.0), memory_version_id
        LIMIT ?`, query, namespace, limit)
	if err != nil {
		return nil, fmt.Errorf("search lexical memory: %w", err)
	}
	defer rows.Close()
	var hits []LexicalHit
	for rows.Next() {
		var hit LexicalHit
		if err := rows.Scan(&hit.MemoryID, &hit.VersionID, &hit.BM25); err != nil {
			return nil, fmt.Errorf("scan lexical hit: %w", err)
		}
		hits = append(hits, hit)
	}
	return hits, rows.Err()
}

// ObservationCount is used by validation and backup tests.
func (s *Store) ObservationCount(ctx context.Context) (int, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM observations").Scan(&count); err != nil {
		return 0, fmt.Errorf("count observations: %w", err)
	}
	return count, nil
}
