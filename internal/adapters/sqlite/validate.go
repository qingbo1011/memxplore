package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
)

// Capabilities is executable evidence for the first storage milestone.
type Capabilities struct {
	FTS5          bool
	BM25          bool
	WAL           bool
	ForeignKeys   bool
	BusyTimeoutMS int
	SecureDelete  bool
	SchemaVersion int
}

// Probe checks the active database connection's required capabilities.
func (s *Store) Probe(ctx context.Context) (Capabilities, error) {
	var result Capabilities
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return result, fmt.Errorf("acquire probe connection: %w", err)
	}
	defer conn.Close()
	var journalMode string
	var foreignKeys int
	var secureDelete int
	if err := conn.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journalMode); err != nil {
		return result, fmt.Errorf("probe WAL: %w", err)
	}
	if err := conn.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		return result, fmt.Errorf("probe foreign keys: %w", err)
	}
	if err := conn.QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&result.BusyTimeoutMS); err != nil {
		return result, fmt.Errorf("probe busy timeout: %w", err)
	}
	if err := conn.QueryRowContext(ctx, "PRAGMA secure_delete").Scan(&secureDelete); err != nil {
		return result, fmt.Errorf("probe secure delete: %w", err)
	}
	if err := conn.QueryRowContext(ctx, "PRAGMA user_version").Scan(&result.SchemaVersion); err != nil {
		return result, fmt.Errorf("probe schema version: %w", err)
	}
	if _, err := conn.ExecContext(ctx, "INSERT INTO memory_fts(memory_version_id, memory_id, namespace_id, text_content) VALUES('probe-v', 'probe-m', 'probe-ns', 'memxplore capability probe')"); err != nil {
		return result, fmt.Errorf("probe FTS5 insert: %w", err)
	}
	defer conn.ExecContext(context.Background(), "DELETE FROM memory_fts WHERE memory_version_id = 'probe-v'")
	var score float64
	if err := conn.QueryRowContext(ctx, "SELECT bm25(memory_fts) FROM memory_fts WHERE memory_fts MATCH 'capability'").Scan(&score); err != nil {
		return result, fmt.Errorf("probe BM25: %w", err)
	}
	result.FTS5 = true
	result.BM25 = true
	result.WAL = strings.EqualFold(journalMode, "wal")
	result.ForeignKeys = foreignKeys == 1
	result.SecureDelete = secureDelete == 1
	return result, nil
}

// Validate checks integrity, foreign keys, schema version, and required capabilities.
func (s *Store) Validate(ctx context.Context) error {
	if err := validateDBIntegrity(ctx, s.db); err != nil {
		return err
	}
	capabilities, err := s.Probe(ctx)
	if err != nil {
		return err
	}
	if !capabilities.FTS5 || !capabilities.BM25 || !capabilities.WAL || !capabilities.ForeignKeys || !capabilities.SecureDelete {
		return fmt.Errorf("required SQLite capability missing: %+v", capabilities)
	}
	if capabilities.BusyTimeoutMS != int(s.busyTimeout.Milliseconds()) {
		return fmt.Errorf("busy timeout = %dms, want %dms", capabilities.BusyTimeoutMS, s.busyTimeout.Milliseconds())
	}
	if capabilities.SchemaVersion != latestSchemaVersion {
		return fmt.Errorf("schema version = %d, want %d", capabilities.SchemaVersion, latestSchemaVersion)
	}
	return nil
}

// ValidateIntegrity opens a database read-only enough for integrity validation.
func ValidateIntegrity(ctx context.Context, path string) error {
	if path == "" {
		return fmt.Errorf("integrity path is required")
	}
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?_query_only=1&_foreign_keys=on")
	if err != nil {
		return fmt.Errorf("open integrity database: %w", err)
	}
	defer db.Close()
	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("ping integrity database: %w", err)
	}
	return validateDBIntegrity(ctx, db)
}

func validateDBIntegrity(ctx context.Context, db *sql.DB) error {
	var integrity string
	if err := db.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&integrity); err != nil {
		return fmt.Errorf("integrity check: %w", err)
	}
	if integrity != "ok" {
		return fmt.Errorf("integrity check returned %q", integrity)
	}
	rows, err := db.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return fmt.Errorf("foreign key check: %w", err)
	}
	defer rows.Close()
	if rows.Next() {
		return fmt.Errorf("foreign key check found a violation")
	}
	return rows.Err()
}
