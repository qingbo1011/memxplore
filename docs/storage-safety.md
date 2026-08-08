# SQLite storage safety milestone

MemXplore uses Go 1.26 and `modernc.org/sqlite` so release binaries remain CGO-free. The storage adapter treats its configuration as executable requirements rather than deployment hints.

## Enforced on every pooled connection

- WAL journal mode.
- Foreign-key enforcement.
- A 5-second default busy timeout.
- FULL synchronous durability.
- Incremental auto-vacuum.
- SQLite secure deletion and FTS5 secure deletion.

## Migration policy

Embedded migrations are contiguous, hashed, recorded, and applied transactionally. Opening a non-empty older database first creates an online backup and verifies `integrity_check` and `foreign_key_check`. A database newer than the binary is rejected.

## Backup and restore

Backups and restores use SQLite's online backup API, not filesystem copies of a live WAL database. Every backup and restored temporary database is integrity-checked. Restore refuses to overwrite by default; explicit overwrite preserves the replaced target as a recoverable pre-restore backup.

## Purge

Purge is an explicit irreversible content operation. It removes memory versions, derived dependency edges, artifact references, and FTS entries, then checkpoints and truncates the WAL. Only a non-content receipt remains. Archive, decay, and forget do not invoke purge.

Automated tests exercise FTS5, BM25, WAL, foreign keys, busy timeout, migration backup, concurrent writers, backup/restore, and purge residual checks.
