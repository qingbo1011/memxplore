package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/qingbo1011/memxplore/internal/domain"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "memxplore.sqlite")
	store, err := Open(context.Background(), path, DefaultOptions())
	if err != nil {
		t.Fatalf("Open(): %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func testObservation(id, text string) domain.Observation {
	return domain.Observation{
		ID: domain.ID(id),
		Scope: domain.Scope{
			Namespace: "ns-test", Owner: "owner-alice", Subject: "subject-alice",
			Actor: "actor-test", Context: "task-test", Visibility: domain.VisibilityPrivate,
		},
		SourceKind: "test", Content: domain.Content{Parts: []domain.ContentPart{{Kind: domain.PartText, Text: text}}},
		EvidenceClass: domain.EvidenceUntrusted, CapturedAt: time.Now().UTC(),
	}
}

func testFactualMemory(memoryID, versionID, observationID, text string) (domain.Memory, domain.MemoryVersion) {
	now := time.Now().UTC()
	scope := testObservation(string(observationID), text).Scope
	memory := domain.Memory{
		ID: domain.ID(memoryID), Scope: scope, Function: domain.FunctionFactual,
		State: domain.MemoryActive, CurrentVersion: 1, CreatedAt: now,
	}
	version := domain.MemoryVersion{
		ID: domain.ID(versionID), MemoryID: memory.ID, Number: 1, State: domain.VersionCurrent,
		Taxonomy: domain.Taxonomy{
			Forms: []string{"token-flat"}, Functions: []string{"factual"},
			Dynamics: []string{"formation", "retrieval"}, Tags: []string{"test-fixture"},
		},
		ValidTime: domain.TimeRange{From: now}, SystemTime: domain.TimeRange{From: now},
		Provenance: []domain.EvidenceRef{{ObservationID: domain.ID(observationID), PartIndex: 0}},
		Payload: domain.MemoryPayload{Factual: &domain.FactualMemory{
			ClaimSubject: scope.Subject, Predicate: "test-claim",
			Object:    domain.Content{Parts: []domain.ContentPart{{Kind: domain.PartText, Text: text}}},
			Epistemic: domain.EpistemicObserved,
		}},
	}
	return memory, version
}

func TestStorageCapabilities(t *testing.T) {
	store := testStore(t)
	capabilities, err := store.Probe(context.Background())
	if err != nil {
		t.Fatalf("Probe(): %v", err)
	}
	if !capabilities.FTS5 || !capabilities.BM25 || !capabilities.WAL || !capabilities.ForeignKeys || !capabilities.SecureDelete {
		t.Fatalf("missing capability: %+v", capabilities)
	}
	if capabilities.BusyTimeoutMS != 5000 || capabilities.SchemaVersion != latestSchemaVersion {
		t.Fatalf("unexpected configuration: %+v", capabilities)
	}

	if _, err := store.db.Exec(`
        INSERT INTO memory_dependencies(parent_version_id, child_version_id)
        VALUES('missing-parent', 'missing-child')`); err == nil {
		t.Fatal("foreign key enforcement accepted missing versions")
	}
}

func TestBM25LexicalSearchIsNamespaceFiltered(t *testing.T) {
	ctx := context.Background()
	store := testStore(t)
	for index, text := range []string{
		"sqlite memory memory retrieval with fts",
		"sqlite storage reference",
	} {
		observationID := fmt.Sprintf("obs-%d", index)
		if err := store.PutObservation(ctx, testObservation(observationID, text)); err != nil {
			t.Fatalf("PutObservation(%d): %v", index, err)
		}
		memory, version := testFactualMemory(fmt.Sprintf("mem-%d", index), fmt.Sprintf("mv-%d", index), observationID, text)
		if err := store.PutMemory(ctx, memory, version); err != nil {
			t.Fatalf("PutMemory(%d): %v", index, err)
		}
	}
	hits, err := store.SearchLexical(ctx, "ns-test", "memory", 10)
	if err != nil {
		t.Fatalf("SearchLexical(): %v", err)
	}
	if len(hits) != 1 || hits[0].MemoryID != "mem-0" {
		t.Fatalf("hits = %+v, want only mem-0", hits)
	}
	hits, err = store.SearchLexical(ctx, "ns-other", "memory", 10)
	if err != nil {
		t.Fatalf("SearchLexical(other): %v", err)
	}
	if len(hits) != 0 {
		t.Fatalf("namespace leaked hits: %+v", hits)
	}
}

func TestConcurrentObservationWriters(t *testing.T) {
	ctx := context.Background()
	store := testStore(t)
	const writers = 24
	var wait sync.WaitGroup
	errors := make(chan error, writers)
	for index := range writers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			errors <- store.PutObservation(ctx,
				testObservation(fmt.Sprintf("obs-concurrent-%d", index), fmt.Sprintf("writer %d", index)))
		}()
	}
	wait.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatalf("concurrent insert: %v", err)
		}
	}
	count, err := store.ObservationCount(ctx)
	if err != nil {
		t.Fatalf("ObservationCount(): %v", err)
	}
	if count != writers {
		t.Fatalf("observation count = %d, want %d", count, writers)
	}
}

func TestExistingDatabaseIsBackedUpBeforeMigration(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	path := filepath.Join(directory, "legacy.sqlite")
	legacy, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path))
	if err != nil {
		t.Fatalf("open legacy database: %v", err)
	}
	if _, err := legacy.Exec("CREATE TABLE legacy_marker(value TEXT); INSERT INTO legacy_marker VALUES('before-migration')"); err != nil {
		t.Fatalf("create legacy database: %v", err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatalf("close legacy database: %v", err)
	}

	store, err := Open(ctx, path, DefaultOptions())
	if err != nil {
		t.Fatalf("Open(legacy): %v", err)
	}
	defer store.Close()
	backups := store.MigrationBackups()
	if len(backups) != 1 {
		t.Fatalf("migration backups = %v, want one", backups)
	}
	if err := ValidateIntegrity(ctx, backups[0]); err != nil {
		t.Fatalf("ValidateIntegrity(backup): %v", err)
	}
	backup, err := sql.Open("sqlite", "file:"+filepath.ToSlash(backups[0])+"?_query_only=1")
	if err != nil {
		t.Fatalf("open migration backup: %v", err)
	}
	defer backup.Close()
	var marker string
	if err := backup.QueryRow("SELECT value FROM legacy_marker").Scan(&marker); err != nil || marker != "before-migration" {
		t.Fatalf("backup marker = %q, err = %v", marker, err)
	}
}

func TestOnlineBackupAndRestore(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	store, err := Open(ctx, filepath.Join(directory, "source.sqlite"), DefaultOptions())
	if err != nil {
		t.Fatalf("Open(): %v", err)
	}
	defer store.Close()
	if err := store.PutObservation(ctx, testObservation("obs-before-backup", "retained")); err != nil {
		t.Fatalf("PutObservation(before): %v", err)
	}
	backupPath := filepath.Join(directory, "backup.sqlite")
	if err := store.Backup(ctx, backupPath); err != nil {
		t.Fatalf("Backup(): %v", err)
	}
	if err := store.PutObservation(ctx, testObservation("obs-after-backup", "not restored")); err != nil {
		t.Fatalf("PutObservation(after): %v", err)
	}
	restoredPath := filepath.Join(directory, "restored.sqlite")
	if _, err := RestoreFile(ctx, backupPath, restoredPath, false); err != nil {
		t.Fatalf("RestoreFile(): %v", err)
	}
	restored, err := Open(ctx, restoredPath, DefaultOptions())
	if err != nil {
		t.Fatalf("Open(restored): %v", err)
	}
	defer restored.Close()
	count, err := restored.ObservationCount(ctx)
	if err != nil || count != 1 {
		t.Fatalf("restored count = %d, err = %v, want 1", count, err)
	}
}

func TestSecurityGatePurgeRemovesContentIndexAndWALResidue(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	path := filepath.Join(directory, "purge.sqlite")
	store, err := Open(ctx, path, DefaultOptions())
	if err != nil {
		t.Fatalf("Open(): %v", err)
	}
	const secret = "sensitive-purge-needle-7f2de0"
	if err := store.PutObservation(ctx, testObservation("obs-purge", "non-sensitive provenance anchor")); err != nil {
		t.Fatalf("PutObservation(): %v", err)
	}
	memory, version := testFactualMemory("mem-purge", "mv-purge", "obs-purge", secret)
	if err := store.PutMemory(ctx, memory, version); err != nil {
		t.Fatalf("PutMemory(): %v", err)
	}
	if _, err := store.PurgeMemory(ctx, "receipt-purge", "ns-test", "actor-admin", "mem-purge", time.Now().UTC()); err != nil {
		t.Fatalf("PurgeMemory(): %v", err)
	}
	hits, err := store.SearchLexical(ctx, "ns-test", "sensitive", 10)
	if err != nil || len(hits) != 0 {
		t.Fatalf("post-purge hits = %+v, err = %v", hits, err)
	}
	var versions int
	if err := store.db.QueryRow("SELECT count(*) FROM memory_versions WHERE memory_id = 'mem-purge'").Scan(&versions); err != nil || versions != 0 {
		t.Fatalf("post-purge versions = %d, err = %v", versions, err)
	}
	var receiptColumns int
	if err := store.db.QueryRow("SELECT versions_deleted FROM purge_receipts WHERE id = 'receipt-purge'").Scan(&receiptColumns); err != nil || receiptColumns != 1 {
		t.Fatalf("purge receipt versions = %d, err = %v", receiptColumns, err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close(): %v", err)
	}
	for _, candidate := range []string{path, path + "-wal"} {
		data, err := os.ReadFile(candidate)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			t.Fatalf("read %s: %v", candidate, err)
		}
		if bytes.Contains(data, []byte(secret)) {
			t.Fatalf("purged content remains in %s", candidate)
		}
	}
}
