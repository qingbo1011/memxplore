package sqlite

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/qingbo1011/memxplore/internal/application"
	"github.com/qingbo1011/memxplore/internal/domain"
)

// ApplyProposal atomically mutates lifecycle state and records the apply operation.
func (s *Store) ApplyProposal(ctx context.Context, proposal application.Proposal, actor domain.ID, at time.Time) (domain.Memory, domain.MemoryVersion, domain.Operation, error) {
	if err := proposal.Validate(); err != nil {
		return domain.Memory{}, domain.MemoryVersion{}, domain.Operation{}, err
	}
	if actor == "" || at.IsZero() {
		return domain.Memory{}, domain.MemoryVersion{}, domain.Operation{}, fmt.Errorf("apply actor and timestamp are required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Memory{}, domain.MemoryVersion{}, domain.Operation{}, fmt.Errorf("begin lifecycle apply: %w", err)
	}
	defer tx.Rollback()
	if memory, version, operation, found, err := loadAppliedProposal(ctx, tx, proposal.ID); err != nil {
		return domain.Memory{}, domain.MemoryVersion{}, domain.Operation{}, err
	} else if found {
		return memory, version, operation, nil
	}

	var memory domain.Memory
	var version domain.MemoryVersion
	switch proposal.Kind {
	case application.ProposalCreate:
		memory, version, err = applyCreate(ctx, tx, proposal, at)
	case application.ProposalUpdate, application.ProposalConsolidate:
		memory, version, err = applyEvolution(ctx, tx, proposal, at)
	case application.ProposalArchive:
		memory, version, err = applyState(ctx, tx, proposal, domain.MemoryArchived, domain.VersionArchived, false)
	case application.ProposalForget:
		memory, version, err = applyState(ctx, tx, proposal, domain.MemoryForgotten, domain.VersionForgotten, true)
	default:
		err = fmt.Errorf("proposal kind %q is not applicable", proposal.Kind)
	}
	if err != nil {
		return domain.Memory{}, domain.MemoryVersion{}, domain.Operation{}, err
	}
	operation := domain.Operation{
		ID: lifecycleID("op", string(proposal.ID)), Phase: domain.PhaseApply,
		Kind: string(proposal.Kind), Actor: actor, TargetID: memory.ID, ProposalID: proposal.ID,
		StrategyID: proposal.StrategyID, OccurredAt: at.UTC(), Result: "applied",
	}
	if err := insertOperation(ctx, tx, operation); err != nil {
		return domain.Memory{}, domain.MemoryVersion{}, domain.Operation{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.Memory{}, domain.MemoryVersion{}, domain.Operation{}, fmt.Errorf("commit lifecycle apply: %w", err)
	}
	return memory, version, operation, nil
}

func applyCreate(ctx context.Context, tx *sql.Tx, proposal application.Proposal, at time.Time) (domain.Memory, domain.MemoryVersion, error) {
	var create application.MemoryCreate
	if err := decodeStrict(proposal.Payload, &create); err != nil {
		return domain.Memory{}, domain.MemoryVersion{}, fmt.Errorf("decode create proposal: %w", err)
	}
	if err := create.Validate(); err != nil {
		return domain.Memory{}, domain.MemoryVersion{}, err
	}
	if create.Scope.Namespace != proposal.Namespace {
		return domain.Memory{}, domain.MemoryVersion{}, fmt.Errorf("create proposal namespace mismatch")
	}
	validTime := domain.TimeRange{From: proposal.CreatedAt.UTC()}
	if create.ValidTime != nil {
		validTime = *create.ValidTime
	}
	memory := domain.Memory{
		ID: lifecycleID("mem", string(proposal.ID)), Scope: create.Scope, Function: create.Function,
		State: domain.MemoryActive, CurrentVersion: 1, CreatedAt: at.UTC(),
	}
	version := domain.MemoryVersion{
		ID: lifecycleID("mv", string(proposal.ID)), MemoryID: memory.ID, Number: 1, State: domain.VersionCurrent,
		Taxonomy: create.Taxonomy, ValidTime: validTime, SystemTime: domain.TimeRange{From: at.UTC()},
		ConflictGroup: create.ConflictGroup, DerivedFrom: append([]domain.ID(nil), create.DerivedFrom...),
		Provenance: append([]domain.EvidenceRef(nil), create.Provenance...), Payload: create.Payload,
	}
	if err := validateDependencies(ctx, tx, proposal.Namespace, "", version.DerivedFrom); err != nil {
		return domain.Memory{}, domain.MemoryVersion{}, err
	}
	if err := validatePayloadReferences(ctx, tx, proposal.Namespace, memory.Scope.Context, at, version.Payload); err != nil {
		return domain.Memory{}, domain.MemoryVersion{}, err
	}
	if err := insertMemoryRows(ctx, tx, memory, version); err != nil {
		return domain.Memory{}, domain.MemoryVersion{}, err
	}
	return memory, version, nil
}

func applyEvolution(ctx context.Context, tx *sql.Tx, proposal application.Proposal, at time.Time) (domain.Memory, domain.MemoryVersion, error) {
	memory, current, err := loadCurrentMemory(ctx, tx, proposal.TargetID)
	if err != nil {
		return domain.Memory{}, domain.MemoryVersion{}, err
	}
	if memory.Scope.Namespace != proposal.Namespace {
		return domain.Memory{}, domain.MemoryVersion{}, fmt.Errorf("evolution proposal namespace mismatch")
	}
	if memory.State != domain.MemoryActive {
		return domain.Memory{}, domain.MemoryVersion{}, fmt.Errorf("only active memory can evolve")
	}
	var evolution application.MemoryEvolution
	if err := decodeStrict(proposal.Payload, &evolution); err != nil {
		return domain.Memory{}, domain.MemoryVersion{}, fmt.Errorf("decode evolution proposal: %w", err)
	}
	if err := evolution.Validate(memory.Function); err != nil {
		return domain.Memory{}, domain.MemoryVersion{}, err
	}
	if proposal.Kind == application.ProposalConsolidate && evolution.Mode != application.EvolutionRebuild {
		return domain.Memory{}, domain.MemoryVersion{}, fmt.Errorf("consolidation proposal requires rebuild mode")
	}
	if err := validateDependencies(ctx, tx, proposal.Namespace, current.ID, evolution.DerivedFrom); err != nil {
		return domain.Memory{}, domain.MemoryVersion{}, err
	}
	if err := validatePayloadReferences(ctx, tx, proposal.Namespace, memory.Scope.Context, at, evolution.Payload); err != nil {
		return domain.Memory{}, domain.MemoryVersion{}, err
	}
	if evolution.Mode == application.EvolutionConflict {
		return applyConflict(ctx, tx, proposal, memory, current, evolution, at)
	}
	if !at.After(current.SystemTime.From) {
		return domain.Memory{}, domain.MemoryVersion{}, fmt.Errorf("evolution system time must advance")
	}
	if _, err := tx.ExecContext(ctx, `
        UPDATE memory_versions
        SET state = 'superseded', system_to = ?,
            valid_to = CASE
                WHEN ? > valid_from AND (valid_to IS NULL OR ? < valid_to) THEN ?
                ELSE valid_to
            END
        WHERE id = ? AND state IN ('current', 'stale')`,
		formatTime(at), formatTime(evolution.ValidTime.From), formatTime(evolution.ValidTime.From),
		formatTime(evolution.ValidTime.From), current.ID); err != nil {
		return domain.Memory{}, domain.MemoryVersion{}, fmt.Errorf("close previous memory version: %w", err)
	}
	if err := markDescendantsStale(ctx, tx, current.ID); err != nil {
		return domain.Memory{}, domain.MemoryVersion{}, err
	}
	version := domain.MemoryVersion{
		ID: lifecycleID("mv", string(proposal.ID)), MemoryID: memory.ID,
		Number: current.Number + 1, State: domain.VersionCurrent, Taxonomy: evolution.Taxonomy,
		ValidTime: evolution.ValidTime, SystemTime: domain.TimeRange{From: at.UTC()},
		ConflictGroup: evolution.ConflictGroup, Supersedes: []domain.ID{current.ID},
		DerivedFrom: append([]domain.ID(nil), evolution.DerivedFrom...),
		Provenance:  append([]domain.EvidenceRef(nil), evolution.Provenance...), Payload: evolution.Payload,
	}
	if err := insertVersionRows(ctx, tx, memory, version); err != nil {
		return domain.Memory{}, domain.MemoryVersion{}, err
	}
	if _, err := tx.ExecContext(ctx,
		"UPDATE memories SET current_version = ?, state = 'active' WHERE id = ?", version.Number, memory.ID); err != nil {
		return domain.Memory{}, domain.MemoryVersion{}, fmt.Errorf("advance memory version: %w", err)
	}
	memory.CurrentVersion = version.Number
	memory.State = domain.MemoryActive
	return memory, version, nil
}

func applyConflict(ctx context.Context, tx *sql.Tx, proposal application.Proposal, memory domain.Memory, current domain.MemoryVersion, evolution application.MemoryEvolution, at time.Time) (domain.Memory, domain.MemoryVersion, error) {
	group := evolution.ConflictGroup
	if current.ConflictGroup != "" {
		if group != "" && group != current.ConflictGroup {
			return domain.Memory{}, domain.MemoryVersion{}, fmt.Errorf("conflict evolution cannot replace an existing conflict group")
		}
		group = current.ConflictGroup
	}
	if group == "" {
		group = lifecycleID("conflict", string(memory.ID))
	}
	if _, err := tx.ExecContext(ctx,
		"UPDATE memory_versions SET conflict_group_id = ? WHERE id = ?", group, current.ID); err != nil {
		return domain.Memory{}, domain.MemoryVersion{}, fmt.Errorf("group current conflict: %w", err)
	}
	sibling := memory
	sibling.ID = lifecycleID("mem", string(proposal.ID), "conflict")
	sibling.CurrentVersion = 1
	sibling.CreatedAt = at.UTC()
	version := domain.MemoryVersion{
		ID: lifecycleID("mv", string(proposal.ID), "conflict"), MemoryID: sibling.ID,
		Number: 1, State: domain.VersionCurrent, Taxonomy: evolution.Taxonomy,
		ValidTime: evolution.ValidTime, SystemTime: domain.TimeRange{From: at.UTC()}, ConflictGroup: group,
		DerivedFrom: append([]domain.ID(nil), evolution.DerivedFrom...),
		Provenance:  append([]domain.EvidenceRef(nil), evolution.Provenance...), Payload: evolution.Payload,
	}
	if err := insertMemoryRows(ctx, tx, sibling, version); err != nil {
		return domain.Memory{}, domain.MemoryVersion{}, err
	}
	return sibling, version, nil
}

func applyState(ctx context.Context, tx *sql.Tx, proposal application.Proposal, memoryState domain.MemoryState, versionState domain.VersionState, removeIndexes bool) (domain.Memory, domain.MemoryVersion, error) {
	if !bytes.Equal(bytes.TrimSpace(proposal.Payload), []byte("{}")) {
		return domain.Memory{}, domain.MemoryVersion{}, fmt.Errorf("state proposal payload must be an empty JSON object")
	}
	var empty struct{}
	if err := decodeStrict(proposal.Payload, &empty); err != nil {
		return domain.Memory{}, domain.MemoryVersion{}, fmt.Errorf("decode state proposal: %w", err)
	}
	memory, version, err := loadCurrentMemory(ctx, tx, proposal.TargetID)
	if err != nil {
		return domain.Memory{}, domain.MemoryVersion{}, err
	}
	if memory.Scope.Namespace != proposal.Namespace {
		return domain.Memory{}, domain.MemoryVersion{}, fmt.Errorf("state proposal namespace mismatch")
	}
	if _, err := tx.ExecContext(ctx, "UPDATE memories SET state = ? WHERE id = ?", memoryState, memory.ID); err != nil {
		return domain.Memory{}, domain.MemoryVersion{}, fmt.Errorf("update memory state: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "UPDATE memory_versions SET state = ? WHERE id = ?", versionState, version.ID); err != nil {
		return domain.Memory{}, domain.MemoryVersion{}, fmt.Errorf("update version state: %w", err)
	}
	if removeIndexes {
		if _, err := tx.ExecContext(ctx, "DELETE FROM memory_fts WHERE memory_id = ?", memory.ID); err != nil {
			return domain.Memory{}, domain.MemoryVersion{}, fmt.Errorf("remove forgotten lexical index: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
            DELETE FROM memory_embeddings
            WHERE memory_version_id IN (SELECT id FROM memory_versions WHERE memory_id = ?)`, memory.ID); err != nil {
			return domain.Memory{}, domain.MemoryVersion{}, fmt.Errorf("remove forgotten embeddings: %w", err)
		}
	}
	memory.State = memoryState
	version.State = versionState
	return memory, version, nil
}

func insertVersionRows(ctx context.Context, executor contextExecutor, memory domain.Memory, version domain.MemoryVersion) error {
	if err := version.Validate(memory.Function); err != nil {
		return fmt.Errorf("validate memory version: %w", err)
	}
	taxonomy, payload, provenance, supersedes, derivedFrom, err := encodeVersion(version)
	if err != nil {
		return err
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

func validateDependencies(ctx context.Context, tx *sql.Tx, namespace domain.ID, targetVersion domain.ID, parents []domain.ID) error {
	seen := make(map[domain.ID]struct{}, len(parents))
	for _, parent := range parents {
		if parent == "" {
			return fmt.Errorf("dependency parent is empty")
		}
		if _, duplicate := seen[parent]; duplicate {
			return fmt.Errorf("duplicate dependency parent %s", parent)
		}
		seen[parent] = struct{}{}
		var parentNamespace domain.ID
		if err := tx.QueryRowContext(ctx, `
            SELECT m.namespace_id
            FROM memory_versions v JOIN memories m ON m.id = v.memory_id
            WHERE v.id = ?`, parent).Scan(&parentNamespace); err != nil {
			return fmt.Errorf("load dependency parent %s: %w", parent, err)
		}
		if parentNamespace != namespace {
			return fmt.Errorf("dependency parent %s crosses namespace", parent)
		}
		if targetVersion != "" {
			var cycle int
			if err := tx.QueryRowContext(ctx, `
                WITH RECURSIVE descendants(id) AS (
                    SELECT child_version_id FROM memory_dependencies WHERE parent_version_id = ?
                    UNION
                    SELECT d.child_version_id
                    FROM memory_dependencies d JOIN descendants x ON d.parent_version_id = x.id
                )
                SELECT EXISTS(SELECT 1 FROM descendants WHERE id = ?)`, targetVersion, parent).Scan(&cycle); err != nil {
				return fmt.Errorf("check dependency cycle: %w", err)
			}
			if cycle != 0 || parent == targetVersion {
				return fmt.Errorf("dependency would create a cycle")
			}
		}
	}
	return nil
}

func validatePayloadReferences(ctx context.Context, tx *sql.Tx, namespace, memoryContext domain.ID, at time.Time, payload domain.MemoryPayload) error {
	switch {
	case payload.Experiential != nil:
		return validateExperientialReferences(ctx, tx, namespace, *payload.Experiential)
	case payload.Working != nil:
		if memoryContext != payload.Working.TaskID {
			return fmt.Errorf("working payload task does not match memory context")
		}
		return validateWorkingReference(ctx, tx, namespace, at, *payload.Working)
	default:
		return nil
	}
}

func markDescendantsStale(ctx context.Context, tx *sql.Tx, parent domain.ID) error {
	if _, err := tx.ExecContext(ctx, `
        WITH RECURSIVE descendants(id) AS (
            SELECT child_version_id FROM memory_dependencies WHERE parent_version_id = ?
            UNION
            SELECT d.child_version_id
            FROM memory_dependencies d JOIN descendants x ON d.parent_version_id = x.id
        )
        UPDATE memory_versions SET state = 'stale'
        WHERE id IN (SELECT id FROM descendants) AND state = 'current'`, parent); err != nil {
		return fmt.Errorf("mark derived memories stale: %w", err)
	}
	return nil
}

func insertOperation(ctx context.Context, tx *sql.Tx, operation domain.Operation) error {
	_, err := tx.ExecContext(ctx, `
        INSERT INTO operations(id, phase, kind, actor_id, target_id, proposal_id, strategy_id, occurred_at, result)
        VALUES(?, ?, ?, ?, ?, ?, NULLIF(?, ''), ?, ?)`,
		operation.ID, operation.Phase, operation.Kind, operation.Actor, operation.TargetID,
		operation.ProposalID, operation.StrategyID, formatTime(operation.OccurredAt), operation.Result)
	if err != nil {
		return fmt.Errorf("insert lifecycle operation: %w", err)
	}
	return nil
}

func loadAppliedProposal(ctx context.Context, tx *sql.Tx, proposalID domain.ID) (domain.Memory, domain.MemoryVersion, domain.Operation, bool, error) {
	var operation domain.Operation
	var phase string
	var occurred string
	err := tx.QueryRowContext(ctx, `
        SELECT id, phase, kind, actor_id, target_id, proposal_id, COALESCE(strategy_id, ''), occurred_at, result
        FROM operations WHERE phase = 'apply' AND proposal_id = ?`, proposalID).Scan(
		&operation.ID, &phase, &operation.Kind, &operation.Actor, &operation.TargetID,
		&operation.ProposalID, &operation.StrategyID, &occurred, &operation.Result)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Memory{}, domain.MemoryVersion{}, domain.Operation{}, false, nil
	}
	if err != nil {
		return domain.Memory{}, domain.MemoryVersion{}, domain.Operation{}, false, fmt.Errorf("load applied proposal: %w", err)
	}
	operation.Phase = domain.OperationPhase(phase)
	operation.OccurredAt, err = parseStoredTime(occurred)
	if err != nil {
		return domain.Memory{}, domain.MemoryVersion{}, domain.Operation{}, false, err
	}
	memory, version, err := loadCurrentMemory(ctx, tx, operation.TargetID)
	return memory, version, operation, err == nil, err
}

func loadCurrentMemory(ctx context.Context, tx *sql.Tx, memoryID domain.ID) (domain.Memory, domain.MemoryVersion, error) {
	row := tx.QueryRowContext(ctx, `
        SELECT m.id, m.namespace_id, m.owner_id, m.subject_id, m.actor_id, COALESCE(m.context_id, ''),
               m.visibility, m.function, m.state, m.current_version, m.created_at,
               v.id, v.version_number, v.state, v.taxonomy_json, v.payload_json, v.provenance_json,
               v.supersedes_json, v.derived_from_json, v.valid_from, v.valid_to,
               v.system_from, v.system_to, COALESCE(v.conflict_group_id, '')
        FROM memories m JOIN memory_versions v
          ON v.memory_id = m.id AND v.version_number = m.current_version
        WHERE m.id = ?`, memoryID)
	return scanMemoryVersion(row)
}

func scanMemoryVersion(row rowScanner) (domain.Memory, domain.MemoryVersion, error) {
	var memory domain.Memory
	var version domain.MemoryVersion
	var contextID, visibility, function, memoryState, created string
	var versionState, taxonomyJSON, payloadJSON, provenanceJSON, supersedesJSON, derivedJSON string
	var validFrom, systemFrom, conflict string
	var validTo, systemTo sql.NullString
	if err := row.Scan(
		&memory.ID, &memory.Scope.Namespace, &memory.Scope.Owner, &memory.Scope.Subject, &memory.Scope.Actor, &contextID,
		&visibility, &function, &memoryState, &memory.CurrentVersion, &created,
		&version.ID, &version.Number, &versionState, &taxonomyJSON, &payloadJSON, &provenanceJSON,
		&supersedesJSON, &derivedJSON, &validFrom, &validTo, &systemFrom, &systemTo, &conflict); err != nil {
		return domain.Memory{}, domain.MemoryVersion{}, err
	}
	memory.Scope.Context = domain.ID(contextID)
	memory.Scope.Visibility = domain.Visibility(visibility)
	memory.Function = domain.MemoryFunction(function)
	memory.State = domain.MemoryState(memoryState)
	var err error
	memory.CreatedAt, err = parseStoredTime(created)
	if err != nil {
		return domain.Memory{}, domain.MemoryVersion{}, err
	}
	version.MemoryID = memory.ID
	version.State = domain.VersionState(versionState)
	version.ConflictGroup = domain.ID(conflict)
	if err := decodeJSONFields(
		[]string{taxonomyJSON, payloadJSON, provenanceJSON, supersedesJSON, derivedJSON},
		[]any{&version.Taxonomy, &version.Payload, &version.Provenance, &version.Supersedes, &version.DerivedFrom}); err != nil {
		return domain.Memory{}, domain.MemoryVersion{}, err
	}
	version.ValidTime.From, err = parseStoredTime(validFrom)
	if err != nil {
		return domain.Memory{}, domain.MemoryVersion{}, err
	}
	version.SystemTime.From, err = parseStoredTime(systemFrom)
	if err != nil {
		return domain.Memory{}, domain.MemoryVersion{}, err
	}
	if validTo.Valid {
		parsed, parseErr := parseStoredTime(validTo.String)
		if parseErr != nil {
			return domain.Memory{}, domain.MemoryVersion{}, parseErr
		}
		version.ValidTime.To = &parsed
	}
	if systemTo.Valid {
		parsed, parseErr := parseStoredTime(systemTo.String)
		if parseErr != nil {
			return domain.Memory{}, domain.MemoryVersion{}, parseErr
		}
		version.SystemTime.To = &parsed
	}
	return memory, version, nil
}

func decodeJSONFields(values []string, targets []any) error {
	for index := range values {
		if err := json.Unmarshal([]byte(values[index]), targets[index]); err != nil {
			return fmt.Errorf("decode memory field %d: %w", index, err)
		}
	}
	return nil
}

func decodeStrict(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("trailing JSON is not allowed")
	}
	return nil
}

func lifecycleID(prefix string, values ...string) domain.ID {
	hash := sha256.New()
	for _, value := range values {
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(value))
	}
	return domain.ID(prefix + "-" + hex.EncodeToString(hash.Sum(nil)[:12]))
}

var _ application.LifecycleRepository = (*Store)(nil)
