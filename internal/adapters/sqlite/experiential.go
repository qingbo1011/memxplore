package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/qingbo1011/memxplore/internal/domain"
)

// PutEpisode atomically records a task trajectory and independently sourced outcomes.
func (s *Store) PutEpisode(ctx context.Context, episode domain.Episode, outcomes []domain.Outcome) error {
	if err := episode.Validate(); err != nil {
		return err
	}
	if len(outcomes) == 0 {
		return fmt.Errorf("episode requires at least one outcome")
	}
	for _, outcome := range outcomes {
		if err := outcome.Validate(); err != nil {
			return err
		}
		if outcome.EpisodeID != episode.ID {
			return fmt.Errorf("outcome %s belongs to another episode", outcome.ID)
		}
	}
	task, err := json.Marshal(episode.Task)
	if err != nil {
		return err
	}
	observationIDs, err := json.Marshal(episode.ObservationIDs)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin episode insert: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
        INSERT INTO episodes(id, namespace_id, subject_id, task_json, observation_ids_json, started_at, ended_at)
        VALUES(?, ?, ?, ?, ?, ?, ?)`, episode.ID, episode.Scope.Namespace, episode.Scope.Subject,
		string(task), string(observationIDs), formatTime(episode.StartedAt), formatTime(episode.EndedAt)); err != nil {
		return fmt.Errorf("insert episode: %w", err)
	}
	for _, outcome := range outcomes {
		evidence, err := json.Marshal(outcome.Evidence)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
            INSERT INTO outcomes(id, episode_id, source_id, kind, value, evidence_json, observed_at)
            VALUES(?, ?, ?, ?, ?, ?, ?)`, outcome.ID, outcome.EpisodeID, outcome.Source,
			outcome.Kind, outcome.Value, string(evidence), formatTime(outcome.ObservedAt)); err != nil {
			return fmt.Errorf("insert outcome %s: %w", outcome.ID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit episode: %w", err)
	}
	return nil
}

// RecordUsageFeedback stores retrieval feedback without rewriting lesson content.
func (s *Store) RecordUsageFeedback(ctx context.Context, versionID domain.ID, feedback domain.UsageFeedback) error {
	if versionID == "" || feedback.TraceID == "" || feedback.Source == "" || feedback.Value < -1 || feedback.Value > 1 || feedback.RecordedAt.IsZero() {
		return fmt.Errorf("valid version and usage feedback are required")
	}
	var memoryNamespace, traceNamespace string
	var function string
	if err := s.db.QueryRowContext(ctx, `
        SELECT m.namespace_id, m.function
        FROM memory_versions v JOIN memories m ON m.id = v.memory_id
        WHERE v.id = ?`, versionID).Scan(&memoryNamespace, &function); err != nil {
		return fmt.Errorf("load feedback memory: %w", err)
	}
	if function != string(domain.FunctionExperiential) {
		return fmt.Errorf("usage feedback target is not experiential memory")
	}
	if err := s.db.QueryRowContext(ctx,
		"SELECT namespace_id FROM retrieval_traces WHERE id = ?", feedback.TraceID).Scan(&traceNamespace); err != nil {
		return fmt.Errorf("load feedback trace: %w", err)
	}
	if memoryNamespace != traceNamespace {
		return fmt.Errorf("usage feedback crosses namespace")
	}
	_, err := s.db.ExecContext(ctx, `
        INSERT INTO experiential_feedback(memory_version_id, trace_id, source_id, value, recorded_at)
        VALUES(?, ?, ?, ?, ?)
        ON CONFLICT(memory_version_id, trace_id, source_id) DO UPDATE SET
            value = excluded.value, recorded_at = excluded.recorded_at`,
		versionID, feedback.TraceID, feedback.Source, feedback.Value, formatTime(feedback.RecordedAt))
	if err != nil {
		return fmt.Errorf("record experiential feedback: %w", err)
	}
	return nil
}

// UsageFeedback returns independently stored feedback for one lesson version.
func (s *Store) UsageFeedback(ctx context.Context, versionID domain.ID) ([]domain.UsageFeedback, error) {
	rows, err := s.db.QueryContext(ctx, `
        SELECT trace_id, source_id, value, recorded_at
        FROM experiential_feedback WHERE memory_version_id = ?
        ORDER BY recorded_at, trace_id, source_id`, versionID)
	if err != nil {
		return nil, fmt.Errorf("list experiential feedback: %w", err)
	}
	defer rows.Close()
	var result []domain.UsageFeedback
	for rows.Next() {
		var feedback domain.UsageFeedback
		var recorded string
		if err := rows.Scan(&feedback.TraceID, &feedback.Source, &feedback.Value, &recorded); err != nil {
			return nil, err
		}
		feedback.RecordedAt, err = parseStoredTime(recorded)
		if err != nil {
			return nil, err
		}
		result = append(result, feedback)
	}
	return result, rows.Err()
}

func validateExperientialReferences(ctx context.Context, tx *sql.Tx, namespace domain.ID, memory domain.ExperientialMemory) error {
	for _, evidence := range memory.Evidence {
		var episodeNamespace domain.ID
		if err := tx.QueryRowContext(ctx, "SELECT namespace_id FROM episodes WHERE id = ?", evidence.EpisodeID).Scan(&episodeNamespace); err != nil {
			return fmt.Errorf("load lesson episode %s: %w", evidence.EpisodeID, err)
		}
		if episodeNamespace != namespace {
			return fmt.Errorf("lesson episode %s crosses namespace", evidence.EpisodeID)
		}
		for _, outcomeID := range evidence.OutcomeIDs {
			var episodeID domain.ID
			if err := tx.QueryRowContext(ctx, "SELECT episode_id FROM outcomes WHERE id = ?", outcomeID).Scan(&episodeID); err != nil {
				return fmt.Errorf("load lesson outcome %s: %w", outcomeID, err)
			}
			if episodeID != evidence.EpisodeID {
				return fmt.Errorf("lesson outcome %s does not belong to episode %s", outcomeID, evidence.EpisodeID)
			}
		}
	}
	return nil
}
