package evaluation

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestLongMemEvalV1AdapterIngestsScoresAndLabelsPartialRun(t *testing.T) {
	now := time.Date(2026, 8, 8, 14, 0, 0, 0, time.UTC)
	run, err := RunLongMemEvalV1(context.Background(), LongMemEvalV1Config{
		DatasetPath: filepath.Join("testdata", "longmemeval_v1_fixture.json"), Revision: "fixture-v1",
		RunID: "longmemeval-v1-test", Limit: 2, TopK: 5, WorkDir: t.TempDir(), Clock: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := run.Validate(); err != nil {
		t.Fatal(err)
	}
	if len(run.Predictions) != 4 || run.Metrics.IndexedUnits != 3 || run.Metrics.IngestTokens == 0 {
		t.Fatalf("predictions=%d indexed=%d tokens=%d", len(run.Predictions), run.Metrics.IndexedUnits, run.Metrics.IngestTokens)
	}
	lexical := run.Metrics.Variants["lexical"]
	if lexical.RecallAtK != 1 || lexical.MRR != 1 || lexical.AbstentionAccuracy != 1 {
		t.Fatalf("lexical=%+v", lexical)
	}
	if len(run.Traces) != 2 {
		t.Fatalf("traces=%d", len(run.Traces))
	}
	if len(run.Manifest.Limitations) < 5 {
		t.Fatalf("limitations=%v", run.Manifest.Limitations)
	}
}

func TestLongMemEvalV1RejectsMisalignedDataset(t *testing.T) {
	instance := longMemEvalV1Instance{
		QuestionID: "bad", QuestionType: "single-session-user", Question: "question", Answer: []byte(`"answer"`),
		HaystackSessionIDs: []string{"s1"}, HaystackSessions: [][]longMemEvalV1Turn{{{Role: "user", Content: "text"}}},
		AnswerSessionIDs: []string{"missing"},
	}
	if err := validateLongMemEvalV1(instance); err == nil {
		t.Fatal("misaligned dataset passed validation")
	}
}
