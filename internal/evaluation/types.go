// Package evaluation implements deterministic benchmark artifacts and adapters.
package evaluation

import (
	"encoding/json"
	"fmt"
	"runtime"
	"sort"
	"strings"
	"time"
)

const SchemaVersion = 1

// DatasetIdentity pins external or built-in evaluation input.
type DatasetIdentity struct {
	Name     string `json:"name"`
	Revision string `json:"revision"`
	SHA256   string `json:"sha256"`
	Path     string `json:"path,omitempty"`
	License  string `json:"license,omitempty"`
}

// Variant identifies a paired benchmark arm.
type Variant struct {
	ID             string   `json:"id"`
	Description    string   `json:"description"`
	StrategyIDs    []string `json:"strategy_ids,omitempty"`
	StrategyHashes []string `json:"strategy_hashes,omitempty"`
	Provider       string   `json:"provider,omitempty"`
	Model          string   `json:"model,omitempty"`
}

// RuntimeIdentity records portable system context without host secrets.
type RuntimeIdentity struct {
	GoVersion string `json:"go_version"`
	GOOS      string `json:"goos"`
	GOARCH    string `json:"goarch"`
}

// Manifest is the immutable experiment contract written after all other artifacts.
type Manifest struct {
	SchemaVersion  int               `json:"schema_version"`
	RunID          string            `json:"run_id"`
	Benchmark      string            `json:"benchmark"`
	Adapter        string            `json:"adapter"`
	Seed           int64             `json:"seed"`
	Limit          int               `json:"limit"`
	TopK           int               `json:"top_k"`
	Dataset        DatasetIdentity   `json:"dataset"`
	Variants       []Variant         `json:"variants"`
	Runtime        RuntimeIdentity   `json:"runtime"`
	StartedAt      time.Time         `json:"started_at"`
	CompletedAt    time.Time         `json:"completed_at"`
	ArtifactSHA256 map[string]string `json:"artifact_sha256"`
	Limitations    []string          `json:"limitations"`
}

// NewManifest supplies stable schema and runtime fields.
func NewManifest(runID, benchmark, adapter string, seed int64, dataset DatasetIdentity, variants []Variant, startedAt time.Time) Manifest {
	return Manifest{
		SchemaVersion: SchemaVersion, RunID: runID, Benchmark: benchmark, Adapter: adapter, Seed: seed,
		Dataset: dataset, Variants: append([]Variant(nil), variants...),
		Runtime:   RuntimeIdentity{GoVersion: runtime.Version(), GOOS: runtime.GOOS, GOARCH: runtime.GOARCH},
		StartedAt: startedAt.UTC(), ArtifactSHA256: make(map[string]string),
	}
}

// RankedReference is one retrieved ground-truth-comparable unit.
type RankedReference struct {
	Reference string  `json:"reference"`
	Rank      int     `json:"rank"`
	Score     float64 `json:"score"`
	MemoryID  string  `json:"memory_id,omitempty"`
	VersionID string  `json:"version_id,omitempty"`
}

// Prediction is one benchmark case and variant result.
type Prediction struct {
	CaseID             string            `json:"case_id"`
	Category           string            `json:"category"`
	Variant            string            `json:"variant"`
	Query              string            `json:"query"`
	ExpectedReferences []string          `json:"expected_references"`
	Retrieved          []RankedReference `json:"retrieved"`
	TraceIDs           []string          `json:"trace_ids"`
	LatencyMS          float64           `json:"latency_ms"`
	InputTokens        int               `json:"input_tokens"`
	OutputTokens       int               `json:"output_tokens"`
	ProviderCalls      int               `json:"provider_calls"`
	CostUSD            float64           `json:"cost_usd"`
	Failure            *Failure          `json:"failure,omitempty"`
}

// Failure keeps bounded failure evidence in the run artifact.
type Failure struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// TraceReference points to a replayable lifecycle or retrieval trace.
type TraceReference struct {
	ID       string          `json:"id"`
	CaseID   string          `json:"case_id"`
	Variant  string          `json:"variant"`
	Kind     string          `json:"kind"`
	Location string          `json:"location"`
	SHA256   string          `json:"sha256,omitempty"`
	Payload  json.RawMessage `json:"payload"`
}

// VariantMetrics contains quality and system measurements for one arm.
type VariantMetrics struct {
	Cases              int     `json:"cases"`
	EvaluableCases     int     `json:"evaluable_cases"`
	AbstentionCases    int     `json:"abstention_cases"`
	HitAtK             float64 `json:"hit_at_k"`
	RecallAtK          float64 `json:"recall_at_k"`
	MRR                float64 `json:"mrr"`
	AbstentionAccuracy float64 `json:"abstention_accuracy"`
	Failures           int     `json:"failures"`
	FailureRate        float64 `json:"failure_rate"`
	LatencyMeanMS      float64 `json:"latency_mean_ms"`
	LatencyP95MS       float64 `json:"latency_p95_ms"`
	InputTokens        int     `json:"input_tokens"`
	OutputTokens       int     `json:"output_tokens"`
	ProviderCalls      int     `json:"provider_calls"`
	CostUSD            float64 `json:"cost_usd"`
}

// Ablation reports paired deltas from a baseline arm.
type Ablation struct {
	Baseline       string  `json:"baseline"`
	Variant        string  `json:"variant"`
	RecallAtKDelta float64 `json:"recall_at_k_delta"`
	MRRDelta       float64 `json:"mrr_delta"`
	LatencyMSDelta float64 `json:"latency_mean_ms_delta"`
}

// Metrics contains all aggregate results without a leaderboard target.
type Metrics struct {
	SchemaVersion   int                       `json:"schema_version"`
	TopK            int                       `json:"top_k"`
	Variants        map[string]VariantMetrics `json:"variants"`
	Ablations       []Ablation                `json:"ablations"`
	LifecycleChecks map[string]bool           `json:"lifecycle_checks,omitempty"`
	IndexedUnits    int                       `json:"indexed_units"`
	IngestLatencyMS float64                   `json:"ingest_latency_ms"`
}

// Run is the complete in-memory experiment result.
type Run struct {
	Manifest    Manifest         `json:"manifest"`
	Predictions []Prediction     `json:"predictions"`
	Metrics     Metrics          `json:"metrics"`
	Traces      []TraceReference `json:"traces"`
}

// Validate rejects incomplete or internally inconsistent runs before any artifact is written.
func (r Run) Validate() error {
	manifest := r.Manifest
	if manifest.SchemaVersion != SchemaVersion || strings.TrimSpace(manifest.RunID) == "" || strings.TrimSpace(manifest.Benchmark) == "" || strings.TrimSpace(manifest.Adapter) == "" {
		return fmt.Errorf("manifest schema, run id, benchmark, and adapter are required")
	}
	if manifest.Dataset.Name == "" || len(manifest.Dataset.SHA256) != 64 || manifest.StartedAt.IsZero() || manifest.CompletedAt.Before(manifest.StartedAt) {
		return fmt.Errorf("manifest requires a pinned dataset digest and valid timestamps")
	}
	if manifest.TopK < 1 || len(manifest.Variants) < 2 || len(r.Predictions) == 0 {
		return fmt.Errorf("run requires top-k, paired variants, and predictions")
	}
	variantIDs := make(map[string]struct{}, len(manifest.Variants))
	for _, variant := range manifest.Variants {
		if variant.ID == "" {
			return fmt.Errorf("variant id is required")
		}
		if _, duplicate := variantIDs[variant.ID]; duplicate {
			return fmt.Errorf("duplicate variant %q", variant.ID)
		}
		variantIDs[variant.ID] = struct{}{}
	}
	seenPredictions := make(map[string]struct{}, len(r.Predictions))
	for _, prediction := range r.Predictions {
		if prediction.CaseID == "" || prediction.Variant == "" || prediction.Query == "" || prediction.LatencyMS < 0 || prediction.InputTokens < 0 || prediction.OutputTokens < 0 || prediction.ProviderCalls < 0 || prediction.CostUSD < 0 {
			return fmt.Errorf("prediction identity, query, and non-negative system metrics are required")
		}
		if _, ok := variantIDs[prediction.Variant]; !ok {
			return fmt.Errorf("prediction references unknown variant %q", prediction.Variant)
		}
		key := prediction.CaseID + "\x00" + prediction.Variant
		if _, duplicate := seenPredictions[key]; duplicate {
			return fmt.Errorf("duplicate prediction for case %q variant %q", prediction.CaseID, prediction.Variant)
		}
		seenPredictions[key] = struct{}{}
		for index, retrieved := range prediction.Retrieved {
			if retrieved.Reference == "" || retrieved.Rank != index+1 {
				return fmt.Errorf("prediction %q has invalid contiguous ranking", prediction.CaseID)
			}
		}
	}
	for variant := range variantIDs {
		if _, ok := r.Metrics.Variants[variant]; !ok {
			return fmt.Errorf("metrics missing variant %q", variant)
		}
	}
	traceIDs := make(map[string]struct{}, len(r.Traces))
	for _, reference := range r.Traces {
		if _, duplicate := traceIDs[reference.ID]; duplicate {
			return fmt.Errorf("duplicate trace reference %q", reference.ID)
		}
		if _, err := ReplayTrace(reference); err != nil {
			return err
		}
		traceIDs[reference.ID] = struct{}{}
	}
	for _, prediction := range r.Predictions {
		for _, traceID := range prediction.TraceIDs {
			if _, ok := traceIDs[traceID]; !ok {
				return fmt.Errorf("prediction %q references missing trace %q", prediction.CaseID, traceID)
			}
		}
	}
	return nil
}

func sortedMetricVariants(metrics map[string]VariantMetrics) []string {
	result := make([]string, 0, len(metrics))
	for variant := range metrics {
		result = append(result, variant)
	}
	sort.Strings(result)
	return result
}
