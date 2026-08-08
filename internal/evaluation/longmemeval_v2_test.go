package evaluation

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestLongMemEvalV2SmallAdapterValidatesMaterialization(t *testing.T) {
	now := time.Date(2026, 8, 8, 15, 0, 0, 0, time.UTC)
	run, err := RunLongMemEvalV2Small(context.Background(), LongMemEvalV2Config{
		DataRoot: filepath.Join("testdata", "longmemeval_v2_small"), Revision: "fixture-v2", RunID: "longmemeval-v2-test",
		Limit: 2, ExpectedHaystackSize: 2, Clock: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := run.Validate(); err != nil {
		t.Fatal(err)
	}
	if len(run.Predictions) != 4 || len(run.Traces) != 2 || run.Metrics.IndexedUnits != 2 || run.Metrics.IngestTokens == 0 {
		t.Fatalf("predictions=%d traces=%d indexed=%d tokens=%d", len(run.Predictions), len(run.Traces), run.Metrics.IndexedUnits, run.Metrics.IngestTokens)
	}
	adapter := run.Metrics.Variants["schema-adapter"]
	if adapter.RecallAtK != 1 || adapter.MRR != 1 || adapter.Failures != 0 {
		t.Fatalf("adapter=%+v", adapter)
	}
	for _, reference := range run.Traces {
		result, err := ReplayTrace(reference)
		if err != nil || result.CandidateCount != 2 || result.SelectedCount != 2 {
			t.Fatalf("replay=%+v err=%v", result, err)
		}
	}
}

func TestLongMemEvalV2SmallRejectsMissingTrajectory(t *testing.T) {
	_, err := RunLongMemEvalV2Small(context.Background(), LongMemEvalV2Config{
		DataRoot: filepath.Join("testdata", "longmemeval_v2_small"), Revision: "fixture-v2", Limit: 1, ExpectedHaystackSize: 3,
	})
	if err == nil {
		t.Fatal("invalid haystack size passed validation")
	}
}
