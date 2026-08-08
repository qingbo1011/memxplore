// Package observability defines a small vendor-neutral telemetry port.
package observability

import "context"

const (
	MetricRetrievalCandidates = "memxplore.retrieval.candidates"
	MetricRetrievalSelected   = "memxplore.retrieval.selected"
	MetricRetrievalTokens     = "memxplore.retrieval.tokens"
	MetricBenchmarkCases      = "memxplore.benchmark.cases"
	MetricBenchmarkFailures   = "memxplore.benchmark.failures"
)

// Attribute is deliberately string-only and must contain low-cardinality, non-content metadata.
type Attribute struct {
	Key   string
	Value string
}

// String creates a telemetry attribute.
func String(key, value string) Attribute { return Attribute{Key: key, Value: value} }

// EndOperation closes an operation span and records its outcome and duration.
type EndOperation func(error, ...Attribute)

// Recorder is the application-facing tracing and metrics port.
type Recorder interface {
	Start(context.Context, string, ...Attribute) (context.Context, EndOperation)
	Observe(context.Context, string, float64, ...Attribute)
}

type noopRecorder struct{}

func (noopRecorder) Start(ctx context.Context, _ string, _ ...Attribute) (context.Context, EndOperation) {
	return ctx, func(error, ...Attribute) {}
}

func (noopRecorder) Observe(context.Context, string, float64, ...Attribute) {}

// Nop returns a recorder that allocates no telemetry state.
func Nop() Recorder { return noopRecorder{} }

// OrNop normalizes optional recorder dependencies.
func OrNop(recorder Recorder) Recorder {
	if recorder == nil {
		return Nop()
	}
	return recorder
}
