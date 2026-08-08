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

## Portable subject export and import

Portable schema 1 is independent of the private SQLite schema. An export is authorization-filtered by namespace, subject, private owner, and explicit shared/public grants. It contains observations, referenced episodes and outcomes, working sets, memories, and all immutable versions. It does not contain credentials, embeddings, jobs, retrieval traces, telemetry, artifact bytes, or model weights.

Validation rejects absent provenance, cross-subject references, dependency cycles, visibility widening, mismatched factual claim subjects, duplicate identities/version numbers, and incompatible working or experiential references. Dry-run import executes all row, dependency, and FTS inserts plus foreign-key validation inside a transaction and rolls it back. A committed import into any database containing user or operational data first creates a verified online backup. Import never overwrites conflicting identities.

## Purge

Purge is an explicit irreversible content operation. Its privacy-safe default recursively removes the target and every transitively derived memory, including versions, dependency edges, embeddings, feedback, artifact references, and FTS entries, then checkpoints and truncates the WAL. Only one aggregate non-content receipt remains. A separate `mark-stale` research mode removes only the target and excludes descendants until they are rebuilt. Archive and forget do not invoke purge; forget removes active indexes while retaining version content for an eventual explicit purge.

Automated tests exercise FTS5, BM25, WAL, foreign keys, busy timeout, migration backup, concurrent writers, portable round-trips and dry-runs, backup/restore, and purge residual checks.
