package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/qingbo1011/memxplore/internal/application"
	"github.com/qingbo1011/memxplore/internal/domain"
)

func testJob(id, key string, now time.Time) application.Job {
	return application.Job{
		ID: domain.ID(id), Namespace: "ns-test", Kind: "formation.factual", IdempotencyKey: key,
		Payload: json.RawMessage(`{"observation_id":"obs-job"}`), AvailableAt: now,
	}
}

func TestEnqueueObservationIsAtomicAndIdempotent(t *testing.T) {
	ctx := context.Background()
	store := testStore(t)
	now := time.Now().UTC()
	observation := testObservation("obs-job", "durable formation")
	created, inserted, err := store.EnqueueObservation(ctx, observation, testJob("job-1", "capture-1", now))
	if err != nil || !inserted || created.State != application.JobQueued {
		t.Fatalf("created=%+v inserted=%v err=%v", created, inserted, err)
	}
	replayed, inserted, err := store.EnqueueObservation(ctx, testObservation("obs-replay", "ignored replay"), testJob("job-replay", "capture-1", now))
	if err != nil || inserted || replayed.ID != "job-1" {
		t.Fatalf("replayed=%+v inserted=%v err=%v", replayed, inserted, err)
	}
	count, err := store.ObservationCount(ctx)
	if err != nil || count != 1 {
		t.Fatalf("observation count=%d err=%v", count, err)
	}

	badObservation := testObservation("obs-rollback", "must roll back")
	badObservation.Content.Parts = nil
	if _, _, err := store.EnqueueObservation(ctx, badObservation, testJob("job-rollback", "capture-rollback", now)); err == nil {
		t.Fatal("invalid observation enqueue succeeded")
	}
	if _, err := store.Get(ctx, "job-rollback"); !errors.Is(err, application.ErrJobNotFound) {
		t.Fatalf("rolled back job lookup err=%v", err)
	}
}

func TestLeaseRecoveryOwnershipRetryAndCompletion(t *testing.T) {
	ctx := context.Background()
	store := testStore(t)
	base := time.Now().UTC().Truncate(time.Microsecond)
	if _, _, err := store.EnqueueObservation(ctx, testObservation("obs-job", "lease recovery"), testJob("job-lease", "lease-1", base)); err != nil {
		t.Fatal(err)
	}
	first, err := store.Claim(ctx, "worker-a", base, time.Second)
	if err != nil || first.Attempts != 1 {
		t.Fatalf("first claim=%+v err=%v", first, err)
	}
	if _, err := store.Claim(ctx, "worker-b", base.Add(500*time.Millisecond), time.Second); !errors.Is(err, application.ErrNoJob) {
		t.Fatalf("early second claim err=%v", err)
	}
	recovered, err := store.Claim(ctx, "worker-b", base.Add(time.Second), time.Second)
	if err != nil || recovered.Attempts != 2 || recovered.LeaseOwner != "worker-b" {
		t.Fatalf("recovered=%+v err=%v", recovered, err)
	}
	if err := store.Complete(ctx, recovered.ID, "worker-a", json.RawMessage(`{"ok":true}`), base.Add(1200*time.Millisecond)); !errors.Is(err, application.ErrLeaseLost) {
		t.Fatalf("foreign complete err=%v", err)
	}
	if err := store.Fail(ctx, recovered.ID, "worker-b", "temporary", "retry", base.Add(1200*time.Millisecond), time.Second, 3); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Claim(ctx, "worker-c", base.Add(2200*time.Millisecond), time.Second); err != nil {
		t.Fatal(err)
	}
	if err := store.Complete(ctx, "job-lease", "worker-c", json.RawMessage(`{"proposal_id":"proposal-1"}`), base.Add(2500*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	finished, err := store.Wait(ctx, "job-lease", time.Millisecond)
	if err != nil || finished.State != application.JobSucceeded || string(finished.Result) == "" {
		t.Fatalf("finished=%+v err=%v", finished, err)
	}
}

func TestFailureBecomesTerminalAtAttemptLimit(t *testing.T) {
	ctx := context.Background()
	store := testStore(t)
	base := time.Now().UTC()
	if _, _, err := store.EnqueueObservation(ctx, testObservation("obs-job", "terminal failure"), testJob("job-fail", "fail-1", base)); err != nil {
		t.Fatal(err)
	}
	claimed, err := store.Claim(ctx, "worker-a", base, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Fail(ctx, claimed.ID, "worker-a", "invalid_output", "bad schema", base.Add(time.Millisecond), 0, 1); err != nil {
		t.Fatal(err)
	}
	finished, err := store.Wait(ctx, claimed.ID, time.Millisecond)
	if err != nil || finished.State != application.JobFailed || finished.ErrorCode != "invalid_output" {
		t.Fatalf("finished=%+v err=%v", finished, err)
	}
}
