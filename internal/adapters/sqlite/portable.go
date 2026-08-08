package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/qingbo1011/memxplore/internal/application"
	"github.com/qingbo1011/memxplore/internal/domain"
)

// ImportResult reports validated rows, whether they were committed, and the mandatory pre-import backup.
type ImportResult struct {
	DryRun       bool   `json:"dry_run"`
	BackupPath   string `json:"backup_path,omitempty"`
	Observations int    `json:"observations"`
	Episodes     int    `json:"episodes"`
	Outcomes     int    `json:"outcomes"`
	WorkingSets  int    `json:"working_sets"`
	Memories     int    `json:"memories"`
	Versions     int    `json:"versions"`
}

// ExportSubject returns a deterministic, self-contained authorized subject bundle.
func (s *Store) ExportSubject(ctx context.Context, access application.AccessScope, subject domain.ID, at time.Time) (application.SubjectExport, error) {
	if at.IsZero() {
		return application.SubjectExport{}, fmt.Errorf("export timestamp is required")
	}
	where, args, err := portableAccessWhere("x", access, subject)
	if err != nil {
		return application.SubjectExport{}, err
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return application.SubjectExport{}, fmt.Errorf("begin subject export: %w", err)
	}
	defer tx.Rollback()

	export := application.SubjectExport{
		Format: application.PortableExportFormat, SchemaVersion: application.PortableExportSchemaVersion,
		ExportedAt: at.UTC(), Namespace: access.Namespace, Subject: subject,
		PrivateOwners: append([]domain.ID(nil), access.PrivateOwners...),
		IncludeShared: access.AllowShared, IncludePublic: access.AllowPublic,
	}
	if export.Observations, err = exportObservations(ctx, tx, where, args); err != nil {
		return application.SubjectExport{}, err
	}
	if export.WorkingSets, err = exportWorkingSets(ctx, tx, where, args); err != nil {
		return application.SubjectExport{}, err
	}
	if export.Memories, err = exportMemories(ctx, tx, where, args); err != nil {
		return application.SubjectExport{}, err
	}
	if export.Episodes, err = exportReferencedEpisodes(ctx, tx, export.Memories); err != nil {
		return application.SubjectExport{}, err
	}
	application.SortSubjectExport(&export)
	if err := export.Validate(); err != nil {
		return application.SubjectExport{}, fmt.Errorf("validate subject export: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return application.SubjectExport{}, fmt.Errorf("complete subject export: %w", err)
	}
	return export, nil
}

func portableAccessWhere(alias string, access application.AccessScope, subject domain.ID) (string, []any, error) {
	if access.Namespace == "" || access.PrincipalID == "" || subject == "" || len(access.PrivateOwners) == 0 {
		return "", nil, fmt.Errorf("complete authorized subject export scope is required")
	}
	placeholders := make([]string, len(access.PrivateOwners))
	args := []any{access.Namespace, subject}
	seen := make(map[domain.ID]struct{}, len(access.PrivateOwners))
	for index, owner := range access.PrivateOwners {
		if owner == "" {
			return "", nil, fmt.Errorf("private owner cannot be empty")
		}
		if _, duplicate := seen[owner]; duplicate {
			return "", nil, fmt.Errorf("duplicate private owner %s", owner)
		}
		seen[owner] = struct{}{}
		placeholders[index] = "?"
		args = append(args, owner)
	}
	args = append(args, access.AllowShared, access.AllowPublic)
	return fmt.Sprintf(`%s.namespace_id = ? AND %s.subject_id = ? AND
        ((%s.visibility = 'private' AND %s.owner_id IN (%s))
          OR (%s.visibility = 'shared' AND ? = 1)
          OR (%s.visibility = 'public' AND ? = 1))`, alias, alias, alias, alias,
		strings.Join(placeholders, ","), alias, alias), args, nil
}

func exportObservations(ctx context.Context, tx *sql.Tx, where string, args []any) ([]domain.Observation, error) {
	rows, err := tx.QueryContext(ctx, `
        SELECT x.id, x.namespace_id, x.owner_id, x.subject_id, x.actor_id, COALESCE(x.context_id, ''),
               x.visibility, x.source_kind, COALESCE(x.source_reference, ''), x.content_json,
               x.evidence_class, COALESCE(x.policy_authority, ''), x.captured_at, x.metadata_json
        FROM observations x WHERE `+where+` ORDER BY x.id`, args...)
	if err != nil {
		return nil, fmt.Errorf("list export observations: %w", err)
	}
	defer rows.Close()
	var result []domain.Observation
	for rows.Next() {
		var item domain.Observation
		var contextID, visibility, contentJSON, evidenceClass, capturedAt, metadataJSON string
		if err := rows.Scan(&item.ID, &item.Scope.Namespace, &item.Scope.Owner, &item.Scope.Subject,
			&item.Scope.Actor, &contextID, &visibility, &item.SourceKind, &item.SourceReference,
			&contentJSON, &evidenceClass, &item.PolicyAuthority, &capturedAt, &metadataJSON); err != nil {
			return nil, fmt.Errorf("scan export observation: %w", err)
		}
		item.Scope.Context = domain.ID(contextID)
		item.Scope.Visibility = domain.Visibility(visibility)
		item.EvidenceClass = domain.EvidenceClass(evidenceClass)
		if err := decodeJSONFields([]string{contentJSON, metadataJSON}, []any{&item.Content, &item.Metadata}); err != nil {
			return nil, err
		}
		item.CapturedAt, err = parseStoredTime(capturedAt)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func exportWorkingSets(ctx context.Context, tx *sql.Tx, where string, args []any) ([]application.PortableWorkingSet, error) {
	rows, err := tx.QueryContext(ctx, `
        SELECT x.id, x.namespace_id, x.owner_id, x.subject_id, x.actor_id, COALESCE(x.context_id, ''),
               x.visibility, x.task_id, x.goal_json, x.global_recall, x.expires_at,
               x.created_at, x.updated_at, x.state
        FROM working_sets x WHERE `+where+` ORDER BY x.id`, args...)
	if err != nil {
		return nil, fmt.Errorf("list export working sets: %w", err)
	}
	defer rows.Close()
	var result []application.PortableWorkingSet
	for rows.Next() {
		var item application.PortableWorkingSet
		var contextID, visibility, goalJSON, createdAt, updatedAt string
		var globalRecall int
		var expiresAt sql.NullString
		if err := rows.Scan(&item.WorkingSet.ID, &item.WorkingSet.Scope.Namespace, &item.WorkingSet.Scope.Owner,
			&item.WorkingSet.Scope.Subject, &item.WorkingSet.Scope.Actor, &contextID, &visibility,
			&item.WorkingSet.TaskID, &goalJSON, &globalRecall, &expiresAt, &createdAt, &updatedAt, &item.State); err != nil {
			return nil, fmt.Errorf("scan export working set: %w", err)
		}
		item.WorkingSet.Scope.Context = domain.ID(contextID)
		item.WorkingSet.Scope.Visibility = domain.Visibility(visibility)
		item.WorkingSet.GlobalRecall = globalRecall != 0
		if err := json.Unmarshal([]byte(goalJSON), &item.WorkingSet.Goal); err != nil {
			return nil, fmt.Errorf("decode working set goal: %w", err)
		}
		item.WorkingSet.CreatedAt, err = parseStoredTime(createdAt)
		if err != nil {
			return nil, err
		}
		item.WorkingSet.UpdatedAt, err = parseStoredTime(updatedAt)
		if err != nil {
			return nil, err
		}
		if expiresAt.Valid {
			parsed, parseErr := parseStoredTime(expiresAt.String)
			if parseErr != nil {
				return nil, parseErr
			}
			item.WorkingSet.ExpiresAt = &parsed
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func exportMemories(ctx context.Context, tx *sql.Tx, where string, args []any) ([]application.PortableMemory, error) {
	rows, err := tx.QueryContext(ctx, `
        SELECT x.id, x.namespace_id, x.owner_id, x.subject_id, x.actor_id, COALESCE(x.context_id, ''),
               x.visibility, x.function, x.state, x.current_version, x.created_at,
               v.id, v.version_number, v.state, v.taxonomy_json, v.payload_json, v.provenance_json,
               v.supersedes_json, v.derived_from_json, v.valid_from, v.valid_to,
               v.system_from, v.system_to, COALESCE(v.conflict_group_id, '')
        FROM memories x JOIN memory_versions v ON v.memory_id = x.id
        WHERE `+where+` ORDER BY x.id, v.version_number, v.id`, args...)
	if err != nil {
		return nil, fmt.Errorf("list export memories: %w", err)
	}
	defer rows.Close()
	var result []application.PortableMemory
	indexes := make(map[domain.ID]int)
	for rows.Next() {
		memory, version, err := scanMemoryVersion(rows)
		if err != nil {
			return nil, fmt.Errorf("scan export memory: %w", err)
		}
		index, ok := indexes[memory.ID]
		if !ok {
			index = len(result)
			indexes[memory.ID] = index
			result = append(result, application.PortableMemory{Memory: memory})
		}
		result[index].Versions = append(result[index].Versions, version)
	}
	return result, rows.Err()
}

func exportReferencedEpisodes(ctx context.Context, tx *sql.Tx, memories []application.PortableMemory) ([]application.PortableEpisode, error) {
	references := make(map[domain.ID]map[domain.ID]struct{})
	for _, portable := range memories {
		for _, version := range portable.Versions {
			if version.Payload.Experiential == nil {
				continue
			}
			for _, evidence := range version.Payload.Experiential.Evidence {
				if references[evidence.EpisodeID] == nil {
					references[evidence.EpisodeID] = make(map[domain.ID]struct{})
				}
				for _, outcomeID := range evidence.OutcomeIDs {
					references[evidence.EpisodeID][outcomeID] = struct{}{}
				}
			}
		}
	}
	ids := make([]domain.ID, 0, len(references))
	for id := range references {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	result := make([]application.PortableEpisode, 0, len(ids))
	for _, id := range ids {
		var item application.PortableEpisode
		var taskJSON, observationIDsJSON, startedAt, endedAt string
		if err := tx.QueryRowContext(ctx, `
            SELECT id, namespace_id, subject_id, task_json, observation_ids_json, started_at, ended_at
            FROM episodes WHERE id = ?`, id).Scan(&item.ID, &item.Namespace, &item.Subject, &taskJSON,
			&observationIDsJSON, &startedAt, &endedAt); err != nil {
			return nil, fmt.Errorf("load export episode %s: %w", id, err)
		}
		if err := decodeJSONFields([]string{taskJSON, observationIDsJSON}, []any{&item.Task, &item.ObservationIDs}); err != nil {
			return nil, err
		}
		var err error
		item.StartedAt, err = parseStoredTime(startedAt)
		if err != nil {
			return nil, err
		}
		item.EndedAt, err = parseStoredTime(endedAt)
		if err != nil {
			return nil, err
		}
		outcomeIDs := make([]domain.ID, 0, len(references[id]))
		for outcomeID := range references[id] {
			outcomeIDs = append(outcomeIDs, outcomeID)
		}
		slices.Sort(outcomeIDs)
		for _, outcomeID := range outcomeIDs {
			var outcome domain.Outcome
			var evidenceJSON, observedAt string
			if err := tx.QueryRowContext(ctx, `
                SELECT id, episode_id, source_id, kind, value, evidence_json, observed_at
                FROM outcomes WHERE id = ?`, outcomeID).Scan(&outcome.ID, &outcome.EpisodeID, &outcome.Source,
				&outcome.Kind, &outcome.Value, &evidenceJSON, &observedAt); err != nil {
				return nil, fmt.Errorf("load export outcome %s: %w", outcomeID, err)
			}
			if err := json.Unmarshal([]byte(evidenceJSON), &outcome.Evidence); err != nil {
				return nil, fmt.Errorf("decode outcome evidence: %w", err)
			}
			outcome.ObservedAt, err = parseStoredTime(observedAt)
			if err != nil {
				return nil, err
			}
			item.Outcomes = append(item.Outcomes, outcome)
		}
		result = append(result, item)
	}
	return result, nil
}

// ImportSubject validates and imports one complete subject bundle. Dry runs execute all inserts and roll back.
func (s *Store) ImportSubject(ctx context.Context, export application.SubjectExport, dryRun bool) (ImportResult, error) {
	result := importCounts(export)
	result.DryRun = dryRun
	application.SortSubjectExport(&export)
	if err := export.Validate(); err != nil {
		return result, fmt.Errorf("validate subject import: %w", err)
	}
	if !dryRun {
		nonEmpty, err := s.hasPortableData(ctx)
		if err != nil {
			return result, err
		}
		if nonEmpty {
			result.BackupPath = fmt.Sprintf("%s.pre-import-%s.bak", s.path, time.Now().UTC().Format("20060102T150405.000000000Z"))
			if err := s.Backup(ctx, result.BackupPath); err != nil {
				return result, fmt.Errorf("pre-import backup: %w", err)
			}
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return result, fmt.Errorf("begin subject import: %w", err)
	}
	defer tx.Rollback()
	if err := importSubjectRows(ctx, tx, export); err != nil {
		return result, err
	}
	if err := validateTransactionForeignKeys(ctx, tx); err != nil {
		return result, err
	}
	if dryRun {
		if err := tx.Rollback(); err != nil {
			return result, fmt.Errorf("roll back subject import dry-run: %w", err)
		}
		return result, nil
	}
	if err := tx.Commit(); err != nil {
		return result, fmt.Errorf("commit subject import: %w", err)
	}
	if err := s.Validate(ctx); err != nil {
		return result, fmt.Errorf("validate imported database: %w", err)
	}
	return result, nil
}

func importCounts(export application.SubjectExport) ImportResult {
	result := ImportResult{Observations: len(export.Observations), Episodes: len(export.Episodes), WorkingSets: len(export.WorkingSets), Memories: len(export.Memories)}
	for _, episode := range export.Episodes {
		result.Outcomes += len(episode.Outcomes)
	}
	for _, memory := range export.Memories {
		result.Versions += len(memory.Versions)
	}
	return result
}

func (s *Store) hasPortableData(ctx context.Context) (bool, error) {
	var exists int
	if err := s.db.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM observations UNION ALL SELECT 1 FROM episodes
			UNION ALL SELECT 1 FROM working_sets UNION ALL SELECT 1 FROM memories
			UNION ALL SELECT 1 FROM operations UNION ALL SELECT 1 FROM retrieval_traces
			UNION ALL SELECT 1 FROM durable_jobs UNION ALL SELECT 1 FROM artifacts
			UNION ALL SELECT 1 FROM purge_receipts UNION ALL SELECT 1 FROM api_tokens
			UNION ALL SELECT 1 FROM agent_event_receipts
		)`).Scan(&exists); err != nil {
		return false, fmt.Errorf("check import target: %w", err)
	}
	return exists != 0, nil
}

func importSubjectRows(ctx context.Context, tx *sql.Tx, export application.SubjectExport) error {
	for _, observation := range export.Observations {
		if err := insertObservation(ctx, tx, observation, export.ExportedAt); err != nil {
			return fmt.Errorf("import observation %s: %w", observation.ID, err)
		}
	}
	for _, portable := range export.WorkingSets {
		goal, err := json.Marshal(portable.WorkingSet.Goal)
		if err != nil {
			return err
		}
		set := portable.WorkingSet
		if _, err := tx.ExecContext(ctx, `
            INSERT INTO working_sets(
                id, namespace_id, owner_id, subject_id, actor_id, context_id, visibility,
                task_id, goal_json, global_recall, expires_at, created_at, updated_at, state
            ) VALUES(?, ?, ?, ?, ?, NULLIF(?, ''), ?, ?, ?, ?, ?, ?, ?, ?)`,
			set.ID, set.Scope.Namespace, set.Scope.Owner, set.Scope.Subject, set.Scope.Actor, set.Scope.Context,
			set.Scope.Visibility, set.TaskID, string(goal), set.GlobalRecall, nullableTime(set.ExpiresAt),
			formatTime(set.CreatedAt), formatTime(set.UpdatedAt), portable.State); err != nil {
			return fmt.Errorf("import working set %s: %w", set.ID, err)
		}
	}
	for _, episode := range export.Episodes {
		task, err := json.Marshal(episode.Task)
		if err != nil {
			return err
		}
		observations, err := json.Marshal(episode.ObservationIDs)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
            INSERT INTO episodes(id, namespace_id, subject_id, task_json, observation_ids_json, started_at, ended_at)
            VALUES(?, ?, ?, ?, ?, ?, ?)`, episode.ID, episode.Namespace, episode.Subject, string(task),
			string(observations), formatTime(episode.StartedAt), formatTime(episode.EndedAt)); err != nil {
			return fmt.Errorf("import episode %s: %w", episode.ID, err)
		}
		for _, outcome := range episode.Outcomes {
			evidence, err := json.Marshal(outcome.Evidence)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `
                INSERT INTO outcomes(id, episode_id, source_id, kind, value, evidence_json, observed_at)
                VALUES(?, ?, ?, ?, ?, ?, ?)`, outcome.ID, outcome.EpisodeID, outcome.Source, outcome.Kind,
				outcome.Value, string(evidence), formatTime(outcome.ObservedAt)); err != nil {
				return fmt.Errorf("import outcome %s: %w", outcome.ID, err)
			}
		}
	}
	for _, portable := range export.Memories {
		memory := portable.Memory
		if _, err := tx.ExecContext(ctx, `
            INSERT INTO memories(
                id, namespace_id, owner_id, subject_id, actor_id, context_id, visibility,
                function, state, current_version, created_at
            ) VALUES(?, ?, ?, ?, ?, NULLIF(?, ''), ?, ?, ?, ?, ?)`,
			memory.ID, memory.Scope.Namespace, memory.Scope.Owner, memory.Scope.Subject, memory.Scope.Actor,
			memory.Scope.Context, memory.Scope.Visibility, memory.Function, memory.State, memory.CurrentVersion,
			formatTime(memory.CreatedAt)); err != nil {
			return fmt.Errorf("import memory %s: %w", memory.ID, err)
		}
	}
	for _, portable := range export.Memories {
		for _, version := range portable.Versions {
			if err := insertPortableVersion(ctx, tx, version); err != nil {
				return err
			}
		}
	}
	for _, portable := range export.Memories {
		for _, version := range portable.Versions {
			for _, parentID := range version.DerivedFrom {
				if _, err := tx.ExecContext(ctx,
					"INSERT INTO memory_dependencies(parent_version_id, child_version_id) VALUES(?, ?)", parentID, version.ID); err != nil {
					return fmt.Errorf("import dependency %s -> %s: %w", parentID, version.ID, err)
				}
			}
			if portable.Memory.State != domain.MemoryForgotten && version.State != domain.VersionForgotten {
				if _, err := tx.ExecContext(ctx,
					"INSERT INTO memory_fts(memory_version_id, memory_id, namespace_id, text_content) VALUES(?, ?, ?, ?)",
					version.ID, portable.Memory.ID, portable.Memory.Scope.Namespace, payloadPlainText(version.Payload)); err != nil {
					return fmt.Errorf("index imported version %s: %w", version.ID, err)
				}
			}
		}
	}
	return nil
}

func insertPortableVersion(ctx context.Context, tx *sql.Tx, version domain.MemoryVersion) error {
	taxonomy, payload, provenance, supersedes, derivedFrom, err := encodeVersion(version)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
        INSERT INTO memory_versions(
            id, memory_id, version_number, state, taxonomy_json, payload_json, provenance_json,
            supersedes_json, derived_from_json, valid_from, valid_to, system_from, system_to,
            conflict_group_id, created_at
        ) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULLIF(?, ''), ?)`,
		version.ID, version.MemoryID, version.Number, version.State, string(taxonomy), string(payload),
		string(provenance), string(supersedes), string(derivedFrom), formatTime(version.ValidTime.From),
		nullableTime(version.ValidTime.To), formatTime(version.SystemTime.From), nullableTime(version.SystemTime.To),
		version.ConflictGroup, formatTime(version.SystemTime.From)); err != nil {
		return fmt.Errorf("import memory version %s: %w", version.ID, err)
	}
	return nil
}

func validateTransactionForeignKeys(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return fmt.Errorf("validate imported foreign keys: %w", err)
	}
	defer rows.Close()
	if rows.Next() {
		return fmt.Errorf("subject import created a foreign key violation")
	}
	return rows.Err()
}
