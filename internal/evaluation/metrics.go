package evaluation

import (
	"math"
	"sort"
)

// Score computes deterministic retrieval and system metrics plus paired no-memory deltas.
func Score(predictions []Prediction, topK int) Metrics {
	if topK < 1 {
		topK = 5
	}
	grouped := make(map[string][]Prediction)
	for _, prediction := range predictions {
		grouped[prediction.Variant] = append(grouped[prediction.Variant], prediction)
	}
	metrics := Metrics{SchemaVersion: SchemaVersion, TopK: topK, Variants: make(map[string]VariantMetrics, len(grouped))}
	for variant, values := range grouped {
		metrics.Variants[variant] = scoreVariant(values, topK)
	}
	baseline, exists := metrics.Variants["no-memory"]
	if exists {
		for _, variant := range sortedMetricVariants(metrics.Variants) {
			if variant == "no-memory" {
				continue
			}
			current := metrics.Variants[variant]
			metrics.Ablations = append(metrics.Ablations, Ablation{
				Baseline: "no-memory", Variant: variant,
				RecallAtKDelta:      current.RecallAtK - baseline.RecallAtK,
				MRRDelta:            current.MRR - baseline.MRR,
				AnswerAccuracyDelta: current.AnswerAccuracy - baseline.AnswerAccuracy,
				LatencyMSDelta:      current.LatencyMeanMS - baseline.LatencyMeanMS,
			})
		}
	}
	return metrics
}

func scoreVariant(predictions []Prediction, topK int) VariantMetrics {
	result := VariantMetrics{Cases: len(predictions)}
	latencies := make([]float64, 0, len(predictions))
	var hitSum, recallSum, reciprocalSum, abstentionCorrect float64
	for _, prediction := range predictions {
		latencies = append(latencies, prediction.LatencyMS)
		result.LatencyMeanMS += prediction.LatencyMS
		result.InputTokens += prediction.InputTokens
		result.OutputTokens += prediction.OutputTokens
		result.ProviderCalls += prediction.ProviderCalls
		result.CostUSD += prediction.CostUSD
		result.RetrievedTokens += prediction.RetrievedTokens
		if prediction.Failure != nil {
			result.Failures++
		}
		if prediction.AnswerCorrect != nil {
			result.AnswerCases++
			if *prediction.AnswerCorrect {
				result.AnswerCorrect++
			}
		}
		if len(prediction.ExpectedReferences) == 0 {
			result.AbstentionCases++
			if prediction.Failure == nil && len(prediction.Retrieved) == 0 {
				abstentionCorrect++
			}
			continue
		}
		result.EvaluableCases++
		expected := make(map[string]struct{}, len(prediction.ExpectedReferences))
		for _, reference := range prediction.ExpectedReferences {
			expected[reference] = struct{}{}
		}
		found := make(map[string]struct{}, len(expected))
		firstRank := 0
		for index, retrieved := range prediction.Retrieved {
			if index >= topK {
				break
			}
			if _, relevant := expected[retrieved.Reference]; relevant {
				found[retrieved.Reference] = struct{}{}
				if firstRank == 0 {
					firstRank = index + 1
				}
			}
		}
		if len(found) > 0 {
			hitSum++
		}
		recallSum += float64(len(found)) / float64(len(expected))
		if firstRank > 0 {
			reciprocalSum += 1 / float64(firstRank)
		}
	}
	if result.Cases > 0 {
		result.FailureRate = float64(result.Failures) / float64(result.Cases)
		result.LatencyMeanMS /= float64(result.Cases)
	}
	if result.EvaluableCases > 0 {
		count := float64(result.EvaluableCases)
		result.HitAtK, result.RecallAtK, result.MRR = hitSum/count, recallSum/count, reciprocalSum/count
	}
	if result.AnswerCases > 0 {
		result.AnswerAccuracy = float64(result.AnswerCorrect) / float64(result.AnswerCases)
	}
	if result.AbstentionCases > 0 {
		result.AbstentionAccuracy = abstentionCorrect / float64(result.AbstentionCases)
	}
	sort.Float64s(latencies)
	if len(latencies) > 0 {
		index := int(math.Ceil(0.95*float64(len(latencies)))) - 1
		if index < 0 {
			index = 0
		}
		result.LatencyP95MS = latencies[index]
	}
	return result
}
