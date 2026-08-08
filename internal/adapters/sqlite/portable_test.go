package sqlite

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/qingbo1011/memxplore/internal/application"
	"github.com/qingbo1011/memxplore/internal/domain"
)

func TestSubjectExportImportAuthorizationDryRunAndBackup(t *testing.T) {
	ctx := context.Background()
	source := testStore(t)
	privateAlice := testObservation("obs-portable-alice", "alice private portable fact")
	privateBob := testObservation("obs-portable-bob", "bob private portable fact")
	privateBob.Scope.Owner = "owner-bob"
	shared := testObservation("obs-portable-shared", "shared portable fact")
	shared.Scope.Owner = "owner-team"
	shared.Scope.Visibility = domain.VisibilityShared
	otherSubject := testObservation("obs-portable-other", "other subject fact")
	otherSubject.Scope.Subject = "subject-other"
	for _, observation := range []domain.Observation{privateAlice, privateBob, shared, otherSubject} {
		putPortableFixture(t, ctx, source, observation)
	}

	access := application.AccessScope{
		PrincipalID: "principal-alice", Namespace: "ns-test", PrivateOwners: []domain.ID{"owner-alice"},
		AllowShared: true,
	}
	at := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	export, err := source.ExportSubject(ctx, access, "subject-alice", at)
	if err != nil {
		t.Fatalf("ExportSubject(): %v", err)
	}
	if len(export.Observations) != 2 || len(export.Memories) != 2 {
		t.Fatalf("authorized export counts = observations %d, memories %d, want 2 and 2", len(export.Observations), len(export.Memories))
	}
	malformed := export
	malformed.Memories = append([]application.PortableMemory(nil), export.Memories...)
	malformed.Memories[0].Versions = append([]domain.MemoryVersion(nil), export.Memories[0].Versions...)
	malformed.Memories[0].Versions[0].DerivedFrom = []domain.ID{malformed.Memories[0].Versions[0].ID}
	if err := malformed.Validate(); err == nil {
		t.Fatal("portable export accepted a cyclic dependency")
	}
	for _, observation := range export.Observations {
		if observation.Scope.Owner == "owner-bob" || observation.Scope.Subject == "subject-other" {
			t.Fatalf("unauthorized observation exported: %+v", observation.Scope)
		}
	}

	dryRunTarget := testStore(t)
	dryRunResult, err := dryRunTarget.ImportSubject(ctx, export, true)
	if err != nil {
		t.Fatalf("ImportSubject(dry-run): %v", err)
	}
	if !dryRunResult.DryRun || dryRunResult.BackupPath != "" {
		t.Fatalf("dry-run result = %+v", dryRunResult)
	}
	if count, err := dryRunTarget.ObservationCount(ctx); err != nil || count != 0 {
		t.Fatalf("dry-run observation count = %d, err = %v, want zero", count, err)
	}

	directory := t.TempDir()
	target, err := Open(ctx, filepath.Join(directory, "target.sqlite"), DefaultOptions())
	if err != nil {
		t.Fatalf("Open(target): %v", err)
	}
	defer target.Close()
	unrelated := testObservation("obs-target-existing", "existing target data")
	unrelated.Scope.Subject = "subject-target-existing"
	if err := target.PutObservation(ctx, unrelated); err != nil {
		t.Fatalf("PutObservation(existing): %v", err)
	}
	result, err := target.ImportSubject(ctx, export, false)
	if err != nil {
		t.Fatalf("ImportSubject(): %v", err)
	}
	if result.BackupPath == "" {
		t.Fatal("non-empty target import did not create a backup")
	}
	if _, err := os.Stat(result.BackupPath); err != nil {
		t.Fatalf("stat pre-import backup: %v", err)
	}
	if err := ValidateIntegrity(ctx, result.BackupPath); err != nil {
		t.Fatalf("ValidateIntegrity(pre-import backup): %v", err)
	}
	roundTrip, err := target.ExportSubject(ctx, access, "subject-alice", at)
	if err != nil {
		t.Fatalf("ExportSubject(round-trip): %v", err)
	}
	if !reflect.DeepEqual(roundTrip, export) {
		t.Fatalf("round-trip export differs\n got: %+v\nwant: %+v", roundTrip, export)
	}
}

func TestSubjectExportRejectsIncompleteReferenceClosure(t *testing.T) {
	ctx := context.Background()
	store := testStore(t)
	observation := testObservation("obs-portable-closure", "dependency closure")
	putPortableFixture(t, ctx, store, observation)
	if _, err := store.db.ExecContext(ctx, "DELETE FROM observations WHERE id = ?", observation.ID); err != nil {
		t.Fatalf("delete fixture observation: %v", err)
	}
	access := application.AccessScope{
		PrincipalID: "principal-alice", Namespace: "ns-test", PrivateOwners: []domain.ID{"owner-alice"},
	}
	if _, err := store.ExportSubject(ctx, access, "subject-alice", time.Now().UTC()); err == nil {
		t.Fatal("ExportSubject() accepted an absent provenance observation")
	}
}

func putPortableFixture(t *testing.T, ctx context.Context, store *Store, observation domain.Observation) {
	t.Helper()
	if err := store.PutObservation(ctx, observation); err != nil {
		t.Fatalf("PutObservation(%s): %v", observation.ID, err)
	}
	memory, version := testFactualMemory("mem-"+string(observation.ID), "mv-"+string(observation.ID), string(observation.ID), observation.Content.PlainText())
	memory.Scope = observation.Scope
	version.Payload.Factual.ClaimSubject = observation.Scope.Subject
	if err := store.PutMemory(ctx, memory, version); err != nil {
		t.Fatalf("PutMemory(%s): %v", memory.ID, err)
	}
}
