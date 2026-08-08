package evaluation

import (
	"context"
	"testing"
	"time"
)

func TestRunInternalExercisesPairedFunctionalLifecycles(t *testing.T) {
	now := time.Date(2026, 8, 8, 13, 0, 0, 0, time.UTC)
	run, err := RunInternal(context.Background(), InternalConfig{
		RunID: "internal-test", Seed: 42, WorkDir: t.TempDir(), Clock: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := run.Validate(); err != nil {
		t.Fatal(err)
	}
	if len(run.Predictions) != 6 || run.Metrics.IndexedUnits != 5 {
		t.Fatalf("predictions=%d indexed=%d", len(run.Predictions), run.Metrics.IndexedUnits)
	}
	for check, passed := range run.Metrics.LifecycleChecks {
		if !passed {
			t.Fatalf("lifecycle check %s failed", check)
		}
	}
	lexical := run.Metrics.Variants["lexical"]
	if lexical.RecallAtK != 1 || lexical.MRR != 1 || lexical.ProviderCalls != 0 || lexical.CostUSD != 0 {
		t.Fatalf("lexical metrics=%+v", lexical)
	}
	if run.Metrics.Variants["no-memory"].RecallAtK != 0 || len(run.Metrics.Ablations) != 1 {
		t.Fatalf("metrics=%+v", run.Metrics)
	}
	if len(run.Traces) < 10 {
		t.Fatalf("trace references=%d", len(run.Traces))
	}
	for _, trace := range run.Traces {
		if _, err := ReplayTrace(trace); err != nil {
			t.Fatalf("replay trace %s: %v", trace.ID, err)
		}
	}
}
