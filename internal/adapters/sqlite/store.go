// Package sqlite implements MemXplore persistence using CGO-free modernc SQLite.
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const latestSchemaVersion = 2

// Options control connection safety and migration backups.
type Options struct {
	BusyTimeout        time.Duration
	MaxOpenConnections int
	MigrationBackupDir string
}

// DefaultOptions are conservative for one daemon with concurrent readers.
func DefaultOptions() Options {
	return Options{
		BusyTimeout:        5 * time.Second,
		MaxOpenConnections: 8,
	}
}

// Store owns a SQLite pool and records backups created before migrations.
type Store struct {
	db               *sql.DB
	path             string
	busyTimeout      time.Duration
	migrationBackups []string
}

// Open configures every pooled connection, backs up old schemas, and migrates.
func Open(ctx context.Context, path string, options Options) (_ *Store, finalErr error) {
	if strings.TrimSpace(path) == "" || path == ":memory:" {
		return nil, fmt.Errorf("sqlite path must be a durable file")
	}
	if options.BusyTimeout <= 0 {
		options.BusyTimeout = DefaultOptions().BusyTimeout
	}
	if options.MaxOpenConnections <= 0 {
		options.MaxOpenConnections = DefaultOptions().MaxOpenConnections
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, fmt.Errorf("create database directory: %w", err)
	}

	existed, err := nonEmptyFile(path)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", databaseDSN(path, options.BusyTimeout))
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(options.MaxOpenConnections)
	db.SetMaxIdleConns(options.MaxOpenConnections)
	store := &Store{db: db, path: path, busyTimeout: options.BusyTimeout}
	defer func() {
		if finalErr != nil {
			_ = db.Close()
		}
	}()
	if err := db.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}

	current, err := userVersion(ctx, db)
	if err != nil {
		return nil, err
	}
	if current > latestSchemaVersion {
		return nil, fmt.Errorf("database schema %d is newer than supported %d", current, latestSchemaVersion)
	}
	if existed && current < latestSchemaVersion {
		backupPath, err := migrationBackupPath(path, options.MigrationBackupDir, current, latestSchemaVersion)
		if err != nil {
			return nil, err
		}
		if err := store.Backup(ctx, backupPath); err != nil {
			return nil, fmt.Errorf("pre-migration backup: %w", err)
		}
		if err := ValidateIntegrity(ctx, backupPath); err != nil {
			return nil, fmt.Errorf("verify pre-migration backup: %w", err)
		}
		store.migrationBackups = append(store.migrationBackups, backupPath)
	}
	if err := migrate(ctx, db, current); err != nil {
		return nil, err
	}
	if err := store.Validate(ctx); err != nil {
		return nil, fmt.Errorf("validate opened database: %w", err)
	}
	return store, nil
}

func databaseDSN(path string, busyTimeout time.Duration) string {
	query := url.Values{}
	query.Set("_busy_timeout", strconv.FormatInt(busyTimeout.Milliseconds(), 10))
	query.Set("_foreign_keys", "on")
	query.Set("_journal_mode", "wal")
	query.Set("_synchronous", "full")
	query.Set("_auto_vacuum", "incremental")
	query.Add("_pragma", "secure_delete=on")
	return "file:" + filepath.ToSlash(path) + "?" + query.Encode()
}

func nonEmptyFile(path string) (bool, error) {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("stat sqlite file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return false, fmt.Errorf("sqlite path is not a regular file")
	}
	return info.Size() > 0, nil
}

// Close releases the connection pool.
func (s *Store) Close() error {
	return s.db.Close()
}

// Path returns the daemon-owned database path.
func (s *Store) Path() string {
	return s.path
}

// MigrationBackups returns a copy of backups created by Open.
func (s *Store) MigrationBackups() []string {
	return append([]string(nil), s.migrationBackups...)
}

func userVersion(ctx context.Context, db *sql.DB) (int, error) {
	var version int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return 0, fmt.Errorf("read schema version: %w", err)
	}
	return version, nil
}
