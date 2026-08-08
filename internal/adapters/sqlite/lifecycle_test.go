package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/qingbo1011/memxplore/internal/application"
	"github.com/qingbo1011/memxplore/internal/domain"
	"github.com/qingbo1011/memxplore/internal/policy"
)

func proposal(id string, kind application.ProposalKind, target domain.ID, payload any, createdAt time.Time) application.Proposal {
	encoded, err := json.Marshal(payload)
	if err != nil {
		panic(err)
	}
	return application.Proposal{
		ID: domain.ID(id), Namespace: "ns-test", ObservationIDs: []domain.ID{domain.ID("obs-" + id)},
		Kind: kind, TargetID: target, Payload: encoded,
		StrategyID: "test.lifecycle@1.0.0", StrategyHash: strings.Repeat("1", 64), CreatedAt: createdAt,
	}
}

func factualCreate(text string, validFrom time.Time, derived []domain.ID) application.MemoryCreate {
	return application.MemoryCreate{
		Scope: testObservation("obs-create", text).Scope, Function: domain.FunctionFactual,
		Taxonomy: domain.Taxonomy{
			Forms: []string{"token-flat"}, Functions: []string{"factual"}, Dynamics: []string{"formation", "evolution", "retrieval"},
		},
		Payload: domain.MemoryPayload{Factual: &domain.FactualMemory{
			ClaimSubject: "subject-alice", Predicate: "favorite-color",
			Object:    domain.Content{Parts: []domain.ContentPart{{Kind: domain.PartText, Text: text}}},
			Epistemic: domain.EpistemicObserved,
		}},
		Provenance: []domain.EvidenceRef{{ObservationID: "obs-create", PartIndex: 0}},
		ValidTime:  &domain.TimeRange{From: validFrom}, DerivedFrom: derived,
	}
}

func factualEvolution(text string, mode application.EvolutionMode, validFrom time.Time, derived []domain.ID) application.MemoryEvolution {
	return application.MemoryEvolution{
		Mode: mode,
		Taxonomy: domain.Taxonomy{
			Forms: []string{"token-flat"}, Functions: []string{"factual"}, Dynamics: []string{"evolution", "retrieval"},
		},
		Payload: domain.MemoryPayload{Factual: &domain.FactualMemory{
			ClaimSubject: "subject-alice", Predicate: "favorite-color",
			Object:    domain.Content{Parts: []domain.ContentPart{{Kind: domain.PartText, Text: text}}},
			Epistemic: domain.EpistemicObserved,
		}},
		Provenance: []domain.EvidenceRef{{ObservationID: domain.ID("obs-evolve-" + text), PartIndex: 0}},
		ValidTime:  domain.TimeRange{From: validFrom}, DerivedFrom: derived,
	}
}

func lifecycleFilter(now time.Time) application.CandidateFilter {
	return application.CandidateFilter{
		Access: application.AccessScope{
			PrincipalID: "owner-alice", Namespace: "ns-test", PrivateOwners: []domain.ID{"owner-alice"},
		},
		Subject: "subject-alice", Context: "task-test", ValidAt: now, SystemAt: now,
	}
}

func TestCreateSupersedeHistoricalRecallConflictAndIdempotency(t *testing.T) {
	ctx := context.Background()
	store := testStore(t)
	base := time.Date(2026, 8, 8, 4, 0, 0, 0, time.UTC)
	createProposal := proposal("proposal-create-blue", application.ProposalCreate, "", factualCreate("blue", base, nil), base)
	memory, first, operation, err := store.ApplyProposal(ctx, createProposal, "actor-test", base.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	replayedMemory, replayedVersion, replayedOperation, err := store.ApplyProposal(ctx, createProposal, "actor-test", base.Add(2*time.Minute))
	if err != nil || replayedMemory.ID != memory.ID || replayedVersion.ID != first.ID || replayedOperation.ID != operation.ID {
		t.Fatalf("idempotent replay memory=%+v version=%+v operation=%+v err=%v", replayedMemory, replayedVersion, replayedOperation, err)
	}
	var operationCount int
	if err := store.db.QueryRow("SELECT count(*) FROM operations WHERE proposal_id = ?", createProposal.ID).Scan(&operationCount); err != nil || operationCount != 1 {
		t.Fatalf("operation count=%d err=%v", operationCount, err)
	}

	updateAt := base.Add(time.Hour)
	updateProposal := proposal("proposal-update-green", application.ProposalUpdate, memory.ID,
		factualEvolution("green", application.EvolutionSupersede, updateAt, nil), updateAt)
	updated, second, _, err := store.ApplyProposal(ctx, updateProposal, "actor-test", updateAt.Add(time.Minute))
	if err != nil || updated.CurrentVersion != 2 || second.Number != 2 || len(second.Supersedes) != 1 || second.Supersedes[0] != first.ID {
		t.Fatalf("updated=%+v second=%+v err=%v", updated, second, err)
	}
	var oldState string
	var oldSystemTo, oldValidTo sql.NullString
	if err := store.db.QueryRow("SELECT state, system_to, valid_to FROM memory_versions WHERE id = ?", first.ID).Scan(&oldState, &oldSystemTo, &oldValidTo); err != nil {
		t.Fatal(err)
	}
	if oldState != "superseded" || !oldSystemTo.Valid || !oldValidTo.Valid {
		t.Fatalf("old version state=%q system_to=%v valid_to=%v", oldState, oldSystemTo, oldValidTo)
	}

	historical := lifecycleFilter(base.Add(30 * time.Minute))
	historical.SystemAt = base.Add(30 * time.Minute)
	hits, err := store.SearchLexicalCandidates(ctx, historical, "favorite blue", 10)
	if err != nil || len(hits) != 1 || hits[0].VersionID != first.ID {
		t.Fatalf("historical hits=%+v err=%v", hits, err)
	}
	current := lifecycleFilter(updateAt.Add(2 * time.Minute))
	hits, err = store.SearchLexicalCandidates(ctx, current, "favorite green", 10)
	if err != nil || len(hits) != 1 || hits[0].VersionID != second.ID {
		t.Fatalf("current hits=%+v err=%v", hits, err)
	}

	conflictProposal := proposal("proposal-conflict-red", application.ProposalUpdate, memory.ID,
		factualEvolution("red", application.EvolutionConflict, updateAt, nil), updateAt.Add(2*time.Minute))
	sibling, conflicting, _, err := store.ApplyProposal(ctx, conflictProposal, "actor-test", updateAt.Add(3*time.Minute))
	if err != nil || sibling.ID == memory.ID || conflicting.ConflictGroup == "" {
		t.Fatalf("sibling=%+v conflicting=%+v err=%v", sibling, conflicting, err)
	}
	current = lifecycleFilter(updateAt.Add(4 * time.Minute))
	hits, err = store.SearchLexicalCandidates(ctx, current, "favorite color", 10)
	if err != nil || len(hits) != 2 || hits[0].ConflictGroup == "" || hits[0].ConflictGroup != hits[1].ConflictGroup {
		t.Fatalf("conflict hits=%+v err=%v", hits, err)
	}
}

func TestDerivedMemoryBecomesStaleAndCanRebuild(t *testing.T) {
	ctx := context.Background()
	store := testStore(t)
	base := time.Date(2026, 8, 8, 5, 0, 0, 0, time.UTC)
	parentProposal := proposal("proposal-parent", application.ProposalCreate, "", factualCreate("parent-v1", base, nil), base)
	parent, parentV1, _, err := store.ApplyProposal(ctx, parentProposal, "actor-test", base.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	childProposal := proposal("proposal-child", application.ProposalCreate, "", factualCreate("derived-v1", base, []domain.ID{parentV1.ID}), base)
	child, childV1, _, err := store.ApplyProposal(ctx, childProposal, "actor-test", base.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	parentUpdate := proposal("proposal-parent-v2", application.ProposalUpdate, parent.ID,
		factualEvolution("parent-v2", application.EvolutionSupersede, base.Add(time.Hour), nil), base.Add(time.Hour))
	_, parentV2, _, err := store.ApplyProposal(ctx, parentUpdate, "actor-test", base.Add(time.Hour+time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	var childState string
	if err := store.db.QueryRow("SELECT state FROM memory_versions WHERE id = ?", childV1.ID).Scan(&childState); err != nil || childState != "stale" {
		t.Fatalf("child state=%q err=%v", childState, err)
	}
	rebuild := proposal("proposal-child-v2", application.ProposalConsolidate, child.ID,
		factualEvolution("derived-v2", application.EvolutionRebuild, base.Add(time.Hour), []domain.ID{parentV2.ID}), base.Add(time.Hour))
	_, childV2, _, err := store.ApplyProposal(ctx, rebuild, "actor-test", base.Add(time.Hour+2*time.Minute))
	if err != nil || childV2.State != domain.VersionCurrent || childV2.Number != 2 || len(childV2.DerivedFrom) != 1 {
		t.Fatalf("rebuilt child=%+v err=%v", childV2, err)
	}
}

func TestArchiveAndForgetHaveDistinctIndexSemantics(t *testing.T) {
	ctx := context.Background()
	store := testStore(t)
	base := time.Now().UTC()
	create := proposal("proposal-state-create", application.ProposalCreate, "", factualCreate("stateful", base, nil), base)
	memory, version, _, err := store.ApplyProposal(ctx, create, "actor-test", base.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	archive := proposal("proposal-archive", application.ProposalArchive, memory.ID, struct{}{}, base.Add(time.Minute))
	archived, archivedVersion, _, err := store.ApplyProposal(ctx, archive, "actor-test", base.Add(time.Minute))
	if err != nil || archived.State != domain.MemoryArchived || archivedVersion.State != domain.VersionArchived {
		t.Fatalf("archived=%+v version=%+v err=%v", archived, archivedVersion, err)
	}
	var indexed int
	if err := store.db.QueryRow("SELECT count(*) FROM memory_fts WHERE memory_version_id = ?", version.ID).Scan(&indexed); err != nil || indexed != 1 {
		t.Fatalf("archive index count=%d err=%v", indexed, err)
	}
	forget := proposal("proposal-forget", application.ProposalForget, memory.ID, struct{}{}, base.Add(2*time.Minute))
	forgotten, forgottenVersion, _, err := store.ApplyProposal(ctx, forget, "actor-test", base.Add(2*time.Minute))
	if err != nil || forgotten.State != domain.MemoryForgotten || forgottenVersion.State != domain.VersionForgotten {
		t.Fatalf("forgotten=%+v version=%+v err=%v", forgotten, forgottenVersion, err)
	}
	if err := store.db.QueryRow("SELECT count(*) FROM memory_fts WHERE memory_id = ?", memory.ID).Scan(&indexed); err != nil || indexed != 0 {
		t.Fatalf("forget index count=%d err=%v", indexed, err)
	}
}

func TestPurgeCascadesDerivedMemoriesOrMarksThemStaleExplicitly(t *testing.T) {
	for _, mode := range []PurgeMode{PurgeCascade, PurgeMarkStale} {
		t.Run(string(mode), func(t *testing.T) {
			ctx := context.Background()
			store := testStore(t)
			base := time.Date(2026, 8, 8, 9, 0, 0, 0, time.UTC)
			parent, parentVersion, _, err := store.ApplyProposal(ctx,
				proposal("proposal-purge-parent-"+string(mode), application.ProposalCreate, "", factualCreate("purge-parent", base, nil), base),
				"actor-test", base.Add(time.Minute))
			if err != nil {
				t.Fatal(err)
			}
			child, childVersion, _, err := store.ApplyProposal(ctx,
				proposal("proposal-purge-child-"+string(mode), application.ProposalCreate, "", factualCreate("purge-derived", base, []domain.ID{parentVersion.ID}), base),
				"actor-test", base.Add(2*time.Minute))
			if err != nil {
				t.Fatal(err)
			}
			receipt, err := store.PurgeMemoryWithMode(ctx, domain.ID("receipt-"+string(mode)), "ns-test", "actor-admin", parent.ID, mode, base.Add(time.Hour))
			if err != nil {
				t.Fatal(err)
			}
			var childCount int
			if err := store.db.QueryRow("SELECT count(*) FROM memories WHERE id = ?", child.ID).Scan(&childCount); err != nil {
				t.Fatal(err)
			}
			if mode == PurgeCascade {
				if receipt.VersionsDeleted != 2 || childCount != 0 {
					t.Fatalf("cascade receipt=%+v childCount=%d", receipt, childCount)
				}
			} else {
				var state string
				if err := store.db.QueryRow("SELECT state FROM memory_versions WHERE id = ?", childVersion.ID).Scan(&state); err != nil {
					t.Fatal(err)
				}
				if receipt.VersionsDeleted != 1 || childCount != 1 || state != "stale" {
					t.Fatalf("mark-stale receipt=%+v childCount=%d state=%q", receipt, childCount, state)
				}
			}
		})
	}
}

func TestAuthorizedApplyBlocksOwnerBypassAndPrivateDisclosure(t *testing.T) {
	ctx := context.Background()
	store := testStore(t)
	service, err := application.NewLifecycleService(policy.OwnerPolicy{}, store)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	ownerScope := testObservation("obs-owner", "private source").Scope
	parentProposal := proposal("proposal-private-parent", application.ProposalCreate, "", factualCreate("private source", base, nil), base)
	parent, err := service.Apply(ctx, ownerScope, parentProposal, base.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}

	attackerScope := ownerScope
	attackerScope.Owner = "owner-bob"
	attackerScope.Subject = "subject-bob"
	attackerScope.Actor = "actor-bob"
	bypass := proposal("proposal-owner-bypass", application.ProposalUpdate, parent.Memory.ID,
		factualEvolution("stolen update", application.EvolutionSupersede, base.Add(2*time.Minute), nil), base.Add(2*time.Minute))
	if _, err := service.Apply(ctx, attackerScope, bypass, base.Add(3*time.Minute)); err == nil || !strings.Contains(err.Error(), "does not own evolution target") {
		t.Fatalf("owner bypass err=%v", err)
	}

	sharedCreate := factualCreate("shared derivative", base.Add(4*time.Minute), []domain.ID{parent.Version.ID})
	sharedCreate.Scope.Visibility = domain.VisibilityShared
	sharedProposal := proposal("proposal-private-to-shared", application.ProposalCreate, "", sharedCreate, base.Add(4*time.Minute))
	sharedScope := ownerScope
	sharedScope.Visibility = domain.VisibilityShared
	if _, err := service.Apply(ctx, sharedScope, sharedProposal, base.Add(5*time.Minute)); err == nil || !strings.Contains(err.Error(), "cannot flow") {
		t.Fatalf("private-to-shared disclosure err=%v", err)
	}
}
