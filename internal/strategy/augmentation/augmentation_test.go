package augmentation

import (
	"context"
	"testing"

	"github.com/qingbo1011/memxplore/internal/adapters/provider/fake"
	"github.com/qingbo1011/memxplore/internal/application"
	"github.com/qingbo1011/memxplore/internal/domain"
)

func item(id domain.ID) application.RecallItem {
	return application.RecallItem{VersionID: id, MemoryID: domain.ID("mem-" + string(id)), Function: domain.FunctionFactual}
}

func TestOptionalAugmentationStagesAreTypedAndBounded(t *testing.T) {
	provider := &fake.Provider{Responses: []application.GenerationResponse{
		{Text: `{"queries":["concise preference","answer style"]}`},
		{Text: `{"version_ids":["mv-b","mv-a"]}`},
		{Text: `{"text":"The user prefers concise answers.","citations":["mv-a"]}`},
	}}
	rewriter, err := NewQueryRewriter(provider, "fake", "fake-v1")
	if err != nil {
		t.Fatal(err)
	}
	rewrite, err := rewriter.Rewrite(context.Background(), "how should I answer?")
	if err != nil || len(rewrite.Queries) != 2 {
		t.Fatalf("rewrite=%+v err=%v", rewrite, err)
	}
	items := []application.RecallItem{item("mv-a"), item("mv-b")}
	reranker, err := NewReranker(provider, "fake", "fake-v1")
	if err != nil {
		t.Fatal(err)
	}
	reranked, err := reranker.Rerank(context.Background(), "answer style", items)
	if err != nil || reranked[0].VersionID != "mv-b" {
		t.Fatalf("reranked=%+v err=%v", reranked, err)
	}
	synthesizer, err := NewSynthesizer(provider, "fake", "fake-v1")
	if err != nil {
		t.Fatal(err)
	}
	synthesis, err := synthesizer.Synthesize(context.Background(), application.RecallBundle{Query: "answer style", Items: items})
	if err != nil || len(synthesis.Citations) != 1 || synthesis.Citations[0] != "mv-a" {
		t.Fatalf("synthesis=%+v err=%v", synthesis, err)
	}
	if len(provider.Requests) != 3 {
		t.Fatalf("provider requests=%d", len(provider.Requests))
	}
}

func TestRerankerAndSynthesizerRejectInventedVersions(t *testing.T) {
	provider := &fake.Provider{Responses: []application.GenerationResponse{
		{Text: `{"version_ids":["mv-invented"]}`},
		{Text: `{"text":"invented","citations":["mv-invented"]}`},
	}}
	reranker, err := NewReranker(provider, "fake", "fake-v1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reranker.Rerank(context.Background(), "query", []application.RecallItem{item("mv-a")}); err == nil {
		t.Fatal("reranker invented a version")
	}
	synthesizer, err := NewSynthesizer(provider, "fake", "fake-v1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := synthesizer.Synthesize(context.Background(), application.RecallBundle{
		Query: "query", Items: []application.RecallItem{item("mv-a")},
	}); err == nil {
		t.Fatal("synthesizer cited an invented version")
	}
}
