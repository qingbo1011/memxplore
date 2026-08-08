package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"unicode"

	"github.com/qingbo1011/memxplore/internal/application"
	"github.com/qingbo1011/memxplore/internal/domain"
)

// SearchLexicalCandidates performs authorized, bitemporal FTS5/BM25 candidate retrieval.
func (s *Store) SearchLexicalCandidates(ctx context.Context, filter application.CandidateFilter, query string, limit int) ([]application.StoredCandidate, error) {
	ftsQuery, err := safeFTSQuery(query)
	if err != nil {
		return nil, err
	}
	where, args, err := candidateWhere(filter)
	if err != nil {
		return nil, err
	}
	args = append([]any{ftsQuery}, args...)
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, `
        SELECT m.id, v.id, m.function, v.conflict_group_id,
               v.payload_json, v.provenance_json, memory_fts.text_content,
               bm25(memory_fts, 0.0, 0.0, 0.0, 1.0)
        FROM memory_fts
        JOIN memory_versions v ON v.id = memory_fts.memory_version_id
        JOIN memories m ON m.id = v.memory_id
        WHERE memory_fts MATCH ? AND `+where+`
        ORDER BY bm25(memory_fts, 0.0, 0.0, 0.0, 1.0), v.id
        LIMIT ?`, args...)
	if err != nil {
		return nil, fmt.Errorf("search authorized lexical candidates: %w", err)
	}
	defer rows.Close()
	return scanCandidates(rows, true)
}

// ListSemanticCandidates returns exact vectors only after authorization/time filtering.
func (s *Store) ListSemanticCandidates(ctx context.Context, filter application.CandidateFilter, providerID, model string, limit int) ([]application.StoredCandidate, error) {
	if providerID == "" || model == "" || limit < 1 || limit > 10001 {
		return nil, fmt.Errorf("semantic provider, model, and limit within [1,10001] are required")
	}
	where, args, err := candidateWhere(filter)
	if err != nil {
		return nil, err
	}
	args = append([]any{providerID, model}, args...)
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, `
        SELECT m.id, v.id, m.function, v.conflict_group_id,
               v.payload_json, v.provenance_json, memory_fts.text_content,
               e.vector_blob, e.dimensions, e.content_sha256
        FROM memory_embeddings e
        JOIN memory_versions v ON v.id = e.memory_version_id
        JOIN memories m ON m.id = v.memory_id
        JOIN memory_fts ON memory_fts.memory_version_id = v.id
        WHERE e.provider_id = ? AND e.model = ? AND `+where+`
        ORDER BY v.id
        LIMIT ?`, args...)
	if err != nil {
		return nil, fmt.Errorf("list authorized semantic candidates: %w", err)
	}
	defer rows.Close()
	return scanCandidates(rows, false)
}

func candidateWhere(filter application.CandidateFilter) (string, []any, error) {
	if filter.Access.Namespace == "" || filter.Access.PrincipalID == "" || len(filter.Access.PrivateOwners) == 0 || filter.Subject == "" || filter.ValidAt.IsZero() || filter.SystemAt.IsZero() {
		return "", nil, fmt.Errorf("complete authorized candidate filter is required")
	}
	privatePlaceholders := make([]string, len(filter.Access.PrivateOwners))
	args := []any{filter.Access.Namespace, filter.Subject}
	for index, owner := range filter.Access.PrivateOwners {
		if owner == "" {
			return "", nil, fmt.Errorf("private owner cannot be empty")
		}
		privatePlaceholders[index] = "?"
		args = append(args, owner)
	}
	args = append(args, filter.Access.AllowShared, filter.Access.AllowPublic)
	clauses := []string{
		"m.namespace_id = ?",
		"m.subject_id = ?",
		"((m.visibility = 'private' AND m.owner_id IN (" + strings.Join(privatePlaceholders, ",") + "))" +
			" OR (m.visibility = 'shared' AND ? = 1) OR (m.visibility = 'public' AND ? = 1))",
		"m.state = 'active'",
		"v.state IN ('current', 'superseded')",
		"v.valid_from <= ? AND (v.valid_to IS NULL OR v.valid_to > ?)",
		"v.system_from <= ? AND (v.system_to IS NULL OR v.system_to > ?)",
	}
	args = append(args, formatTime(filter.ValidAt), formatTime(filter.ValidAt), formatTime(filter.SystemAt), formatTime(filter.SystemAt))
	workingSet := `EXISTS (
        SELECT 1 FROM working_sets ws
        WHERE ws.namespace_id = m.namespace_id AND ws.task_id = m.context_id
          AND ws.state = 'active' AND (ws.expires_at IS NULL OR ws.expires_at > ?)`
	if filter.Context != "" {
		clauses = append(clauses, "(m.function <> 'working' OR "+workingSet+" AND ws.task_id = ?))")
		args = append(args, formatTime(filter.SystemAt), filter.Context)
	} else if filter.IncludeGlobalWorking {
		clauses = append(clauses, "(m.function <> 'working' OR "+workingSet+" AND ws.global_recall = 1))")
		args = append(args, formatTime(filter.SystemAt))
	} else {
		clauses = append(clauses, "m.function <> 'working'")
	}
	if len(filter.Functions) > 0 {
		placeholders := make([]string, len(filter.Functions))
		for index, function := range filter.Functions {
			switch function {
			case domain.FunctionFactual, domain.FunctionExperiential, domain.FunctionWorking:
			default:
				return "", nil, fmt.Errorf("memory function %q is invalid", function)
			}
			placeholders[index] = "?"
			args = append(args, function)
		}
		clauses = append(clauses, "m.function IN ("+strings.Join(placeholders, ",")+")")
	}
	return strings.Join(clauses, " AND "), args, nil
}

func safeFTSQuery(query string) (string, error) {
	var tokens []string
	var current strings.Builder
	flush := func() {
		if current.Len() > 0 {
			tokens = append(tokens, current.String())
			current.Reset()
		}
	}
	for _, character := range query {
		if unicode.IsLetter(character) || unicode.IsNumber(character) || character == '_' {
			current.WriteRune(character)
		} else {
			flush()
		}
	}
	flush()
	if len(tokens) == 0 {
		return "", fmt.Errorf("lexical query contains no searchable tokens")
	}
	for index := range tokens {
		tokens[index] = `"` + tokens[index] + `"`
	}
	return strings.Join(tokens, " OR "), nil
}

type candidateRows interface {
	Next() bool
	Scan(...any) error
	Err() error
}

func scanCandidates(rows candidateRows, lexical bool) ([]application.StoredCandidate, error) {
	var candidates []application.StoredCandidate
	for rows.Next() {
		var candidate application.StoredCandidate
		var function string
		var conflict sql.NullString
		var payloadJSON, provenanceJSON string
		if lexical {
			var score float64
			if err := rows.Scan(&candidate.MemoryID, &candidate.VersionID, &function, &conflict,
				&payloadJSON, &provenanceJSON, &candidate.Text, &score); err != nil {
				return nil, fmt.Errorf("scan lexical candidate: %w", err)
			}
			candidate.LexicalBM25 = &score
		} else {
			var encoded []byte
			var dimensions int
			var storedDigest string
			if err := rows.Scan(&candidate.MemoryID, &candidate.VersionID, &function, &conflict,
				&payloadJSON, &provenanceJSON, &candidate.Text, &encoded, &dimensions, &storedDigest); err != nil {
				return nil, fmt.Errorf("scan semantic candidate: %w", err)
			}
			digest := sha256.Sum256([]byte(candidate.Text))
			if storedDigest != hex.EncodeToString(digest[:]) {
				return nil, fmt.Errorf("stored embedding %s content digest mismatch", candidate.VersionID)
			}
			vector, err := decodeVector(encoded, dimensions)
			if err != nil {
				return nil, fmt.Errorf("decode embedding %s: %w", candidate.VersionID, err)
			}
			candidate.Vector = vector
		}
		candidate.Function = domain.MemoryFunction(function)
		candidate.ConflictGroup = domain.ID(conflict.String)
		if err := json.Unmarshal([]byte(payloadJSON), &candidate.Payload); err != nil {
			return nil, fmt.Errorf("decode candidate payload: %w", err)
		}
		if err := candidate.Payload.Validate(candidate.Function); err != nil {
			return nil, fmt.Errorf("validate candidate payload: %w", err)
		}
		if err := json.Unmarshal([]byte(provenanceJSON), &candidate.Provenance); err != nil {
			return nil, fmt.Errorf("decode candidate provenance: %w", err)
		}
		if len(candidate.Provenance) == 0 {
			return nil, fmt.Errorf("candidate provenance is empty")
		}
		candidates = append(candidates, candidate)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate candidates: %w", err)
	}
	return candidates, nil
}

// PutRetrievalTrace atomically persists a trace and all candidate decisions.
func (s *Store) PutRetrievalTrace(ctx context.Context, trace domain.RetrievalTrace) error {
	if err := trace.Validate(); err != nil {
		return fmt.Errorf("validate retrieval trace: %w", err)
	}
	scope, err := json.Marshal(trace.Scope)
	if err != nil {
		return fmt.Errorf("encode retrieval scope: %w", err)
	}
	filter, err := json.Marshal(struct {
		Authorization        domain.RetrievalAuthorization `json:"authorization"`
		Functions            []domain.MemoryFunction       `json:"functions,omitempty"`
		IncludeGlobalWorking bool                          `json:"include_global_working"`
	}{trace.Authorization, trace.Functions, trace.IncludeGlobalWorking})
	if err != nil {
		return fmt.Errorf("encode retrieval filter: %w", err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin retrieval trace: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
        INSERT INTO retrieval_traces(
            id, namespace_id, scope_json, query, strategy_id, strategy_hash, fallback_reason,
			filter_json, valid_at, system_at, token_budget, tokens_used, started_at, completed_at
		) VALUES(?, ?, ?, ?, ?, ?, NULLIF(?, ''), ?, ?, ?, ?, ?, ?, ?)`,
		trace.ID, trace.Scope.Namespace, string(scope), trace.Query, trace.StrategyID, trace.StrategyHash, trace.FallbackReason,
		string(filter), formatTime(trace.ValidAt), formatTime(trace.SystemAt), trace.TokenBudget, trace.TokensUsed,
		formatTime(trace.StartedAt), formatTime(trace.CompletedAt)); err != nil {
		return fmt.Errorf("insert retrieval trace: %w", err)
	}
	for ordinal, candidate := range trace.Candidates {
		encoded, err := json.Marshal(candidate)
		if err != nil {
			return fmt.Errorf("encode retrieval candidate %d: %w", ordinal, err)
		}
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO retrieval_candidates(trace_id, ordinal, candidate_json) VALUES(?, ?, ?)",
			trace.ID, ordinal, string(encoded)); err != nil {
			return fmt.Errorf("insert retrieval candidate %d: %w", ordinal, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit retrieval trace: %w", err)
	}
	return nil
}

var (
	_ application.CandidateRepository = (*Store)(nil)
	_ application.RetrievalTraceSink  = (*Store)(nil)
)
