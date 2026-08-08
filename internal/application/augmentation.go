package application

import (
	"context"

	"github.com/qingbo1011/memxplore/internal/domain"
)

// RewriteResult is an explicit optional expansion of an original query.
type RewriteResult struct {
	Original string   `json:"original"`
	Queries  []string `json:"queries"`
}

// QueryRewriter is separate from baseline retrieval and must be invoked explicitly.
type QueryRewriter interface {
	Rewrite(context.Context, string) (RewriteResult, error)
}

// Reranker is separate from candidate generation and cannot add candidates.
type Reranker interface {
	Rerank(context.Context, string, []RecallItem) ([]RecallItem, error)
}

// Synthesis is optional generated text with version-level citations.
type Synthesis struct {
	Text      string      `json:"text"`
	Citations []domain.ID `json:"citations"`
}

// Synthesizer consumes a RecallBundle but is never called by Retriever.Recall.
type Synthesizer interface {
	Synthesize(context.Context, RecallBundle) (Synthesis, error)
}
