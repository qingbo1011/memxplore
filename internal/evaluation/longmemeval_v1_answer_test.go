package evaluation

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	providerfake "github.com/qingbo1011/memxplore/internal/adapters/provider/fake"
	"github.com/qingbo1011/memxplore/internal/application"
)

func TestLongMemEvalV1AnswerSubsetPairsSameGenerator(t *testing.T) {
	provider := &providerfake.Provider{Responses: []application.GenerationResponse{
		{Text: "UNKNOWN", Usage: application.Usage{InputTokens: 20, OutputTokens: 1, TotalTokens: 21}},
		{Text: "Paris", Usage: application.Usage{InputTokens: 80, OutputTokens: 1, TotalTokens: 81}},
	}}
	now := time.Date(2026, 8, 8, 16, 0, 0, 0, time.UTC)
	run, err := RunLongMemEvalV1AnswerSubset(context.Background(), LongMemEvalV1AnswerConfig{
		DatasetPath: filepath.Join("testdata", "longmemeval_v1_fixture.json"), Revision: "fixture-v1",
		RunID: "longmemeval-v1-answer-test", Limit: 1, TopK: 5, TokenBudget: 512, MaxTokens: 32,
		WorkDir: t.TempDir(), Provider: "fake-local", Model: "fake-model", Generator: provider,
		Clock: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := run.Validate(); err != nil {
		t.Fatal(err)
	}
	if len(run.Predictions) != 2 || len(run.Traces) != 1 || len(provider.Requests) != 2 {
		t.Fatalf("predictions=%d traces=%d requests=%d", len(run.Predictions), len(run.Traces), len(provider.Requests))
	}
	if got := run.Metrics.Variants["no-memory"]; got.AnswerCases != 1 || got.AnswerAccuracy != 0 || got.ProviderCalls != 1 || got.InputTokens != 20 {
		t.Fatalf("no-memory=%+v", got)
	}
	if got := run.Metrics.Variants["lexical"]; got.AnswerCases != 1 || got.AnswerAccuracy != 1 || got.RecallAtK != 1 || got.ProviderCalls != 1 || got.InputTokens != 80 {
		t.Fatalf("lexical=%+v", got)
	}
	if strings.Contains(provider.Requests[0].Messages[1].Content, "MEMORY EVIDENCE") || !strings.Contains(provider.Requests[1].Messages[1].Content, "BEGIN UNTRUSTED MEMORY EVIDENCE") {
		t.Fatalf("paired prompts are not isolated: %+v", provider.Requests)
	}
	if got := run.Metrics.Ablations[0].AnswerAccuracyDelta; got != 1 {
		t.Fatalf("answer accuracy delta=%v", got)
	}
}

func TestLongMemEvalV1ScalarAnswerAcceptsNumber(t *testing.T) {
	answer, err := longMemEvalV1ScalarAnswer([]byte(`21.5`))
	if err != nil || answer != "21.5" {
		t.Fatalf("answer=%q err=%v", answer, err)
	}
	if _, err := longMemEvalV1ScalarAnswer([]byte(`["no"]`)); err == nil {
		t.Fatal("composite answer passed validation")
	}
}
