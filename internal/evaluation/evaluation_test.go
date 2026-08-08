package evaluation

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func sampleRun() Run {
	now := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	predictions := []Prediction{
		{CaseID: "case-a", Category: "factual", Variant: "no-memory", Query: "where", ExpectedReferences: []string{"session-a"}, LatencyMS: 0.1},
		{CaseID: "case-a", Category: "factual", Variant: "lexical", Query: "where", ExpectedReferences: []string{"session-a"}, Retrieved: []RankedReference{{Reference: "session-a", Rank: 1, Score: 1}}, TraceIDs: []string{"trace-a"}, LatencyMS: 2},
		{CaseID: "case-abstain", Category: "abstention", Variant: "no-memory", Query: "unknown", LatencyMS: 0.1},
		{CaseID: "case-abstain", Category: "abstention", Variant: "lexical", Query: "unknown", LatencyMS: 1},
	}
	metrics := Score(predictions, 5)
	metrics.LifecycleChecks = map[string]bool{"conflict_visible": true}
	manifest := NewManifest("run-test", "internal", "internal-v1", 7, DatasetIdentity{
		Name: "builtin", Revision: "v1", SHA256: strings.Repeat("a", 64), License: "Apache-2.0",
	}, []Variant{{ID: "no-memory", Description: "ablation"}, {ID: "lexical", Description: "FTS5"}}, now)
	manifest.TopK = 5
	manifest.CompletedAt = now.Add(time.Second)
	manifest.Limitations = []string{"deterministic fixture"}
	return Run{Manifest: manifest, Predictions: predictions, Metrics: metrics, Traces: []TraceReference{{ID: "trace-a", CaseID: "case-a", Variant: "lexical", Kind: "retrieval", Location: "sqlite:retrieval_traces/trace-a"}}}
}

func TestScoreProducesRetrievalSystemAndAblationMetrics(t *testing.T) {
	metrics := sampleRun().Metrics
	if metrics.Variants["no-memory"].RecallAtK != 0 || metrics.Variants["lexical"].RecallAtK != 1 {
		t.Fatalf("metrics=%+v", metrics.Variants)
	}
	if len(metrics.Ablations) != 1 || metrics.Ablations[0].RecallAtKDelta != 1 {
		t.Fatalf("ablations=%+v", metrics.Ablations)
	}
	if metrics.Variants["lexical"].AbstentionAccuracy != 1 || metrics.Variants["lexical"].LatencyP95MS != 2 {
		t.Fatalf("lexical=%+v", metrics.Variants["lexical"])
	}
}

func TestWriteVerifyAndRefuseOverwrite(t *testing.T) {
	root := filepath.Join(t.TempDir(), "runs")
	run := sampleRun()
	directory, err := WriteRun(root, run)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyRun(directory); err != nil {
		t.Fatal(err)
	}
	report, err := os.ReadFile(filepath.Join(directory, "report.html"))
	if err != nil || !strings.Contains(string(report), "Recall@K") || !strings.Contains(string(report), "no leaderboard claim") {
		t.Fatalf("report err=%v body=%s", err, report)
	}
	if _, err := WriteRun(root, run); !errors.Is(err, ErrRunExists) {
		t.Fatalf("overwrite error=%v", err)
	}
}

func TestVerifyDetectsTampering(t *testing.T) {
	directory, err := WriteRun(t.TempDir(), sampleRun())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(directory, 0o750); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "metrics.json")
	if err := os.Chmod(path, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{}\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := VerifyRun(directory); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("verify error=%v", err)
	}
}
