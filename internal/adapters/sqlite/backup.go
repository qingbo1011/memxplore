package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	modernsqlite "modernc.org/sqlite"
)

type backupConnection interface {
	NewBackup(string) (*modernsqlite.Backup, error)
	NewRestore(string) (*modernsqlite.Backup, error)
}

// Backup creates and integrity-checks an online SQLite backup.
func (s *Store) Backup(ctx context.Context, destination string) error {
	if destination == "" || filepath.Clean(destination) == filepath.Clean(s.path) {
		return fmt.Errorf("backup destination must differ from database path")
	}
	if _, err := os.Stat(destination); err == nil {
		return fmt.Errorf("backup destination already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat backup destination: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o750); err != nil {
		return fmt.Errorf("create backup directory: %w", err)
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire backup connection: %w", err)
	}
	defer conn.Close()
	if err := conn.Raw(func(driverConn any) error {
		backuper, ok := driverConn.(backupConnection)
		if !ok {
			return fmt.Errorf("sqlite driver does not expose online backup")
		}
		backup, err := backuper.NewBackup(destination)
		if err != nil {
			return err
		}
		for more := true; more; {
			more, err = backup.Step(256)
			if err != nil {
				_ = backup.Finish()
				return err
			}
		}
		return backup.Finish()
	}); err != nil {
		_ = os.Remove(destination)
		return fmt.Errorf("online backup: %w", err)
	}
	if err := ValidateIntegrity(ctx, destination); err != nil {
		_ = os.Remove(destination)
		return fmt.Errorf("validate backup: %w", err)
	}
	return nil
}

// RestoreResult identifies the replaced database backup, if any.
type RestoreResult struct {
	ReplacedBackup string
}

// RestoreFile verifies a backup, restores into a temporary database, then atomically installs it.
// When overwrite is true, the previous target is preserved beside it.
func RestoreFile(ctx context.Context, backupPath, targetPath string, overwrite bool) (RestoreResult, error) {
	var result RestoreResult
	if filepath.Clean(backupPath) == filepath.Clean(targetPath) {
		return result, fmt.Errorf("restore source and target must differ")
	}
	if err := ValidateIntegrity(ctx, backupPath); err != nil {
		return result, fmt.Errorf("validate restore source: %w", err)
	}
	targetExists, err := nonEmptyFile(targetPath)
	if err != nil {
		return result, err
	}
	if targetExists && !overwrite {
		return result, fmt.Errorf("restore target exists and overwrite is false")
	}
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o750); err != nil {
		return result, fmt.Errorf("create restore directory: %w", err)
	}
	tempFile, err := os.CreateTemp(filepath.Dir(targetPath), ".memxplore-restore-*.sqlite")
	if err != nil {
		return result, fmt.Errorf("create restore temporary file: %w", err)
	}
	tempPath := tempFile.Name()
	if err := tempFile.Close(); err != nil {
		_ = os.Remove(tempPath)
		return result, fmt.Errorf("close restore temporary file: %w", err)
	}
	if err := os.Remove(tempPath); err != nil {
		return result, fmt.Errorf("prepare restore temporary file: %w", err)
	}
	defer os.Remove(tempPath)

	db, err := sql.Open("sqlite", databaseDSN(tempPath, DefaultOptions().BusyTimeout))
	if err != nil {
		return result, fmt.Errorf("open restore target: %w", err)
	}
	db.SetMaxOpenConns(1)
	conn, err := db.Conn(ctx)
	if err != nil {
		_ = db.Close()
		return result, fmt.Errorf("acquire restore connection: %w", err)
	}
	err = conn.Raw(func(driverConn any) error {
		restorer, ok := driverConn.(backupConnection)
		if !ok {
			return fmt.Errorf("sqlite driver does not expose online restore")
		}
		restore, err := restorer.NewRestore(backupPath)
		if err != nil {
			return err
		}
		for more := true; more; {
			more, err = restore.Step(256)
			if err != nil {
				_ = restore.Finish()
				return err
			}
		}
		return restore.Finish()
	})
	_ = conn.Close()
	closeErr := db.Close()
	if err != nil {
		return result, fmt.Errorf("restore backup: %w", err)
	}
	if closeErr != nil {
		return result, fmt.Errorf("close restored database: %w", closeErr)
	}
	if err := ValidateIntegrity(ctx, tempPath); err != nil {
		return result, fmt.Errorf("validate restored database: %w", err)
	}

	if targetExists {
		result.ReplacedBackup = fmt.Sprintf("%s.pre-restore-%s.bak", targetPath, time.Now().UTC().Format("20060102T150405.000000000Z"))
		if err := os.Rename(targetPath, result.ReplacedBackup); err != nil {
			return RestoreResult{}, fmt.Errorf("preserve restore target: %w", err)
		}
	}
	if err := os.Rename(tempPath, targetPath); err != nil {
		if result.ReplacedBackup != "" {
			_ = os.Rename(result.ReplacedBackup, targetPath)
		}
		return RestoreResult{}, fmt.Errorf("install restored database: %w", err)
	}
	return result, nil
}

func migrationBackupPath(databasePath, directory string, from, to int) (string, error) {
	if directory == "" {
		directory = filepath.Dir(databasePath)
	}
	if err := os.MkdirAll(directory, 0o750); err != nil {
		return "", fmt.Errorf("create migration backup directory: %w", err)
	}
	name := fmt.Sprintf("%s.pre-migration-v%d-to-v%d-%s.bak",
		filepath.Base(databasePath), from, to, time.Now().UTC().Format("20060102T150405.000000000Z"))
	return filepath.Join(directory, name), nil
}
