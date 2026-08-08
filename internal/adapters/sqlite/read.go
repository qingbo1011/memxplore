package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/qingbo1011/memxplore/internal/application"
	"github.com/qingbo1011/memxplore/internal/domain"
)

// GetObservation loads immutable captured evidence for durable workers.
func (s *Store) GetObservation(ctx context.Context, id domain.ID) (domain.Observation, error) {
	var observation domain.Observation
	var contextID, visibility, contentJSON, evidenceClass, capturedAt, metadataJSON string
	err := s.db.QueryRowContext(ctx, `
        SELECT id, namespace_id, owner_id, subject_id, actor_id, COALESCE(context_id, ''),
               visibility, source_kind, COALESCE(source_reference, ''), content_json,
               evidence_class, COALESCE(policy_authority, ''), captured_at, metadata_json
        FROM observations WHERE id = ?`, id).Scan(
		&observation.ID, &observation.Scope.Namespace, &observation.Scope.Owner, &observation.Scope.Subject,
		&observation.Scope.Actor, &contextID, &visibility, &observation.SourceKind,
		&observation.SourceReference, &contentJSON, &evidenceClass, &observation.PolicyAuthority,
		&capturedAt, &metadataJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Observation{}, application.ErrNotFound
	}
	if err != nil {
		return domain.Observation{}, fmt.Errorf("get observation: %w", err)
	}
	observation.Scope.Context = domain.ID(contextID)
	observation.Scope.Visibility = domain.Visibility(visibility)
	observation.EvidenceClass = domain.EvidenceClass(evidenceClass)
	if err := json.Unmarshal([]byte(contentJSON), &observation.Content); err != nil {
		return domain.Observation{}, err
	}
	if err := json.Unmarshal([]byte(metadataJSON), &observation.Metadata); err != nil {
		return domain.Observation{}, err
	}
	observation.CapturedAt, err = parseStoredTime(capturedAt)
	if err != nil {
		return domain.Observation{}, err
	}
	if err := observation.Validate(); err != nil {
		return domain.Observation{}, fmt.Errorf("validate stored observation: %w", err)
	}
	return observation, nil
}

// GetMemory loads stable identity and its current version for authorization and evolution.
func (s *Store) GetMemory(ctx context.Context, id domain.ID) (domain.Memory, domain.MemoryVersion, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return domain.Memory{}, domain.MemoryVersion{}, err
	}
	defer tx.Rollback()
	memory, version, err := loadCurrentMemory(ctx, tx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Memory{}, domain.MemoryVersion{}, application.ErrNotFound
	}
	if err != nil {
		return domain.Memory{}, domain.MemoryVersion{}, err
	}
	return memory, version, nil
}
