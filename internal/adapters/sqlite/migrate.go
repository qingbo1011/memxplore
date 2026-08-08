package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

type migration struct {
	version int
	name    string
	sql     string
	sha256  string
}

func migrate(ctx context.Context, db transactionBeginner, current int) error {
	migrations, err := loadMigrations()
	if err != nil {
		return err
	}
	for _, item := range migrations {
		if item.version <= current {
			continue
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin migration %d: %w", item.version, err)
		}
		if _, err := tx.ExecContext(ctx, item.sql); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("apply migration %d: %w", item.version, err)
		}
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO schema_migrations(version, name, sha256, applied_at) VALUES(?, ?, ?, ?)",
			item.version, item.name, item.sha256, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("record migration %d: %w", item.version, err)
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", item.version)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("version migration %d: %w", item.version, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %d: %w", item.version, err)
		}
	}
	return nil
}

type transactionBeginner interface {
	BeginTx(context.Context, *sql.TxOptions) (*sql.Tx, error)
}

func loadMigrations() ([]migration, error) {
	entries, err := fs.ReadDir(migrationFiles, "migrations")
	if err != nil {
		return nil, fmt.Errorf("list migrations: %w", err)
	}
	items := make([]migration, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		prefix, _, ok := strings.Cut(entry.Name(), "_")
		if !ok {
			return nil, fmt.Errorf("migration %q has no numeric prefix", entry.Name())
		}
		version, err := strconv.Atoi(prefix)
		if err != nil || version < 1 {
			return nil, fmt.Errorf("migration %q has invalid version", entry.Name())
		}
		content, err := migrationFiles.ReadFile(filepath.ToSlash(filepath.Join("migrations", entry.Name())))
		if err != nil {
			return nil, fmt.Errorf("read migration %q: %w", entry.Name(), err)
		}
		digest := sha256.Sum256(content)
		items = append(items, migration{
			version: version,
			name:    entry.Name(),
			sql:     string(content),
			sha256:  hex.EncodeToString(digest[:]),
		})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].version < items[j].version })
	if len(items) == 0 || items[len(items)-1].version != latestSchemaVersion {
		return nil, fmt.Errorf("latest embedded migration does not match schema version %d", latestSchemaVersion)
	}
	for index, item := range items {
		if item.version != index+1 {
			return nil, fmt.Errorf("migration sequence skips version %d", index+1)
		}
	}
	return items, nil
}
