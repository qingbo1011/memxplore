package fake

import (
	"context"
	"math"
	"slices"
	"testing"

	"github.com/qingbo1011/memxplore/internal/application"
)

func TestEmbeddingsAreDeterministicNormalizedAndOrdered(t *testing.T) {
	provider := &Provider{}
	request := application.EmbeddingRequest{Model: "fake-v1", Input: []string{"alpha", "beta"}, Dimensions: 32}
	first, err := provider.Embed(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := provider.Embed(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(first.Vectors[0], second.Vectors[0]) || slices.Equal(first.Vectors[0], first.Vectors[1]) {
		t.Fatal("fake embeddings are not stable and input-sensitive")
	}
	for _, vector := range first.Vectors {
		var squared float64
		for _, value := range vector {
			squared += float64(value * value)
		}
		if math.Abs(math.Sqrt(squared)-1) > 1e-6 {
			t.Fatalf("vector norm = %f", math.Sqrt(squared))
		}
	}
}

func TestGeneratorIsScripted(t *testing.T) {
	provider := &Provider{Responses: []application.GenerationResponse{{Text: `{"kind":"create"}`}}}
	response, err := provider.Generate(context.Background(), application.GenerationRequest{
		Model: "fake-v1", Messages: []application.Message{{Role: "user", Content: "observe"}},
	})
	if err != nil || response.Text == "" || len(provider.Requests) != 1 {
		t.Fatalf("response = %+v, requests = %d, err = %v", response, len(provider.Requests), err)
	}
}
