// Package fake provides deterministic providers for CI, evaluation, and tests.
package fake

import (
	"context"
	"crypto/sha256"
	"fmt"
	"math"
	"sync"

	"github.com/qingbo1011/memxplore/internal/application"
)

// Provider implements both generator and embedding ports without network I/O.
type Provider struct {
	mu        sync.Mutex
	Responses []application.GenerationResponse
	Requests  []application.GenerationRequest
}

// Generate pops a scripted response and records a copy of the request.
func (p *Provider) Generate(ctx context.Context, request application.GenerationRequest) (application.GenerationResponse, error) {
	if err := ctx.Err(); err != nil {
		return application.GenerationResponse{}, err
	}
	if err := request.Validate(); err != nil {
		return application.GenerationResponse{}, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.Requests = append(p.Requests, request)
	if len(p.Responses) == 0 {
		return application.GenerationResponse{}, fmt.Errorf("fake generator has no scripted response")
	}
	response := p.Responses[0]
	p.Responses = p.Responses[1:]
	return response, nil
}

// Embed returns stable unit vectors derived from each complete input string.
func (p *Provider) Embed(ctx context.Context, request application.EmbeddingRequest) (application.EmbeddingResponse, error) {
	if err := ctx.Err(); err != nil {
		return application.EmbeddingResponse{}, err
	}
	if err := request.Validate(); err != nil {
		return application.EmbeddingResponse{}, err
	}
	dimensions := request.Dimensions
	if dimensions == 0 {
		dimensions = 16
	}
	if dimensions > 16384 {
		return application.EmbeddingResponse{}, fmt.Errorf("fake embedding dimensions exceed 16384")
	}
	vectors := make([][]float32, len(request.Input))
	for index, input := range request.Input {
		vectors[index] = hashVector(request.Model+"\x00"+input, dimensions)
	}
	return application.EmbeddingResponse{Vectors: vectors}, nil
}

func hashVector(input string, dimensions int) []float32 {
	vector := make([]float32, dimensions)
	var squared float64
	for index := range vector {
		digest := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%d", input, index)))
		value := float64(int(digest[0])-128) / 128
		vector[index] = float32(value)
		squared += value * value
	}
	norm := float32(math.Sqrt(squared))
	for index := range vector {
		vector[index] /= norm
	}
	return vector
}
